//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/jetcast/client"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/realtimeauth"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/stretchr/testify/require"
)

// visitorConfig 通过公开 HTTP 接口换取匿名 Cookie 或客户签名对应的短期凭据。
func visitorConfig(t *testing.T, h *visitorRealtimeHarness, channel, anonymous, customer string) appservice.RealtimePeerConnection {
	t.Helper()
	response := h.request(t, channel, anonymous, "", http.Header{appservice.CustomerTokenHeader: []string{customer}})
	require.Equal(t, http.StatusOK, response.StatusCode)
	var config appservice.RealtimePeerConnection
	require.NoError(t, json.NewDecoder(response.Body).Decode(&config))
	return config
}

// rawPeer 建立忽略 SDK 撤销控制的真实 NATS 直订阅连接。
func rawPeer(t *testing.T, address string, config appservice.RealtimePeerConnection) *nats.Conn {
	t.Helper()
	socketID := strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:22]
	nc, err := nats.Connect("ws"+strings.TrimPrefix(address, "http")+config.Path, nats.Token(config.Token), nats.Name(socketID), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	_, err = nc.SubscribeSync(config.Prefix + ".ev.prv." + config.Channel)
	require.NoError(t, err)
	require.NoError(t, nc.Flush())
	return nc
}

// TestJetcastVisitorIdentityRevocation 验证 Cookie 恢复、身份隔离、跨节点客户密钥撤销和不合作客户端的强制关闭。
func TestJetcastVisitorIdentityRevocation(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	h := startVisitorRealtime(t, f)
	ctx := t.Context()
	const anonymous = "0123456789abcdef0123456789abcdef"
	original := visitorConfig(t, h, f.channelID, anonymous, "")
	request, err := http.NewRequest(http.MethodGet, h.url+"/public/website-channels/"+f.channelID+"/messenger", nil)
	require.NoError(t, err)
	request.AddCookie(&http.Cookie{Name: "visitor_" + f.channelID, Value: anonymous})
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	var restored appservice.WebsiteVisitorMessenger
	require.NoError(t, json.NewDecoder(response.Body).Decode(&restored))
	response.Body.Close()
	require.Equal(t, anonymous, restored.VisitorToken)
	require.Equal(t, original.UserID, visitorConfig(t, h, f.channelID, anonymous, "").UserID)
	valid := connectPeer(t, f.db, h.url, original)
	// 首次匿名访问签发长期 HttpOnly Cookie，后续仅携带 Cookie 恢复同一身份。
	fresh, err := http.Get(h.url + "/public/website-channels/" + f.channelID + "/messenger")
	require.NoError(t, err)
	var initial appservice.WebsiteVisitorMessenger
	require.NoError(t, json.NewDecoder(fresh.Body).Decode(&initial))
	fresh.Body.Close()
	require.Len(t, fresh.Cookies(), 1)
	cookie := fresh.Cookies()[0]
	require.True(t, cookie.HttpOnly)
	require.Equal(t, "visitor_"+f.channelID, cookie.Name)
	require.Equal(t, 365*24*60*60, cookie.MaxAge)
	require.Equal(t, initial.VisitorToken, cookie.Value)
	_, err = h.backend.SendTextMessage(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, customeridentity.AnonymousExternalID(cookie.Value), appservice.WebsiteVisitorTextMessageInput{ClientMessageID: uuid.NewV7().String(), Body: "新匿名访客"})
	require.NoError(t, err)
	freshConfig := visitorConfig(t, h, f.channelID, cookie.Value, "")
	require.Equal(t, freshConfig.UserID, visitorConfig(t, h, f.channelID, cookie.Value, "").UserID)
	require.NotContains(t, freshConfig.Token, cookie.Value)
	connectPeer(t, f.db, h.url, freshConfig)
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	signed := signCustomer(t, secret, jwt.MapClaims{"sub": "identified-customer", "exp": time.Now().Add(time.Hour).Unix()})
	customer, err := h.backend.VerifyCustomer(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, signed)
	require.NoError(t, err)
	meta := appservice.WebsiteVisitorMeta{Customer: &customer, CustomerToken: signed}
	message, err := h.backend.SendTextMessage(ctx, meta, f.channelID, customeridentity.CustomerExternalID(customer.UserID), appservice.WebsiteVisitorTextMessageInput{ClientMessageID: uuid.NewV7().String(), Body: "已识别访客"})
	require.NoError(t, err)
	identified := visitorConfig(t, h, f.channelID, anonymous, signed)
	require.NotEqual(t, original.UserID, identified.UserID)
	require.NotContains(t, identified.Token, signed)
	signedClient := connectPeer(t, f.db, h.url, identified)
	raw := rawPeer(t, h.url, identified)
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.Error(t, valid.echo.Private(identified.Channel).Ready(ready))
	require.Error(t, signedClient.echo.Private(original.Channel).Ready(ready))
	require.Error(t, signedClient.echo.Private("w."+f.owner.Workspace.ID+".inbox").Ready(ready))
	_, err = h.backend.ListMessages(ctx, meta, f.channelID, customeridentity.CustomerExternalID(customer.UserID), f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.Error(t, err, "另一身份的业务快照被拒绝")
	history, err := h.backend.ListMessages(ctx, meta, f.channelID, customeridentity.CustomerExternalID(customer.UserID), message.Conversation.ID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	require.NotEmpty(t, history.Messages)
	// 另一应用节点复用同一正式服务与连接登记完成强制撤销。
	options := h.broker.Conn.Opts
	options.Name = "visitor-replica"
	nc, err := options.Connect()
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	replica, err := members.New(&broker.Connection{Conn: nc, Signer: h.broker.Signer, Admin: h.broker.Admin}, newAccountTestBackend(t, f.db), f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, replica.Start(ctx))
	t.Cleanup(func() { _ = replica.Stop() })
	h.publisher.SetMembers(replica)
	// 真实 Action 的提交通过另一应用节点强制撤销旧密钥连接。
	_, err = customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.owner)
	require.NoError(t, err)
	require.Eventually(t, raw.IsClosed, 5*time.Second, 10*time.Millisecond)
	signedClient.expectEnded()
	h.expectRejected(t, f.channelID, "", http.Header{appservice.CustomerTokenHeader: []string{signed}}, http.StatusUnauthorized)
	require.NoError(t, replica.Publish(ctx, realtime.Notification{WorkspaceID: f.owner.Workspace.ID, AudienceKind: realtime.AudienceVisitorDirectory, AudienceID: strings.TrimPrefix(original.UserID, "v_"), Kind: realtime.KindVisitorTyping, ConversationID: f.conversationID, Active: true}))
	valid.expect(protocol.VisitorTyping{ConversationID: f.conversationID, Active: true})
	for _, path := range []string{"/public/website-channels/" + f.channelID + "/realtime", "/realtime", "/realtime/computer", "/realtime/workspaces"} {
		response, err := http.Get(h.url + path)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusNotFound, response.StatusCode)
	}
}

