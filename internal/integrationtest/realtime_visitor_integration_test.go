//go:build server

package integrationtest

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
)

// visitorRealtimeHarness 是提供公开访客路由与访客事件流的测试服务。
type visitorRealtimeHarness struct {
	url       string
	namespace string
	subjects  chan string
}

// startVisitorRealtime 启动发布器、带访客后端的实时网关与公开 HTTP 路由，并记录测试工作区的全部通知 Subject。
func startVisitorRealtime(t *testing.T, f customerReadFixture, options gateway.Options) *visitorRealtimeHarness {
	t.Helper()
	config := servertest.NATSConfig(t, "test_visitor_"+strings.ReplaceAll(uuid.NewV7().String(), "-", ""))
	publisher := startTestPublisher(t, config)

	// 订阅测试工作区全部实时 Subject，用于核对访客通知不携带原始凭据。
	subjects := make(chan string, 64)
	watch, err := publisher.Connection().Subscribe(config.Namespace+".realtime."+f.owner.Organization.ID+".>", func(message *nats.Msg) {
		select {
		case subjects <- message.Subject:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watch.Unsubscribe() })

	scheduler := agentrunaction.NewScheduler(newTestTasks(f.db))
	visitorBackend := direct.NewWebsiteVisitorBackend(f.db, scheduler, newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil)
	memberBackend := direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	realtimeGateway := gateway.New(memberBackend, visitorBackend, config.Namespace, options)
	realtimeGateway.Start(publisher.Connection())
	t.Cleanup(realtimeGateway.Shutdown)

	httpAPI := api.NewService(appservice.New(memberBackend),
		api.WithWebsiteVisitor(appservice.NewWebsiteVisitorService(visitorBackend), false, ""),
		api.WithWebsiteVisitorRealtime(realtimeGateway),
	)
	server := httptest.NewUnstartedServer(httpAPI)
	// 服务器读写超时短于事件流存活时间，验证访客事件流不受其约束。
	server.Config.ReadTimeout = time.Second
	server.Config.WriteTimeout = time.Second
	server.Start()
	t.Cleanup(server.Close)
	return &visitorRealtimeHarness{url: server.URL, namespace: config.Namespace, subjects: subjects}
}

// request 按访客 Token 请求访客事件流，token 为空时不携带凭据，query 为附加查询串。
func (h *visitorRealtimeHarness) request(t *testing.T, channelID, token, query string, header http.Header) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.url+"/public/website-channels/"+channelID+"/realtime"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range header {
		request.Header[name] = values
	}
	if token != "" {
		request.AddCookie(&http.Cookie{Name: "visitor_" + channelID, Value: token})
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// connect 建立访客事件流并读取首个事件，要求其为访客 Hello。
func (h *visitorRealtimeHarness) connect(t *testing.T, channelID, token, query string) *realtimeTestClient {
	t.Helper()
	response := h.request(t, channelID, token, query, nil)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, content type = %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
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
				t.Errorf("解码访客事件 %s: %v", data, err)
				return
			}
			client.frames <- frame
		}
	}()
	if _, ok := client.next().(protocol.VisitorHello); !ok {
		t.Fatal("首个事件不是 visitor_hello")
	}
	return client
}

// expectRejected 验证访客事件流请求按公开错误体被拒绝。
func (h *visitorRealtimeHarness) expectRejected(t *testing.T, channelID, token string, header http.Header, status int) {
	t.Helper()
	response := h.request(t, channelID, token, "", header)
	var payload struct {
		Error appservice.Error `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || payload.Error.Message == "" {
		t.Fatalf("status = %d want %d, error = %+v", response.StatusCode, status, payload.Error)
	}
}

// TestVisitorRealtimeStream 验证访客事件流按渠道身份收敛：只收到本人线程的公开变更通知，跨身份隔离，缺少身份与成员令牌被拒，渠道停用结束事件流并拒绝重连。
func TestVisitorRealtimeStream(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	options := testGatewayOptions()
	options.PingInterval = 300 * time.Millisecond
	h := startVisitorRealtime(t, f, options)
	const visitorToken = "0123456789abcdef0123456789abcdef"
	const otherToken = "fedcba9876543210fedcba9876543210"

	// 尚未建立业务身份的访客不建立事件流，成员登录令牌同样不能用于访客事件流。
	h.expectRejected(t, f.channelID, otherToken, nil, http.StatusNotFound)
	memberToken := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	h.expectRejected(t, f.channelID, "", http.Header{"Authorization": []string{"Bearer " + memberToken}}, http.StatusBadRequest)

	client := h.connect(t, f.channelID, visitorToken, "")

	// 另一个访客身份先发消息建立身份，其事件流不接收本访客线程的通知。
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db), nil)
	if _, err := receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + otherToken, ClientMessageID: uuid.NewV7().String(), Body: "另一位访客的问题",
	}); err != nil {
		t.Fatal(err)
	}
	// 请求中指定他人线程编号不扩大接收范围，事件流仍只按本身份的渠道身份受众收敛。
	other := h.connect(t, f.channelID, otherToken, "?conversationId="+f.conversationID)

	// 访客事件流持续超过服务器 1 秒读写超时后仍存活并收到心跳。
	deadline := time.After(1500 * time.Millisecond)
	pings := 0
	for waiting := true; waiting; {
		select {
		case frame, ok := <-client.frames:
			if !ok {
				t.Fatal("访客事件流在服务器读写超时后结束")
			}
			if _, ping := frame.(protocol.Ping); ping {
				pings++
			}
		case <-deadline:
			waiting = false
		}
	}
	if pings < 3 {
		t.Fatalf("pings = %d", pings)
	}

	// 客服回复推进本访客线程版本，只有该访客的事件流收到公开变更通知。
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复访客",
	}); err != nil {
		t.Fatal(err)
	}
	frame, ok := client.next().(protocol.ConversationChanged)
	if !ok || frame.ConversationID != f.conversationID {
		t.Fatalf("访客事件 = %#v, want conversation_changed %s", frame, f.conversationID)
	}
	other.expectQuiet()

	// 通知 Subject 使用渠道身份记录 ID，不出现访客 Token 原值。
	var identityID string
	if err := f.db.NewSelect().Table("channel_conversations").Column("contact_channel_identity_id").
		Where("conversation_id = ?", f.conversationID).Scan(ctx, &identityID); err != nil {
		t.Fatal(err)
	}
	seen := false
	for len(h.subjects) > 0 {
		subject := <-h.subjects
		if strings.Contains(subject, visitorToken) || strings.Contains(subject, otherToken) {
			t.Fatalf("Subject 泄露访客 Token: %s", subject)
		}
		if subject == realtime.Subject(h.namespace, f.owner.Organization.ID, realtime.AudienceVisitorDirectory, identityID) {
			seen = true
		}
	}
	if !seen {
		t.Fatal("未发布访客目录受众通知")
	}

	// 停用渠道结束该渠道全部访客事件流，重新请求被拒绝；请求中的渠道 ID 大小写不同也发往同一个规范受众。
	if _, err := newTestChannelStatusAction(f.db).Execute(ctx, f.owner, strings.ToUpper(f.channelID), false); err != nil {
		t.Fatal(err)
	}
	client.expectEnded()
	other.expectEnded()
	h.expectRejected(t, f.channelID, visitorToken, nil, http.StatusNotFound)
}
