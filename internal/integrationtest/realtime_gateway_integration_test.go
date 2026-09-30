//go:build server

package integrationtest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	accountaction "github.com/runforyou-ai/cervi/internal/actions/account"
	authaction "github.com/runforyou-ai/cervi/internal/actions/auth"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/cervi/internal/realtime"
	"github.com/runforyou-ai/cervi/internal/realtime/gateway"
	"github.com/runforyou-ai/cervi/internal/realtime/protocol"
	"github.com/runforyou-ai/cervi/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// realtimeGatewayHarness 是连到独立 NATS 命名空间实时网关的测试服务。
type realtimeGatewayHarness struct {
	gateway     *gateway.Gateway
	backend     *direct.Backend
	namespace   string
	url         string
	runURL      string
	nats        *nats.Conn
	workspaceID string
}

// startRealtimeGateway 启动发布器、实时网关与带读写超时的 HTTP 服务，事件流请求以测试工作区为目标，wrap 可替换网关使用的成员后端。
func startRealtimeGateway(t *testing.T, f navigationFixture, options gateway.Options, wrap func(gateway.MemberBackend) gateway.MemberBackend) *realtimeGatewayHarness {
	t.Helper()
	config := servertest.NATSConfig(t, "test_gateway_"+strings.ReplaceAll(uuid.NewV7().String(), "-", ""))
	publisher := startTestPublisher(t, config)

	backend := direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	var member gateway.MemberBackend = backend
	if wrap != nil {
		member = wrap(backend)
	}
	realtimeGateway := gateway.New(member, nil, config.Namespace, options)
	realtimeGateway.Start(publisher.Connection())
	handler := realtimeGateway.Middleware(http.NotFoundHandler())
	server := httptest.NewUnstartedServer(handler)
	// 服务器读写超时短于事件流存活时间，验证事件流不受其约束。
	server.Config.ReadTimeout = time.Second
	server.Config.WriteTimeout = time.Second
	server.Start()
	t.Cleanup(server.Close)
	t.Cleanup(realtimeGateway.Shutdown)
	return &realtimeGatewayHarness{
		gateway: realtimeGateway, backend: backend, namespace: config.Namespace,
		url: server.URL + gateway.Path, runURL: server.URL + gateway.RunPath, nats: publisher.Connection(),
		workspaceID: f.owner.Organization.ID,
	}
}

// testGatewayOptions 返回默认网关参数并缩短下线等待时间。
func testGatewayOptions() gateway.Options {
	options := gateway.DefaultOptions()
	options.ShutdownTimeout = 2 * time.Second
	return options
}

// realtimeTestClient 是测试用的实时事件流客户端，后台协程把收到的事件放入通道，事件流结束时关闭通道。
type realtimeTestClient struct {
	t      *testing.T
	frames chan protocol.Frame
}

