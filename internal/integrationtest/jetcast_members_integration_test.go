//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/runforyou-ai/jetcast/client"
	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectAccount 通过部署入口建立账号连接并订阅全部有效成员频道。
func (h *realtimeGatewayHarness) connectAccount(t *testing.T, token string) *realtimeTestClient {
	t.Helper()
	c, _ := h.connectMemberChannels(t, token, false)
	return c
}

// TestJetcastMembershipChanges 验证新增、暂停与恢复工作区提交后强制关闭旧授权，账号保留其他成员范围。
func TestJetcastMembershipChanges(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	invitation := newInvitationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	connection, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name("ABCDEFGHIJKLMNOPQRSTUV"), nats.Token(token), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(connection.Close)
	_, invitationToken := invitation.invite(t, f.memberEmail, "受邀成员")
	second, err := h.backend.AcceptInvitation(t.Context(), appservice.RequestMeta{Token: token}, appservice.InvitationTokenInput{Token: invitationToken})
	require.NoError(t, err)
	require.Eventually(t, connection.IsClosed, 5*time.Second, 10*time.Millisecond)
	_, err = f.db.NewUpdate().Model((*servermodels.Account)(nil)).Set("is_platform_admin = true").Where("id = ?", f.owner.Account.ID).Exec(t.Context())
	require.NoError(t, err)
	operator := &servermodels.AccountIdentity{Account: f.owner.Account}
	peers := workspacePeerConnections(t, h, second.ID, token)
	for _, status := range []domain.WorkspaceLifecycleStatus{domain.WorkspaceLifecycleSuspended, domain.WorkspaceLifecycleActive} {
		c, echo := h.connectMemberChannels(t, token, false)
		previous := echo.SocketID()
		if status == domain.WorkspaceLifecycleSuspended {
			subscribeRun(t, h, echo, insertAgentRun(t, f.db, h.workspaceID, f.groupID))
		}
		_, err := platformaction.NewSetWorkspaceStatusAction(f.db).Execute(t.Context(), operator, second.ID, status)
		require.NoError(t, err)
		require.Eventually(t, func() bool { return echo.Status() == client.StatusStopped || echo.SocketID() != previous }, 8*time.Second, 20*time.Millisecond)
		_ = c.echo.Close()
		config, err := h.backend.GetRealtimeConnection(t.Context(), appservice.RequestMeta{Token: token})
		require.NoError(t, err)
		if status == domain.WorkspaceLifecycleSuspended {
			for _, peer := range peers {
				require.Eventually(t, peer.IsClosed, 5*time.Second, 10*time.Millisecond)
			}
			require.Len(t, config.Members, 1)
			require.Equal(t, h.workspaceID, config.Members[0].WorkspaceID)
		} else {
			require.Len(t, config.Members, 2)
		}
	}
}

// TestJetcastPasswordSessionRevocation 验证改密码只撤销其他登录会话，当前会话继续收到成员通知。
func TestJetcastPasswordSessionRevocation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	otherToken := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	current := h.connectAccount(t, token)
	other := h.connectAccount(t, otherToken)
	runID := insertAgentRun(t, f.db, h.workspaceID, f.groupID)
	subscribeRun(t, h, current.echo, runID)
	subscribeRun(t, h, other.echo, runID)
	require.NoError(t, accountaction.NewChangePasswordAction(f.db).Execute(t.Context(), testAccountSession(t, f.db, token), accountaction.ChangePasswordInput{CurrentPassword: "password123", NewPassword: "newpassword123"}))
	other.expectEnded()
	h.expectRejected(t, otherToken)
	notice := realtime.UserIdentityProfileChanged(h.workspaceID, f.member.User.ID, 3)
	require.NoError(t, h.members.Publish(t.Context(), notice))
	current.expect(members.NotificationFrame(notice))
	require.NoError(t, authaction.NewLogoutAction(f.db).Execute(t.Context(), testAccountSession(t, f.db, token)))
	current.expectEnded()
}

