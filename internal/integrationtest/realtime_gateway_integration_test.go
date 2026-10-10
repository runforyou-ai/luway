//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/jetcast/client"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// realtimeGatewayHarness 是提供正式 Jetcast 与业务 HTTP 入口的测试服务。
type realtimeGatewayHarness struct {
	members     *members.Service
	broker      *broker.Connection
	backend     *direct.Backend
	db          *bun.DB
	channel     string
	url         string
	publisher   *realtime.Publisher
	workspaceID string
}

// startRealtimeGateway 启动发布器、正式 Jetcast 和业务 HTTP 入口，configure 配置连接管理依赖。
func startRealtimeGateway(t *testing.T, f navigationFixture, configure ...func(*broker.Connection)) *realtimeGatewayHarness {
	t.Helper()
	channel := servertest.BusChannel()
	realtimePublisherLock.Lock()
	t.Cleanup(realtimePublisherLock.Unlock)
	publisher := realtime.NewPublisher(servertest.StartBus(t, f.db, channel, uuid.NewV7().String()))

	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db), RunSnapshots: publisher}, nil, nil, agentrunaction.NewRunCancellation(f.db, testEnqueuer), testEnqueuer, nil, nil, nil)
	connection := servertest.StartRealtimeBroker(t)
	for _, configureBroker := range configure {
		configureBroker(connection)
	}
	memberService, err := members.New(connection, backend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, memberService.Start(context.Background()))
	t.Cleanup(func() { _ = memberService.Stop() })
	publisher.SetMembers(memberService)
	publisher.SetRunTransport(connection.Conn, "app_realtime", memberService.PublishRun)
	require.NoError(t, publisher.Start())
	t.Cleanup(func() { _ = publisher.Stop() })
	proxy, err := connection.Proxy()
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/nats", proxy)
	mux.Handle("/api/", http.StripPrefix("/api", api.NewService(backend, api.WithComputers(backend))))
	server := httptest.NewUnstartedServer(mux)
	// HTTP 读写超时独立于已升级的 NATS WebSocket 生命周期。
	server.Config.ReadTimeout = time.Second
	server.Config.WriteTimeout = time.Second
	server.Start()
	t.Cleanup(server.Close)
	return &realtimeGatewayHarness{
		backend: backend, db: f.db, channel: channel,
		url: server.URL, members: memberService, broker: connection, publisher: publisher,
		workspaceID: f.owner.Workspace.ID,
	}
}

// realtimeTestClient 用正式 SDK 订阅事件并记录收到的业务帧。
type realtimeTestClient struct {
	echo   *client.Client
	t      *testing.T
	frames chan protocol.Frame
}

// next 在时限内读取下一条业务事件。
func (c *realtimeTestClient) next() protocol.Frame {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatal("事件流已结束")
			}
			return frame
		case <-timeout:
			c.t.Fatal("等待实时事件超时")
		}
	}
}

// expect 读取下一条事件并与期望比较。
func (c *realtimeTestClient) expect(want protocol.Frame) {
	c.t.Helper()
	require.Equal(c.t, want, c.next())
}

// expectQuiet 在短暂等待内确认没有业务事件。
func (c *realtimeTestClient) expectQuiet() {
	c.t.Helper()
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatal("事件流已结束")
			}
			c.t.Fatalf("收到不应送达的事件 %#v", frame)
		case <-timeout:
			return
		}
	}
}

// expectEnded 在时限内确认 SDK 因失权停止。
func (c *realtimeTestClient) expectEnded() {
	c.t.Helper()
	require.Eventually(c.t, func() bool { return c.echo.Status() == client.StatusStopped }, 5*time.Second, 10*time.Millisecond)
}

// loginToken 登录测试账号并返回新签发的令牌。
func loginToken(t *testing.T, db *bun.DB, workspaceID, email string) string {
	t.Helper()
	login := servertest.LoginMember(t, db, workspaceID, email, "password123")
	return login.Token
}

// testAccountSession 解析登录令牌对应的账号会话。
func testAccountSession(t *testing.T, db *bun.DB, token string) *servermodels.AccountIdentity {
	t.Helper()
	account, err := authaction.NewResolveAccountQuery(db).Execute(context.Background(), token)
	require.NoError(t, err)
	return account
}