// request 携带令牌请求事件流并返回响应。
func (h *realtimeGatewayHarness) request(t *testing.T, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(appservice.WorkspaceHeader, h.workspaceID)
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// requestRun 携带令牌请求指定运行的过程流并返回响应。
func (h *realtimeGatewayHarness) requestRun(t *testing.T, token, runID string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.runURL+runID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(appservice.WorkspaceHeader, h.workspaceID)
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// openRun 建立运行过程流并开始读取事件。
func (h *realtimeGatewayHarness) openRun(t *testing.T, token, runID string) *realtimeTestClient {
	t.Helper()
	response := h.requestRun(t, token, runID)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, content type = %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	return h.readFrames(t, response)
}

// connect 建立事件流并读取首个事件，要求其为服务端 Hello。
func (h *realtimeGatewayHarness) connect(t *testing.T, token string) (*realtimeTestClient, protocol.ServerHello) {
	t.Helper()
	client := h.open(t, token)
	hello, ok := client.next().(protocol.ServerHello)
	if !ok {
		t.Fatal("首个事件不是 server_hello")
	}
	return client, hello
}

// open 建立事件流并开始读取事件。
func (h *realtimeGatewayHarness) open(t *testing.T, token string) *realtimeTestClient {
	t.Helper()
	response := h.request(t, token)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, content type = %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	return h.readFrames(t, response)
}

// readFrames 启动后台协程逐条解码事件流中的事件，事件流结束时关闭通道。
func (h *realtimeGatewayHarness) readFrames(t *testing.T, response *http.Response) *realtimeTestClient {
	t.Helper()
	client := &realtimeTestClient{t: t, frames: make(chan protocol.Frame, 16)}
	go func() {
		defer close(client.frames)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			frame, err := protocol.Decode([]byte(data))
			if err != nil {
				t.Errorf("解码实时事件 %s: %v", data, err)
				return
			}
			client.frames <- frame
		}
	}()
	return client
}

// expectRejected 验证令牌请求事件流时返回登录会话错误。
func (h *realtimeGatewayHarness) expectRejected(t *testing.T, token string) {
	t.Helper()
	h.expectRejectedWith(t, token, http.StatusUnauthorized, appservice.SessionStateLogin)
}

// expectRejectedWith 验证令牌请求事件流时返回指定状态码与会话入口。
func (h *realtimeGatewayHarness) expectRejectedWith(t *testing.T, token string, status int, state appservice.SessionState) {
	t.Helper()
	response := h.request(t, token)
	var payload struct {
		Error appservice.Error `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || payload.Error.State != state {
		t.Fatalf("status = %d, error = %+v", response.StatusCode, payload.Error)
	}
}

// next 在时限内读取下一条事件，跳过心跳。
func (c *realtimeTestClient) next() protocol.Frame {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatal("事件流已结束")
			}
			if _, ping := frame.(protocol.Ping); !ping {
				return frame
			}
		case <-timeout:
			c.t.Fatal("等待实时事件超时")
		}
	}
}

// expect 读取下一条事件并与期望比较。
func (c *realtimeTestClient) expect(want protocol.Frame) {
	c.t.Helper()
	if got := c.next(); !reflect.DeepEqual(got, want) {
		c.t.Fatalf("frame = %#v, want %#v", got, want)
	}
}

// expectQuiet 在短暂等待内确认事件流除心跳外没有事件。
func (c *realtimeTestClient) expectQuiet() {
	c.t.Helper()
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatal("事件流已结束")
			}
			if _, ping := frame.(protocol.Ping); !ping {
				c.t.Fatalf("收到不应送达的事件 %#v", frame)
			}
		case <-timeout:
			return
		}
	}
}

// expectEnded 在时限内读到事件流结束，结束前只允许出现心跳。
func (c *realtimeTestClient) expectEnded() {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				return
			}
			if _, ping := frame.(protocol.Ping); !ping {
				c.t.Fatalf("事件流结束前收到 %#v", frame)
			}
		case <-timeout:
			c.t.Fatal("等待事件流结束超时")
		}
	}
}

// loginToken 登录测试账号并返回新签发的令牌。
func loginToken(t *testing.T, db *bun.DB, organizationID, email string) string {
	t.Helper()
	login := loginMember(t, db, organizationID, email, "password123")
	return login.Token
}

// testAccountSession 解析登录令牌对应的账号会话。
func testAccountSession(t *testing.T, db *bun.DB, token string) *servermodels.AccountIdentity {
	t.Helper()
	account, err := authaction.NewResolveAccountQuery(db).Execute(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

// commitBeforeHeads 在读取同步探针前执行一次写入，模拟订阅安装与探针读取之间提交的变化。
type commitBeforeHeads struct {
	gateway.MemberBackend
	commit func()
}

// MemberSyncHeads 先执行写入再读取同步探针。
func (b commitBeforeHeads) MemberSyncHeads(ctx context.Context, session direct.MemberSession) (appservice.SyncHeads, error) {
	b.commit()
	return b.MemberBackend.MemberSyncHeads(ctx, session)
}

// logoutAfterAuthentication 在首次认证成功后登出，模拟认证与订阅生效之间提交的登出。
type logoutAfterAuthentication struct {
	gateway.MemberBackend
	logout func()
	calls  *atomic.Int32
}

// AuthenticateMember 首次认证后执行登出，之后照常认证。
func (b logoutAfterAuthentication) AuthenticateMember(ctx context.Context, meta appservice.RequestMeta) (direct.MemberSession, error) {
	session, err := b.MemberBackend.AuthenticateMember(ctx, meta)
	if err == nil && b.calls.Add(1) == 1 {
		b.logout()
	}
	return session, err
}

// TestRealtimeGatewayDelivery 验证同一用户多条事件流都收到用户与客服共享受众通知，登出只结束对应登录会话，停用成员结束全部事件流且无法再进入该工作区。
func TestRealtimeGatewayDelivery(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	organizationID := f.owner.Organization.ID
	h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
	tokenA := loginToken(t, f.db, organizationID, f.memberEmail)
	tokenB := loginToken(t, f.db, organizationID, f.memberEmail)
	clientA, helloA := h.connect(t, tokenA)
	clientB, _ := h.connect(t, tokenB)
	if heads, err := h.backend.MemberSyncHeads(ctx, direct.NewMemberSession(f.member)); err != nil || !reflect.DeepEqual(helloA.SyncHeads, heads) {
		t.Fatalf("hello heads = %+v, want %+v (%v)", helloA.SyncHeads, heads, err)
	}
	h.expectRejected(t, "")

	// 群消息通知同一用户的两条事件流。
	f.send(t, f.owner, "实时网关", false)
	changed := protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline}
	clientA.expect(changed)
	clientB.expect(changed)

	// 客户会话变化经客服共享受众送达全部成员事件流。
	customerConversationID := uuid.NewV7().String()
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.ServiceInboxConversationChanged(organizationID, customerConversationID, domain.ConversationTypeChannel, 3, domain.ConversationChangeTimeline))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	customerChanged := protocol.ConversationChanged{ConversationID: customerConversationID, ConversationType: domain.ConversationTypeChannel, Version: 3, Changes: domain.ConversationChangeTimeline}
	clientA.expect(customerChanged)
	clientB.expect(customerChanged)

	// 撤销事务回滚时不发布，事件流继续收到后续通知。
	identityA, err := h.backend.AuthenticateMember(ctx, appservice.RequestMeta{Token: tokenA, WorkspaceID: organizationID})
	if err != nil {
		t.Fatal(err)
	}
	errRollback := errors.New("rollback")
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.UserSessionLoggedOut(organizationID, f.member.User.ID, identityA.SessionID))
		return errRollback
	}); !errors.Is(err, errRollback) {
		t.Fatalf("rollback err = %v", err)
	}
	f.send(t, f.owner, "回滚之后", false)
	changed = protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline}
	clientA.expect(changed)
	clientB.expect(changed)

	// 登出只结束该登录会话的事件流，另一登录会话继续收到通知，被登出的令牌无法重连。
	if err := authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, tokenA)); err != nil {
		t.Fatal(err)
	}
	clientA.expectEnded()
	f.send(t, f.owner, "登出之后", false)
	clientB.expect(protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline})
	h.expectRejected(t, tokenA)

	// 停用账号结束该用户全部事件流；同一事务的资料通知可能先于撤销送达。
	if _, err := testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for ended := false; !ended; {
		select {
		case frame, ok := <-clientB.frames:
			switch frame.(type) {
			case nil:
				ended = !ok
			case protocol.IdentityProfileChanged, protocol.Ping:
			default:
				t.Fatalf("停用后收到 %#v", frame)
			}
		case <-timeout:
			t.Fatal("停用后事件流未结束")
		}
	}
	// 账号会话仍然有效，停用的成员身份不能再进入该工作区。
	h.expectRejectedWith(t, tokenB, http.StatusForbidden, appservice.SessionStateWorkspace)
}

// TestRealtimeGatewayPasswordChangeRevokesOtherSessions 验证修改密码结束同一账号其他登录会话的事件流，当前会话继续收到通知。
func TestRealtimeGatewayPasswordChangeRevokesOtherSessions(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
	current := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	other := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	currentClient, _ := h.connect(t, current)
	otherClient, _ := h.connect(t, other)

	if err := accountaction.NewChangePasswordAction(f.db).Execute(ctx, testAccountSession(t, f.db, current), accountaction.ChangePasswordInput{
		CurrentPassword: "password123", NewPassword: "password456",
	}); err != nil {
		t.Fatal(err)
	}
	otherClient.expectEnded()
	f.send(t, f.owner, "改密之后", false)
	currentClient.expect(protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline})
	h.expectRejected(t, other)
}

// TestRealtimeGatewayHelloAfterSubscription 验证订阅安装与探针读取之间提交的消息同时体现在 Hello 探针与后续通知中。
func TestRealtimeGatewayHelloAfterSubscription(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	token := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	h := startRealtimeGateway(t, f, testGatewayOptions(), func(backend gateway.MemberBackend) gateway.MemberBackend {
		return commitBeforeHeads{MemberBackend: backend, commit: func() {
			_, err := newGroupSendAction(f.db).Execute(context.Background(), f.owner, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "探针之前"})
			if err != nil {
				t.Error(err)
			}
		}}
	})
	client := h.open(t, token)

	// 通知与 Hello 的先后顺序不固定，两者都必须送达。
	var hello *protocol.ServerHello
	var changed *protocol.ConversationChanged
	for hello == nil || changed == nil {
		switch frame := client.next().(type) {
		case protocol.ServerHello:
			hello = &frame
		case protocol.ConversationChanged:
			changed = &frame
		default:
			t.Fatalf("unexpected frame %#v", frame)
		}
	}
	if want := (protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline}); *changed != want {
		t.Fatalf("changed = %#v, want %#v", *changed, want)
	}
	if heads, err := h.backend.MemberSyncHeads(context.Background(), direct.NewMemberSession(f.member)); err != nil || !reflect.DeepEqual(hello.SyncHeads, heads) {
		t.Fatalf("hello heads = %+v, want %+v (%v)", hello.SyncHeads, heads, err)
	}
}

// TestRealtimeGatewayConnectionLimits 验证认证与订阅之间的登出、心跳与服务器读写超时、最长存活时间与服务端下线。
func TestRealtimeGatewayConnectionLimits(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	organizationID := f.owner.Organization.ID
	token := loginToken(t, f.db, organizationID, f.memberEmail)

	t.Run("认证后订阅前登出", func(t *testing.T) {
		loggedOut := loginToken(t, f.db, organizationID, f.memberEmail)
		loggedOutAccount := testAccountSession(t, f.db, loggedOut)
		h := startRealtimeGateway(t, f, testGatewayOptions(), func(backend gateway.MemberBackend) gateway.MemberBackend {
			return logoutAfterAuthentication{MemberBackend: backend, calls: &atomic.Int32{}, logout: func() {
				if err := authaction.NewLogoutAction(f.db).Execute(context.Background(), loggedOutAccount); err != nil {
					t.Error(err)
				}
			}}
		})
		h.expectRejected(t, loggedOut)
	})
	t.Run("心跳与服务器读写超时", func(t *testing.T) {
		options := testGatewayOptions()
		options.PingInterval = 300 * time.Millisecond
		h := startRealtimeGateway(t, f, options, nil)
		client, _ := h.connect(t, token)
		// 持续超过服务器读写超时后仍收到心跳与通知。
		deadline := time.After(2500 * time.Millisecond)
		pings := 0
		for waiting := true; waiting; {
			select {
			case frame, ok := <-client.frames:
				if !ok {
					t.Fatal("事件流在服务器读写超时后结束")
				}
				if _, ping := frame.(protocol.Ping); ping {
					pings++
				}
			case <-deadline:
				waiting = false
			}
		}
		if pings < 5 {
			t.Fatalf("pings = %d", pings)
		}
		f.send(t, f.owner, "超时之后", false)
		client.expect(protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: loadConversationVersion(t, f.db, f.groupID), Changes: domain.ConversationChangeTimeline})
	})
	t.Run("最长存活时间", func(t *testing.T) {
		options := testGatewayOptions()
		options.MaxLifetime = time.Second
		h := startRealtimeGateway(t, f, options, nil)
		client, _ := h.connect(t, token)
		client.expectEnded()
		h.connect(t, token)
	})
	t.Run("服务端下线", func(t *testing.T) {
		h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
		client, _ := h.connect(t, token)
		stopped := make(chan struct{})
		go func() {
			h.gateway.Shutdown()
			close(stopped)
		}()
		client.expectEnded()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("事件流结束后网关未及时停止")
		}
		response := h.request(t, token)
		var payload struct {
			Error appservice.Error `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || response.StatusCode != http.StatusServiceUnavailable || payload.Error.Kind != appservice.ErrorKindUnavailable {
			t.Fatalf("下线后请求 status = %d, error = %+v (%v)", response.StatusCode, payload.Error, err)
		}
	})
}