// connect 通过 jetcast 建立当前工作区成员订阅并在就绪后读取同步探针。
func (h *realtimeGatewayHarness) connect(t *testing.T, token string) (*realtimeTestClient, protocol.ServerHello) {
	t.Helper()
	c, echo := h.connectMemberChannels(t, token, true)
	heads, err := h.backend.GetSyncHeads(context.Background(), appservice.RequestMeta{Token: token, WorkspaceID: h.workspaceID})
	require.NoError(t, err)
	return c, protocol.ServerHello{ConnectionID: echo.SocketID(), SyncHeads: heads}
}

// connectMemberChannels 使用正式 Go SDK 连接真实账户，并把事件交给现有业务断言。
func (h *realtimeGatewayHarness) connectMemberChannels(t *testing.T, token string, currentOnly bool) (*realtimeTestClient, *client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	echo, err := client.Connect(ctx, client.Options{
		Servers: []string{"ws" + strings.TrimPrefix(h.url, "http") + "/nats"}, Prefix: "app_realtime",
		GetToken: func(ctx context.Context) (string, error) {
			if _, err := h.backend.GetRealtimeConnection(ctx, appservice.RequestMeta{Token: token}); err != nil {
				return "", client.ErrUnauthorized
			}
			return token, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() })
	c := &realtimeTestClient{t: t, frames: make(chan protocol.Frame, 256), echo: echo}
	var info appservice.RealtimeConnection
	require.NoError(t, json.Unmarshal(echo.Info(), &info))
	for _, member := range info.Members {
		if currentOnly && member.WorkspaceID != h.workspaceID {
			continue
		}
		for _, name := range []string{member.Channel, member.InboxChannel, member.TypingChannel, member.InboxTypingChannel} {
			sub := echo.Private(name)
			sub.ListenAll(func(event client.Event) {
				frame, err := protocol.Decode(event.Data)
				if !assert.NoError(t, err) {
					return
				}
				select {
				case c.frames <- frame:
				case <-ctx.Done():
				}
			})
			ready, stop := context.WithTimeout(ctx, 5*time.Second)
			require.NoError(t, sub.Ready(ready))
			stop()
		}
	}
	return c, echo
}

// TestJetcastRevocationEnforced 验证忽略控制消息的客户端被强制关闭，剩余工作区继续可用，输入状态不进入历史。
func TestJetcastRevocationEnforced(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	second := servertest.AddAccountWorkspace(t, f.db, token, "保留的工作区").Workspace
	// 成员变化的撤销标记覆盖在途认证，SDK 负责等待该窗口结束。
	c, echo := h.connectMemberChannels(t, token, false)
	subscribeRun(t, h, echo, insertAgentRun(t, f.db, h.workspaceID, f.groupID))
	closed := make(chan struct{})
	nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name("ZYXWVUTSRQPONMLKJIHGFE"), nats.Token(token), nats.NoReconnect(), nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	ctx := context.Background()
	stream, err := h.broker.JS.Stream(ctx, "app_realtime_events")
	require.NoError(t, err)
	notice := realtime.UserConversationTyping(h.workspaceID, f.member.User.ID, f.groupID, f.subjectID, true)
	require.NoError(t, h.members.Publish(ctx, notice))
	c.expect(members.NotificationFrame(notice))
	require.NoError(t, h.broker.Conn.Flush())
	_, err = stream.GetLastMsgForSubject(ctx, "app_realtime.ev.prv."+realtime.MemberChannels(h.workspaceID, f.member.User.ID).TypingChannel)
	require.ErrorIs(t, err, jetstream.ErrMsgNotFound, "输入状态不留存")
	_, err = testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("未强制关闭不合作的客户端")
	}
	_ = echo.Close()
	config, err := h.backend.GetRealtimeConnection(ctx, appservice.RequestMeta{Token: token})
	require.NoError(t, err)
	require.Len(t, config.Members, 1)
	require.Equal(t, second.ID, config.Members[0].WorkspaceID)
	remaining, connection := h.connectMemberChannels(t, token, false)
	ready, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.Error(t, connection.Private(realtime.MemberChannels(h.workspaceID, f.member.User.ID).Channel).Ready(ready))
	message := realtime.UserNotificationFor(second.ID, config.Members[0].UserID, uuid.NewV7().String(), f.groupID, domain.NotificationViewGroup, "仍然可见", "其他工作区")
	require.NoError(t, h.members.Publish(ctx, message))
	remaining.expect(members.NotificationFrame(message))
	require.NoError(t, authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, token)))
	remaining.expectEnded()
	h.expectRejected(t, token)
}