// TestRealtimeGatewayDelivery 验证同一用户多条事件流都收到用户与客服共享受众通知，登出只结束对应登录会话，停用成员结束全部事件流且无法再进入该工作区。
func TestRealtimeGatewayDelivery(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	workspaceID := f.owner.Workspace.ID
	h := startRealtimeGateway(t, f)
	tokenA := loginToken(t, f.db, workspaceID, f.memberEmail)
	tokenB := loginToken(t, f.db, workspaceID, f.memberEmail)
	clientA, helloA := h.connect(t, tokenA)
	clientB, _ := h.connect(t, tokenB)
	heads, err := h.backend.GetSyncHeads(ctx, appservice.RequestMeta{Token: tokenA, WorkspaceID: workspaceID})
	require.NoError(t, err)
	require.Equal(t, heads, helloA.SyncHeads)
	h.expectRejected(t, "")

	// 群消息通知同一用户的两条事件流。
	f.send(t, f.owner, "实时网关", false)
	changed := protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline}
	clientA.expect(changed)
	clientB.expect(changed)

	// 客户会话变化经客服共享受众送达全部成员事件流。
	customerConversationID := uuid.NewV7().String()
	require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.ServiceInboxConversationChanged(workspaceID, customerConversationID, domain.ConversationTypeChannel, 3, domain.ConversationChangeTimeline))
		return nil
	}))
	customerChanged := protocol.ConversationChanged{ConversationID: customerConversationID, ConversationType: domain.ConversationTypeChannel, Version: 3, Changes: domain.ConversationChangeTimeline}
	clientA.expect(customerChanged)
	clientB.expect(customerChanged)

	// 撤销事务回滚时不发布，事件流继续收到后续通知。
	identityA, err := h.backend.AuthenticateAccountMembers(ctx, appservice.RequestMeta{Token: tokenA, WorkspaceID: workspaceID})
	require.NoError(t, err)
	errRollback := errors.New("rollback")
	require.ErrorIs(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.UserSessionLoggedOut(workspaceID, f.member.User.ID, identityA.SessionID))
		return errRollback
	}), errRollback)
	f.send(t, f.owner, "回滚之后", false)
	changed = protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline}
	clientA.expect(changed)
	clientB.expect(changed)

	// 登出只结束该登录会话的事件流，另一登录会话继续收到通知，被登出的令牌无法重连。
	require.NoError(t, authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, tokenA)))
	clientA.expectEnded()
	f.send(t, f.owner, "登出之后", false)
	clientB.expect(protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline})
	h.expectRejected(t, tokenA)

	// 停用账号结束该用户全部事件流；同一事务的资料通知可能先于撤销送达。
	oldSocket := clientB.echo.SocketID()
	_, err = testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return clientB.echo.Status() == client.StatusStopped || clientB.echo.SocketID() != oldSocket
	}, 8*time.Second, 20*time.Millisecond)
	// 账号会话仍然有效，停用的成员身份不能再进入该工作区。
	h.expectRejectedWith(t, tokenB, http.StatusForbidden, appservice.SessionStateWorkspace)
}

// insertAgentRun 写入一条属于指定会话的运行记录，供运行过程流授权使用。
func insertAgentRun(t *testing.T, db *bun.DB, workspaceID, conversationID string) string {
	t.Helper()
	run := servermodels.AgentRun{
		ID: uuid.NewV7().String(), WorkspaceID: workspaceID, ConversationID: conversationID,
		AgentIdentityID: uuid.NewV7().String(), AgentRevisionID: uuid.NewV7().String(), LaneID: uuid.NewV7().String(),
		ScopeKind: string(domain.AgentExecutionScopeConversation), ScopeID: conversationID,
		Status: string(domain.AgentRunStatusRunning), Usage: []byte("{}"),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	_, err := db.NewInsert().Model(&run).Exec(context.Background())
	require.NoError(t, err)
	return run.ID
}

// executeRunOn 登记运行由指定实例的指定尝试执行：以新心跳登记该服务端实例，并把执行中的任务尝试写入运行。
func executeRunOn(t *testing.T, db *bun.DB, workspaceID, runID, instanceID string, attempt int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, serverinstanceaction.ReportInstance(ctx, db, serverinstanceaction.InstanceReport{
		ID: instanceID, Hostname: "run-stream", Version: "test", BusDriver: clusterbus.DriverPostgres, BusConnected: true,
	}))
	t.Cleanup(func() {
		_ = serverinstanceaction.RemoveInstance(context.Background(), db, instanceID)
	})
	_, err := db.NewUpdate().Table("agent_runs").
		Set("status = ?", domain.AgentRunStatusRunning).
		Set("task_run_id = ?", uuid.NewV7().String()).
		Set("task_attempt = ?", attempt).
		Set("task_instance_id = ?", instanceID).
		Where("workspace_id = ? AND id = ?", workspaceID, runID).Exec(ctx)
	require.NoError(t, err)
}

// TestRealtimeGatewayNotificationsAcrossServers 验证另一台服务器提交的业务通知经共享 jetcast 送达成员连接。
func TestRealtimeGatewayNotificationsAcrossServers(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	client, _ := h.connect(t, loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail))

	// 另一实例通过独立 APP 连接和 jetcast 服务发布，客户端仍连接原实例的代理。
	options := h.broker.Conn.Opts
	options.Name = "member-replica"
	connection, err := options.Connect()
	require.NoError(t, err)
	t.Cleanup(connection.Close)
	replica, err := members.New(&broker.Connection{Conn: connection, Signer: h.broker.Signer, Admin: h.broker.Admin}, h.backend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, replica.Start(t.Context()))
	t.Cleanup(func() { _ = replica.Stop() })
	startPublisher(t, servertest.StartBus(t, f.db, h.channel, uuid.NewV7().String()), f.db).SetMembers(replica)
	f.send(t, f.owner, "跨服务器通知", false)
	client.expect(protocol.ConversationChanged{
		ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup,
		Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline,
	})
}