// TestRealtimeGatewaySlowConsumer 验证停止读取的事件流在有界时间内结束，其他事件流照常收到通知。
func TestRealtimeGatewaySlowConsumer(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	organizationID := f.owner.Organization.ID
	options := testGatewayOptions()
	options.QueueSize = 8
	options.WriteTimeout = 500 * time.Millisecond
	h := startRealtimeGateway(t, f, options, nil)
	slow := h.request(t, loginToken(t, f.db, organizationID, f.memberEmail))
	fast, _ := h.connect(t, loginToken(t, f.db, organizationID, f.ownerEmail))

	// 向慢事件流所属用户受众连续发布互不合并的通知，慢事件流期间不读取。
	subject := realtime.Subject(h.namespace, organizationID, realtime.AudienceUser, f.member.User.ID)
	for range 200000 {
		data, err := json.Marshal(realtime.Payload{Kind: realtime.KindConversationChanged, ConversationID: uuid.NewV7().String(), Version: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.nats.Publish(subject, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.nats.Flush(); err != nil {
		t.Fatal(err)
	}

	// 慢事件流恢复读取后在时限内读到结束。
	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, slow.Body)
		ended <- err
	}()
	select {
	case <-ended:
	case <-time.After(15 * time.Second):
		t.Fatal("慢事件流未在时限内结束")
	}

	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.UserConversationChanged(organizationID, f.owner.User.ID, f.groupID, domain.ConversationTypeGroup, 99, domain.ConversationChangeTimeline))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fast.expect(protocol.ConversationChanged{ConversationID: f.groupID, ConversationType: domain.ConversationTypeGroup, Version: 99, Changes: domain.ConversationChangeTimeline})
}

// runStreamBackend 用可控的运行流替换本进程订阅，验证网关的授权、快照分片、增量写出与失权结束。
type runStreamBackend struct {
	gateway.MemberBackend
	mu         sync.Mutex
	snapshot   runstream.Snapshot
	running    bool
	onDelta    func(runstream.Delta)
	onEnd      func()
	subscribed chan struct{}
}

// SubscribeAgentRunStream 返回预置快照并登记回调，未标记运行中时返回 false。
func (b *runStreamBackend) SubscribeAgentRunStream(_ string, onDelta func(runstream.Delta), onEnd func()) (runstream.Snapshot, func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return runstream.Snapshot{}, nil, false
	}
	b.onDelta, b.onEnd = onDelta, onEnd
	close(b.subscribed)
	return b.snapshot, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.onDelta, b.onEnd = nil, nil
	}, true
}