// TestJetcastVisitorExpiryRefresh 验证短期凭据过期拒绝和正式 SDK 到期前重新取凭据。
func TestJetcastVisitorExpiryRefresh(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	h := startVisitorRealtime(t, f)
	config := visitorConfig(t, h, f.channelID, "0123456789abcdef0123456789abcdef", "")
	identity, err := realtimeauth.Visitor(t.Context(), f.db, strings.TrimPrefix(config.UserID, "v_"))
	require.NoError(t, err)
	sign := func(exp time.Time) string {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, realtimeauth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: identity.ID, Audience: []string{"realtime-v"}, ExpiresAt: jwt.NewNumericDate(exp)}}).SignedString([]byte("realtime-v:" + identity.Secret))
		require.NoError(t, err)
		return "rt_v." + token
	}
	expired := config
	expired.Token = sign(time.Now().Add(-time.Second))
	nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Token(expired.Token), nats.Name("ZYXWVUTSRQPONMLKJIHGFE"), nats.NoReconnect())
	if nc != nil {
		nc.Close()
	}
	require.Error(t, err)
	var requests atomic.Int32
	short := sign(time.Now().Add(35 * time.Second))
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	echo, err := client.Connect(ctx, client.Options{Servers: []string{"ws" + strings.TrimPrefix(h.url, "http") + "/nats"}, Prefix: config.Prefix, GetToken: func(ctx context.Context) (string, error) {
		if requests.Add(1) == 1 {
			return short, nil
		}
		refreshed, err := h.backend.GetVisitorRealtimeConnection(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, identity.ExternalID)
		return refreshed.Token, err
	}})
	require.NoError(t, err)
	defer echo.Close()
	require.NoError(t, echo.Private(config.Channel).Ready(ctx))
	socket := echo.SocketID()
	require.Eventually(t, func() bool { return requests.Load() > 1 && echo.SocketID() != socket }, 12*time.Second, 25*time.Millisecond)
	require.Equal(t, client.StatusConnected, echo.Status())
}
