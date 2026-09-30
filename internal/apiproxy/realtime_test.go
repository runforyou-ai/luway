//go:build !server

package apiproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/clientsession"
	"github.com/runforyou-ai/cervi/internal/realtime/protocol"
)

// testWindowKey 在测试中标记发起调用的前端窗口。
type testWindowKey struct{}

// emittedEvent 是原生端投递给前端的一条事件。
type emittedEvent struct {
	name string
	data any
}

// newRealtimeTestBackend 创建持有登录凭据、连到指定服务器地址并记录投递事件的原生端后端。
func newRealtimeTestBackend(t *testing.T, serverURL string) (*Backend, <-chan emittedEvent) {
	t.Helper()
	events := make(chan emittedEvent, 4)
	backend := newRealtimeTestBackendEmitting(t, serverURL, func(_, name string, data any) { events <- emittedEvent{name, data} })
	return backend, events
}

// newRealtimeTestBackendEmitting 创建持有登录凭据、连到指定服务器地址并按 emit 投递事件的原生端后端，发起窗口取自调用上下文。
func newRealtimeTestBackendEmitting(t *testing.T, serverURL string, emit func(owner, name string, data any)) *Backend {
	t.Helper()
	store := &memoryStore{serverURL: serverURL, credentialSet: true, credential: clientsession.Credential{
		ServerURL: serverURL, Token: "native-token", ExpiresAt: time.Now().Add(time.Hour),
	}}
	sessions, err := clientsession.NewManager(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewBackend(store, "", sessions, emit, func(ctx context.Context) string {
		owner, _ := ctx.Value(testWindowKey{}).(string)
		return owner
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

// TestRealtimeWindowTargetAndRelease 验证事件只投递给发起窗口，窗口关闭后结束其事件流、删除登记，建立期间关闭的窗口拒绝登记新事件流。
func TestRealtimeWindowTargetAndRelease(t *testing.T) {
	frame, err := protocol.Encode(protocol.ConversationChanged{ConversationID: "conversation-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write(append(append([]byte("data: "), frame...), '\n', '\n'))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	// targeted 是投递到某个窗口的一条事件。
	type targeted struct {
		owner string
		name  string
		data  any
	}
	events := make(chan targeted, 4)
	backend := newRealtimeTestBackendEmitting(t, server.URL, func(owner, name string, data any) { events <- targeted{owner, name, data} })
	t.Cleanup(backend.realtime.disconnectAll)
	meta := appservice.RequestMeta{Locale: "zh-CN"}
	owners := map[string]string{}
	for _, owner := range []string{"1", "2"} {
		connection, err := backend.ConnectRealtime(context.WithValue(context.Background(), testWindowKey{}, owner), meta)
		if err != nil {
			t.Fatal(err)
		}
		owners[connection.ConnectionID] = owner
	}
	// 每条事件流的事件只投递给发起它的窗口。
	for range 2 {
		select {
		case got := <-events:
			event, ok := got.data.(appservice.RealtimeFrameEvent)
			if got.name != appservice.RealtimeFrameEventName || !ok || owners[event.ConnectionID] != got.owner {
				t.Fatalf("event = %#v, owners = %v", got, owners)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("等待实时事件超时")
		}
	}

	// 关闭窗口 1 结束其事件流并删除登记，窗口 2 不受影响。
	backend.ReleaseWindow("1")
	select {
	case got := <-events:
		event, ok := got.data.(appservice.RealtimeClosedEvent)
		if got.name != appservice.RealtimeClosedEventName || !ok || got.owner != "1" || owners[event.ConnectionID] != "1" {
			t.Fatalf("closed event = %#v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待事件流结束超时")
	}
	backend.realtime.mu.Lock()
	_, released := backend.realtime.windows["1"]
	_, kept := backend.realtime.windows["2"]
	backend.realtime.mu.Unlock()
	if released || !kept {
		t.Fatalf("window 1 registered = %v, window 2 registered = %v", released, kept)
	}

	// 事件流建立期间窗口已关闭时不登记新事件流，也不重建该窗口的登记。
	generation := backend.realtime.generation("3")
	backend.ReleaseWindow("3")
	if _, ok := backend.realtime.start("3", io.NopCloser(strings.NewReader("")), func() {}, generation); ok {
		t.Fatal("closed window registered a new stream")
	}
	backend.realtime.mu.Lock()
	_, recreated := backend.realtime.windows["3"]
	backend.realtime.mu.Unlock()
	if recreated {
		t.Fatal("closed window registration recreated")
	}
}

// TestRealtimeConnection 验证原生端按服务器地址路径拼接事件流地址并携带凭据，投递服务端事件与事件流结束。
func TestRealtimeConnection(t *testing.T) {
	frame, err := protocol.Encode(protocol.ConversationChanged{ConversationID: "conversation-1", Version: 9007199254740993})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/app/api/realtime" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer native-token" || request.Header.Get("Accept-Language") != "zh-CN" {
			t.Errorf("headers = %v", request.Header)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write(append(append([]byte("data: "), frame...), '\n', '\n'))
		writer.(http.Flusher).Flush()
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(server.Close)

	backend, events := newRealtimeTestBackend(t, server.URL+"/app")
	connection, err := backend.ConnectRealtime(context.Background(), appservice.RequestMeta{Locale: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	expectEvent(t, events, emittedEvent{appservice.RealtimeFrameEventName, appservice.RealtimeFrameEvent{ConnectionID: connection.ConnectionID, Frame: string(frame)}})
	close(release)
	expectEvent(t, events, emittedEvent{appservice.RealtimeClosedEventName, appservice.RealtimeClosedEvent{ConnectionID: connection.ConnectionID}})
}

// TestRealtimeConnectionHeaderTimeout 验证服务端迟迟不返回响应头时建立事件流在时限内失败。
func TestRealtimeConnectionHeaderTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	previous := realtimeConnectTimeout
	realtimeConnectTimeout = 200 * time.Millisecond
	t.Cleanup(func() { realtimeConnectTimeout = previous })

	backend, _ := newRealtimeTestBackend(t, server.URL)
	started := time.Now()
	_, err := backend.ConnectRealtime(context.Background(), appservice.RequestMeta{Locale: "zh-CN"})
	var applicationError *appservice.Error
	if !errors.As(err, &applicationError) || applicationError.Kind != appservice.ErrorKindUnavailable || time.Since(started) > 5*time.Second {
		t.Fatalf("err = %v, elapsed = %v", err, time.Since(started))
	}
}

// TestRealtimeConnectionRejectedCredential 验证服务端拒绝登录凭据时返回登录会话错误并清除本地凭据。
func TestRealtimeConnectionRejectedCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"state":"login","message":"请重新登录"}}`))
	}))
	t.Cleanup(server.Close)

	backend, _ := newRealtimeTestBackend(t, server.URL)
	_, err := backend.ConnectRealtime(context.Background(), appservice.RequestMeta{Locale: "zh-CN"})
	if state := appservice.SessionStateOf(err); state != appservice.SessionStateLogin {
		t.Fatalf("session state = %q (%v), want login", state, err)
	}
	if _, ok := backend.sessions.Current(context.Background(), server.URL); ok {
		t.Fatal("登录凭据被拒绝后仍保留本地凭据")
	}
}

// TestRealtimeConnectionRequiresLogin 验证没有登录凭据时拒绝建立实时事件流。
func TestRealtimeConnectionRequiresLogin(t *testing.T) {
	backend, err := newTestBackend(&memoryStore{serverURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.ConnectRealtime(context.Background(), appservice.RequestMeta{Locale: "zh-CN"})
	if state := appservice.SessionStateOf(err); state != appservice.SessionStateLogin {
		t.Fatalf("session state = %q (%v), want login", state, err)
	}
}

// TestRealtimeIndependentConnections 验证多个前端窗口各自持有事件流，断开一条不影响其余事件流。
func TestRealtimeIndependentConnections(t *testing.T) {
	frame, err := protocol.Encode(protocol.ConversationChanged{ConversationID: "conversation-1", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write(append(append([]byte("data: "), frame...), '\n', '\n'))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	backend, events := newRealtimeTestBackend(t, server.URL)
	meta := appservice.RequestMeta{Locale: "zh-CN"}
	first, err := backend.ConnectRealtime(context.WithValue(context.Background(), testWindowKey{}, "1"), meta)
	if err != nil {
		t.Fatal(err)
	}
	second, err := backend.ConnectRealtime(context.WithValue(context.Background(), testWindowKey{}, "2"), meta)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(backend.realtime.disconnectAll)
	if first.ConnectionID == second.ConnectionID {
		t.Fatalf("两条事件流的连接编号相同：%q", first.ConnectionID)
	}
	// 每条事件流按自身连接编号投递事件。
	delivered := map[string]bool{}
	for range 2 {
		select {
		case got := <-events:
			event, ok := got.data.(appservice.RealtimeFrameEvent)
			if got.name != appservice.RealtimeFrameEventName || !ok || event.Frame != string(frame) {
				t.Fatalf("event = %#v", got)
			}
			delivered[event.ConnectionID] = true
		case <-time.After(5 * time.Second):
			t.Fatal("等待实时事件超时")
		}
	}
	if !delivered[first.ConnectionID] || !delivered[second.ConnectionID] {
		t.Fatalf("投递事件的连接编号 = %v", delivered)
	}

	if err := backend.DisconnectRealtime(context.Background(), meta, first.ConnectionID); err != nil {
		t.Fatal(err)
	}
	expectEvent(t, events, emittedEvent{appservice.RealtimeClosedEventName, appservice.RealtimeClosedEvent{ConnectionID: first.ConnectionID}})
	if current, _ := windowStreamsOf(backend, "1"); current != "" {
		t.Fatalf("断开的窗口仍登记成员事件流 %q", current)
	}
	if current, _ := windowStreamsOf(backend, "2"); current != second.ConnectionID {
		t.Fatalf("另一窗口的成员事件流 = %q，want %q", current, second.ConnectionID)
	}
}

// TestRealtimeWindowReconnectReplacesStream 验证同一窗口或无法识别的窗口重新连接时关闭该通道原有的事件流。
func TestRealtimeWindowReconnectReplacesStream(t *testing.T) {
	for _, test := range []struct {
		name  string
		owner string
	}{
		{name: "同一窗口", owner: "1"},
		{name: "无法识别窗口", owner: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.(http.Flusher).Flush()
				<-request.Context().Done()
			}))
			t.Cleanup(server.Close)

			backend, events := newRealtimeTestBackend(t, server.URL)
			meta := appservice.RequestMeta{Locale: "zh-CN"}
			window := context.Background()
			if test.owner != "" {
				window = context.WithValue(window, testWindowKey{}, test.owner)
			}
			first, err := backend.ConnectRealtime(window, meta)
			if err != nil {
				t.Fatal(err)
			}
			second, err := backend.ConnectRealtime(window, meta)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(backend.realtime.disconnectAll)
			expectEvent(t, events, emittedEvent{appservice.RealtimeClosedEventName, appservice.RealtimeClosedEvent{ConnectionID: first.ConnectionID}})
			if current, _ := windowStreamsOf(backend, test.owner); current != second.ConnectionID {
				t.Fatalf("窗口的成员事件流 = %q，want %q", current, second.ConnectionID)
			}
		})
	}
}

// windowStreamsOf 返回指定窗口当前登记的成员事件流编号与运行过程流数量。
func windowStreamsOf(backend *Backend, owner string) (string, int) {
	backend.realtime.mu.Lock()
	defer backend.realtime.mu.Unlock()
	streams, ok := backend.realtime.windows[owner]
	if !ok {
		return "", 0
	}
	current := ""
	if streams.current != nil {
		current = streams.current.id
	}
	return current, len(streams.runs)
}

// expectEvent 在时限内读取下一条事件并与期望比较。
func expectEvent(t *testing.T, events <-chan emittedEvent, want emittedEvent) {
	t.Helper()
	select {
	case got := <-events:
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("event = %#v, want %#v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("等待事件 %s 超时", want.name)
	}
}

// TestRealtimeDisconnectClosesRunStreams 验证成员事件流断开时一并关闭全部运行过程流。
func TestRealtimeDisconnectClosesRunStreams(t *testing.T) {
	frame, err := protocol.Encode(protocol.RunStreamEnded{RunID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		if strings.HasPrefix(request.URL.Path, "/api/realtime/runs/") {
			_, _ = writer.Write(append(append([]byte("data: "), frame...), '\n', '\n'))
		}
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	backend, events := newRealtimeTestBackend(t, server.URL)
	meta := appservice.RequestMeta{Locale: "zh-CN"}
	member, err := backend.ConnectRealtime(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	first, err := backend.ConnectAgentRunStream(context.Background(), meta, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := backend.ConnectAgentRunStream(context.Background(), meta, "run-2")
	if err != nil {
		t.Fatal(err)
	}
	// 运行过程事件携带运行编号，订阅方据此区分并发的运行过程流；两条流的首帧到达顺序不固定。
	want := map[string]appservice.RealtimeRunFrameEvent{
		"run-1": {ConnectionID: first.ConnectionID, RunID: "run-1", Frame: string(frame)},
		"run-2": {ConnectionID: second.ConnectionID, RunID: "run-2", Frame: string(frame)},
	}
	for range 2 {
		select {
		case event := <-events:
			data, ok := event.data.(appservice.RealtimeRunFrameEvent)
			if !ok || event.name != appservice.RealtimeRunFrameEventName || !reflect.DeepEqual(data, want[data.RunID]) {
				t.Fatalf("event = %#v", event)
			}
			delete(want, data.RunID)
		case <-time.After(5 * time.Second):
			t.Fatalf("等待运行过程事件超时，尚未收到 %v", want)
		}
	}

	if err := backend.DisconnectRealtime(context.Background(), meta, member.ConnectionID); err != nil {
		t.Fatal(err)
	}
	// 三条事件流都结束，顺序不固定。
	closed := map[string]bool{}
	for range 3 {
		select {
		case event := <-events:
			switch data := event.data.(type) {
			case appservice.RealtimeClosedEvent:
				closed[data.ConnectionID] = true
			case appservice.RealtimeRunClosedEvent:
				closed[data.ConnectionID] = true
			default:
				t.Fatalf("event = %#v", event)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("等待事件流结束超时，已结束 %v", closed)
		}
	}
	if !closed[member.ConnectionID] || !closed[first.ConnectionID] || !closed[second.ConnectionID] {
		t.Fatalf("closed = %v", closed)
	}
}

// TestWorkspaceActivityConnection 验证工作区动态事件流不携带目标工作区、按工作区动态事件名投递；成员事件流重建时继续保持，
// 同一窗口重新建立时替换原有事件流，登录会话变化时结束。
func TestWorkspaceActivityConnection(t *testing.T) {
	frame, err := protocol.Encode(protocol.WorkspaceActivity{WorkspaceID: "workspace-2", Kind: protocol.TypeConversationChanged, ConversationID: "conversation-1"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/realtime/workspaces":
			if request.Header.Get(appservice.WorkspaceHeader) != "" || request.Header.Get("Authorization") != "Bearer native-token" {
				t.Errorf("headers = %v", request.Header)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write(append(append([]byte("data: "), frame...), '\n', '\n'))
		case "/api/realtime":
			writer.Header().Set("Content-Type", "text/event-stream")
		default:
			http.NotFound(writer, request)
			return
		}
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	backend, events := newRealtimeTestBackend(t, server.URL)
	ctx := context.WithValue(context.Background(), testWindowKey{}, "window-1")
	activity, err := backend.ConnectWorkspaceActivity(ctx, appservice.RequestMeta{Locale: "zh-CN", WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatal(err)
	}
	expectEvent(t, events, emittedEvent{appservice.RealtimeWorkspacesFrameEventName, appservice.RealtimeFrameEvent{ConnectionID: activity.ConnectionID, Frame: string(frame)}})
	// 成员事件流重建（如移动端回到前台）不影响工作区动态事件流。
	member, err := backend.ConnectRealtime(ctx, appservice.RequestMeta{Locale: "zh-CN", WorkspaceID: "workspace-1"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("成员事件流重建后收到 %#v", event)
	case <-time.After(300 * time.Millisecond):
	}
	// 同一窗口重新建立工作区动态事件流时替换原有事件流。
	replacement, err := backend.ConnectWorkspaceActivity(ctx, appservice.RequestMeta{Locale: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	expectEvents(t, events,
		emittedEvent{appservice.RealtimeWorkspacesFrameEventName, appservice.RealtimeFrameEvent{ConnectionID: replacement.ConnectionID, Frame: string(frame)}},
		emittedEvent{appservice.RealtimeWorkspacesClosedEventName, appservice.RealtimeClosedEvent{ConnectionID: activity.ConnectionID}},
	)
	// 登录会话变化时与成员事件流一并结束。
	backend.realtime.disconnectAll()
	expectEvents(t, events,
		emittedEvent{appservice.RealtimeWorkspacesClosedEventName, appservice.RealtimeClosedEvent{ConnectionID: replacement.ConnectionID}},
		emittedEvent{appservice.RealtimeClosedEventName, appservice.RealtimeClosedEvent{ConnectionID: member.ConnectionID}},
	)
}

// expectEvents 读取指定数量的投递事件并与期望集合比较，不要求顺序。
func expectEvents(t *testing.T, events <-chan emittedEvent, want ...emittedEvent) {
	t.Helper()
	remaining := slices.Clone(want)
	for range want {
		select {
		case event := <-events:
			index := slices.IndexFunc(remaining, func(candidate emittedEvent) bool { return reflect.DeepEqual(candidate, event) })
			if index < 0 {
				t.Fatalf("event = %#v, want one of %#v", event, remaining)
			}
			remaining = slices.Delete(remaining, index, index+1)
		case <-time.After(5 * time.Second):
			t.Fatalf("waiting for events %#v", remaining)
		}
	}
}

// TestWorkspaceActivityConcurrentStart 验证同一窗口并发建立的工作区动态事件流只登记先完成的一条，后完成的按过期结果丢弃。
func TestWorkspaceActivityConcurrentStart(t *testing.T) {
	backend, _ := newRealtimeTestBackend(t, "https://app.example.com")
	generation := backend.realtime.activityGeneration("window-1")
	first, second := io.NopCloser(strings.NewReader("")), io.NopCloser(strings.NewReader(""))
	if _, ok := backend.realtime.startActivity("window-1", first, func() {}, generation); !ok {
		t.Fatal("先完成的事件流应登记")
	}
	if _, ok := backend.realtime.startActivity("window-1", second, func() {}, generation); ok {
		t.Fatal("同一代次后完成的事件流应丢弃")
	}
}