// expectRejected 校验非法令牌无法通过 NATS WebSocket 认证。
func (h *realtimeGatewayHarness) expectRejected(t *testing.T, token string) {
	t.Helper()
	nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name("abcdefghijklmnopqrstuv"), nats.Token(token), nats.NoReconnect(), nats.Timeout(5*time.Second))
	if nc != nil {
		nc.Close()
	}
	require.Error(t, err)
}

// expectRejectedWith 验证被撤销的当前工作区身份仍走原有业务恢复入口。
func (h *realtimeGatewayHarness) expectRejectedWith(t *testing.T, token string, status int, state appservice.SessionState) {
	t.Helper()
	_, err := h.backend.LoadIdentity(context.Background(), appservice.RequestMeta{Token: token, WorkspaceID: h.workspaceID})
	var failure *appservice.Error
	require.ErrorAs(t, err, &failure)
	require.Equal(t, status, failure.HTTPStatus())
	require.Equal(t, state, failure.State)
}

// TestJetcastMemberIsolation 验证精确成员授权、跨工作区用户通知与未授权频道拒绝。
func TestJetcastMemberIsolation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	second := servertest.AddAccountWorkspace(t, f.db, token, "成员实时第二工作区")
	c, echo := h.connectMemberChannels(t, token, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for _, channel := range []string{realtime.MemberChannels(h.workspaceID, f.owner.User.ID).Channel, "w." + uuid.NewV7().String() + ".inbox"} {
		require.Error(t, echo.Private(channel).Ready(ctx))
	}
	config, err := h.backend.GetRealtimeConnection(ctx, appservice.RequestMeta{Token: token})
	require.NoError(t, err)
	var memberID string
	for _, m := range config.Members {
		if m.WorkspaceID == second.Workspace.ID {
			memberID = m.UserID
		}
	}
	require.NotEmpty(t, memberID)
	require.NotEqual(t, f.member.Account.ID, memberID)
	notice := realtime.UserNotificationFor(second.Workspace.ID, memberID, uuid.NewV7().String(), uuid.NewV7().String(), domain.NotificationViewGroup, "跨工作区", "通知")
	require.NoError(t, h.members.Publish(ctx, notice))
	c.expect(members.NotificationFrame(notice))
	h.expectRejected(t, "invalid")
	for _, path := range []string{"/api/realtime", "/api/realtime/workspaces"} {
		response, err := http.Get(h.url + path)
		require.NoError(t, err)
		_ = response.Body.Close()
		require.Equal(t, http.StatusNotFound, response.StatusCode)
	}
}

// TestJetcastMemberCredentialLifetime 验证成员连接的凭据按二十分钟上限到期。
func TestJetcastMemberCredentialLifetime(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	socket := "credentialLifetime0001"
	nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name(socket), nats.Token(token), nats.NoReconnect(),
		nats.CustomInboxPrefix("app_realtime.c."+socket+".r"))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	connected := time.Now() //clock:local
	reply, err := nc.Request("app_realtime.rq."+socket+".hello", []byte("{}"), 5*time.Second)
	require.NoError(t, err)
	var hello struct {
		ExpiresAt int64           `json:"expiresAt"`
		Error     json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(reply.Data, &hello))
	require.Empty(t, hello.Error)
	expires := time.UnixMilli(hello.ExpiresAt)
	require.False(t, expires.Before(connected.Add(20*time.Minute-10*time.Second)), "凭据有效期取二十分钟上限")
	require.False(t, expires.After(connected.Add(20*time.Minute+5*time.Second)), "凭据有效期不超过二十分钟")
}