// publish 向已登记的订阅方推送一条增量。
func (b *runStreamBackend) publish(t *testing.T, delta runstream.Delta) {
	t.Helper()
	select {
	case <-b.subscribed:
	case <-time.After(5 * time.Second):
		t.Fatal("等待运行过程流订阅超时")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.onDelta == nil {
		t.Fatal("运行过程流没有订阅方")
	}
	b.onDelta(delta)
}

// insertAgentRun 写入一条属于指定会话的运行记录，供运行过程流授权使用。
func insertAgentRun(t *testing.T, db *bun.DB, organizationID, conversationID string) string {
	t.Helper()
	run := servermodels.AgentRun{
		ID: uuid.NewV7().String(), OrganizationID: organizationID, ConversationID: conversationID,
		AgentIdentityID: uuid.NewV7().String(), AgentRevisionID: uuid.NewV7().String(), LaneID: uuid.NewV7().String(),
		ScopeKind: string(domain.AgentExecutionScopeConversation), ScopeID: conversationID,
		Status: string(domain.AgentRunStatusRunning), Usage: []byte("{}"),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(&run).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return run.ID
}

// TestRealtimeGatewayRunStream 验证运行过程流按会话阅读资格授权、按分片写出快照与增量，并在所属会话失权时结束。
func TestRealtimeGatewayRunStream(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	organizationID := f.owner.Organization.ID
	runID := insertAgentRun(t, f.db, organizationID, f.groupID)
	stub := &runStreamBackend{subscribed: make(chan struct{})}
	h := startRealtimeGateway(t, f, testGatewayOptions(), func(backend gateway.MemberBackend) gateway.MemberBackend {
		stub.MemberBackend = backend
		return stub
	})
	token := loginToken(t, f.db, organizationID, f.memberEmail)

	// 运行不存在时不区分无权与不存在，返回与业务接口一致的错误体。
	response := h.requestRun(t, token, uuid.NewV7().String())
	var payload struct {
		Error appservice.Error `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound || payload.Error.Reason != "agent_run_process_unavailable" {
		t.Fatalf("status = %d, error = %+v", response.StatusCode, payload.Error)
	}

	// 运行不在本进程执行时直接结束该流，客户端按持久事实收敛。
	idle := h.openRun(t, token, runID)
	idle.expect(protocol.RunStreamEnded{RunID: runID})
	idle.expectEnded()

	// 运行在本进程执行时先按分片写出快照，再写出增量。
	stub.mu.Lock()
	stub.running = true
	stub.snapshot = runstream.Snapshot{
		RunID: runID, StreamID: "stream-1", Attempt: 1, Sequence: 2, CandidateContent: "候选",
		Blocks: []runstream.Block{{ID: "block-1", Position: 1, Kind: domain.AgentRunBlockThinking, Text: "先确认"}},
	}
	stub.mu.Unlock()
	client := h.openRun(t, token, runID)
	client.expect(protocol.RunStreamSnapshot{
		RunID: runID, StreamID: "stream-1", Attempt: 1, Sequence: 2, Part: 0, PartCount: 1, CandidateContent: "候选",
		Blocks: []protocol.RunStreamBlock{{ID: "block-1", Position: 1, Kind: domain.AgentRunBlockThinking, Text: "先确认"}},
	})
	stub.publish(t, runstream.Delta{
		RunID: runID, StreamID: "stream-1", Attempt: 1, BaseSequence: 2, Sequence: 3,
		Operations: []runstream.Operation{{Kind: runstream.OperationAppendBlockText, BlockID: "block-1", Text: "退款政策"}},
	})
	client.expect(protocol.RunStreamDelta{
		RunID: runID, StreamID: "stream-1", Attempt: 1, BaseSequence: 2, Sequence: 3,
		Operations: []protocol.RunStreamOperation{{Kind: protocol.RunStreamAppendBlockText, BlockID: "block-1", Text: "退款政策"}},
	})

	// 失去运行所属会话的阅读资格后，服务端结束该流，不再推送后续内容。
	if err := realtime.RunInTx(context.Background(), f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.UserConversationRemoved(organizationID, f.member.User.ID, f.groupID))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client.expectEnded()
}
