//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// visitorRealtimeHarness 是提供公开访客路由与访客事件流的测试服务。
type visitorRealtimeHarness struct {
	publisher *realtime.Publisher
	db        *bun.DB
	url       string
	backend   *direct.WebsiteVisitorBackend
	members   *members.Service
	broker    *broker.Connection
}

// startVisitorRealtime 启动发布器、正式 Jetcast、公开 HTTP 路由并配置连接管理依赖。
func startVisitorRealtime(t *testing.T, f customerReadFixture, configure ...func(*broker.Connection)) *visitorRealtimeHarness {
	t.Helper()
	// 通知发布器使用本测试独立的内部总线命名空间。
	channel := servertest.BusChannel()
	publisher := startTestPublisherOn(t, f.db, channel)

	scheduler := agentrunaction.NewScheduler(testEnqueuer)
	visitorBackend := direct.NewWebsiteVisitorBackend(f.db, scheduler, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	memberBackend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	connection := servertest.StartRealtimeBroker(t)
	for _, configureBroker := range configure {
		configureBroker(connection)
	}
	service, err := members.New(connection, memberBackend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, service.Start(t.Context()))
	t.Cleanup(func() { _ = service.Stop() })
	publisher.SetMembers(service)
	proxy, err := connection.Proxy()
	require.NoError(t, err)

	httpAPI := api.NewService(memberBackend,
		api.WithWebsiteVisitor(visitorBackend, func() bool { return false }),
	)
	mux := http.NewServeMux()
	mux.Handle("/nats", proxy)
	mux.Handle("/", httpAPI)
	server := httptest.NewUnstartedServer(mux)
	// HTTP 读写超时独立于已升级的 NATS WebSocket 生命周期。
	server.Config.ReadTimeout = time.Second
	server.Config.WriteTimeout = time.Second
	server.Start()
	t.Cleanup(server.Close)
	return &visitorRealtimeHarness{publisher: publisher, db: f.db, url: server.URL, backend: visitorBackend, members: service, broker: connection}
}

// request 以渠道 Cookie 或客户签名换取实时凭据，query 为附加查询串。
func (h *visitorRealtimeHarness) request(t *testing.T, channelID, token, query string, header http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.url+"/public/website-channels/"+channelID+"/realtime-connection"+query, nil)
	require.NoError(t, err)
	for name, values := range header {
		request.Header[name] = values
	}
	if token != "" {
		request.AddCookie(&http.Cookie{Name: "visitor_" + channelID, Value: token})
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// connect 换取短期凭据并等待全部精确频道订阅就绪。
func (h *visitorRealtimeHarness) connect(t *testing.T, channelID, token, query string) *realtimeTestClient {
	t.Helper()
	response := h.request(t, channelID, token, query, nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var config appservice.RealtimePeerConnection
	require.NoError(t, json.NewDecoder(response.Body).Decode(&config))
	return connectPeer(t, h.db, h.url, config)

}

// expectRejected 验证访客实时凭据请求按公开错误体被拒绝。
func (h *visitorRealtimeHarness) expectRejected(t *testing.T, channelID, token string, header http.Header, status int) {
	t.Helper()
	response := h.request(t, channelID, token, "", header)
	var payload struct {
		Error appservice.Error `json:"error"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&payload))
	require.Equal(t, status, response.StatusCode)
	require.NotEmpty(t, payload.Error.Message)
}

// TestVisitorRealtimeStream 验证访客事件流按渠道身份收敛：只收到本人线程的公开变更通知，跨身份隔离，缺少身份与成员令牌被拒，渠道停用结束事件流并拒绝重连。
func TestVisitorRealtimeStream(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	h := startVisitorRealtime(t, f)
	const visitorToken = "0123456789abcdef0123456789abcdef"
	const otherToken = "fedcba9876543210fedcba9876543210"

	// 尚未建立业务身份的访客不建立事件流，成员登录令牌同样不能用于访客事件流。
	h.expectRejected(t, f.channelID, otherToken, nil, http.StatusNotFound)
	memberToken := loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail)
	h.expectRejected(t, f.channelID, "", http.Header{"Authorization": []string{"Bearer " + memberToken}}, http.StatusBadRequest)

	client := h.connect(t, f.channelID, visitorToken, "")

	// 另一个访客身份先发消息建立身份，其事件流不接收本访客线程的通知。
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer, servertest.DisabledMail{})
	_, err := receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + otherToken, ClientMessageID: uuid.NewV7().String(), Body: "另一位访客的问题",
	})
	require.NoError(t, err)
	// 请求中指定他人线程编号不扩大接收范围，事件流仍只按本身份的渠道身份受众收敛。
	other := h.connect(t, f.channelID, otherToken, "?conversationId="+f.conversationID)

	// 客服回复推进本访客线程版本，只有该访客的事件流收到公开变更通知。
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复访客",
	})
	require.NoError(t, err)
	frame, ok := client.next().(protocol.ConversationChanged)
	require.True(t, ok, "访客事件 = %#v, want conversation_changed", frame)
	require.Equal(t, f.conversationID, frame.ConversationID)
	other.expectQuiet()

	// 停用渠道结束该渠道全部访客事件流，重新请求被拒绝；请求中的渠道 ID 大小写不同也发往同一个规范受众。
	_, err = newTestChannelStatusAction(f.db).Execute(ctx, f.owner, strings.ToUpper(f.channelID), false)
	require.NoError(t, err)
	client.expectEnded()
	other.expectEnded()
	h.expectRejected(t, f.channelID, visitorToken, nil, http.StatusNotFound)
}
