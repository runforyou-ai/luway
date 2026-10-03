package control

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
)

// sentryEvent 是测试关心的错误事件字段。
type sentryEvent struct {
	Message     string            `json:"message"`
	Release     string            `json:"release"`
	Environment string            `json:"environment"`
	ServerName  string            `json:"server_name"`
	Tags        map[string]string `json:"tags"`
	Fingerprint []string          `json:"fingerprint"`
	Contexts    map[string]any    `json:"contexts"`
	Exception   []struct {
		Type       string `json:"type"`
		Value      string `json:"value"`
		Stacktrace *struct {
			Frames []struct {
				Function string `json:"function"`
			} `json:"frames"`
		} `json:"stacktrace"`
	} `json:"exception"`
}

// fakeSentry 记录 control 错误事件接口收到的签名请求与事件。
type fakeSentry struct {
	mu     sync.Mutex
	paths  []string
	keyIDs []string
	bodies []string
	events []sentryEvent
}

// ServeHTTP 解析 Sentry 信封中的 event 条目。
func (f *fakeSentry) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, request.URL.Path)
	f.keyIDs = append(f.keyIDs, request.Header.Get("Signature-Input"))
	f.bodies = append(f.bodies, string(body))
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	scanner.Scan()
	for scanner.Scan() {
		var item struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &item)
		if !scanner.Scan() {
			break
		}
		if item.Type == "event" {
			var event sentryEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err == nil {
				f.events = append(f.events, event)
			}
		}
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write([]byte(`{"id":"0"}`))
}

// snapshot 返回已收到的事件、请求路径与签名输入。
func (f *fakeSentry) snapshot() ([]sentryEvent, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentryEvent(nil), f.events...), append([]string(nil), f.paths...), append([]string(nil), f.keyIDs...)
}

// body 返回已收到的全部请求体。
func (f *fakeSentry) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.bodies, "\n")
}

// newTestReporter 创建指向模拟 control 的错误上报器与日志器，返回日志输出缓冲。
func newTestReporter(t *testing.T, enabled *atomic.Bool) (*fakeSentry, *ErrorReporter, *slog.Logger, *bytes.Buffer) {
	t.Helper()
	fake := &fakeSentry{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := New(server.URL, "1.2.3", func(context.Context) (Identity, error) {
		return Identity{ServerID: "server-1", PrivateKey: privateKey}, nil
	})
	reporter, err := client.NewErrorReporter(enabled.Load)
	if err != nil {
		t.Fatal(err)
	}
	output := &bytes.Buffer{}
	logger := slog.New(reporter.Handler(slog.NewTextHandler(output, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return fake, reporter, logger, output
}

// databaseError 模拟带 SQLSTATE 的数据库错误，错误信息含不应上报的内容。
type databaseError struct{}

// Error 返回带客户内容的错误信息。
func (databaseError) Error() string {
	return `ERROR: duplicate key value "客户机密" (SQLSTATE=23505)`
}

// Field 返回错误字段，C 为 SQLSTATE。
func (databaseError) Field(key byte) string {
	if key == 'C' {
		return "23505"
	}
	return ""
}

// TestErrorReporterSendsErrorLogs 校验 Error 级别日志以签名请求上报，只带异常类型、数据库错误码、白名单标签、归组和版本信息，错误信息原文只写本地日志。
func TestErrorReporterSendsErrorLogs(t *testing.T) {
	enabled := &atomic.Bool{}
	enabled.Store(true)
	fake, reporter, logger, output := newTestReporter(t, enabled)

	cause := fmt.Errorf("save title %q: %w", "客户机密", databaseError{})
	logger.With("operation", "GetInboxConversation").Error("业务调用失败", "error", cause, "workspace_id", "w-1")
	logger.Warn("可恢复的问题", "error", errors.New("ignored"))
	reporter.Flush(5 * time.Second)

	events, paths, signatures := fake.snapshot()
	if len(events) != 1 || len(paths) != 1 {
		t.Fatalf("events = %#v, paths = %v", events, paths)
	}
	if paths[0] != "/sentry/api/1/envelope/" || !strings.Contains(signatures[0], `keyid="server-1"`) {
		t.Fatalf("path = %q, signature = %q", paths[0], signatures[0])
	}
	if body := fake.body(); strings.Contains(body, "客户机密") || strings.Contains(body, "w-1") {
		t.Fatalf("body = %s", body)
	}
	event := events[0]
	if event.Message != "业务调用失败" || event.Release != "1.2.3" || event.Environment != Environment || event.ServerName != "" {
		t.Fatalf("event = %#v", event)
	}
	if len(event.Tags) != 2 || event.Tags["operation"] != "GetInboxConversation" || event.Tags["sqlstate"] != "23505" {
		t.Fatalf("tags = %#v", event.Tags)
	}
	if strings.Join(event.Fingerprint, ",") != "{{ default }},GetInboxConversation,23505" {
		t.Fatalf("fingerprint = %#v", event.Fingerprint)
	}
	// 异常链只有类型；错误没有自带调用栈时不附带写日志处的调用栈。
	if len(event.Exception) != 2 || event.Exception[0].Type != "control.databaseError" || event.Exception[1].Type != "*fmt.wrapError" ||
		event.Exception[0].Value != "" || event.Exception[1].Value != "" || event.Exception[1].Stacktrace != nil {
		t.Fatalf("exception = %#v", event.Exception)
	}
	if _, ok := event.Contexts["runtime"]; !ok {
		t.Fatalf("contexts = %#v", event.Contexts)
	}
	// 本地日志按原处理器输出完整错误信息，并带上事件编号。
	if !strings.Contains(output.String(), "客户机密") || !strings.Contains(output.String(), "event_id=") || !strings.Contains(output.String(), "可恢复的问题") {
		t.Fatalf("output = %s", output.String())
	}
}

// TestErrorReporterPanicStack 校验 panic 错误以 panic 发生处的调用栈上报、不带 panic 值，并按任务名归组。
func TestErrorReporterPanicStack(t *testing.T) {
	enabled := &atomic.Bool{}
	enabled.Store(true)
	fake, reporter, logger, _ := newTestReporter(t, enabled)

	err := func() (err error) {
		defer func() {
			err = common.NewPanicError(recover())
		}()
		panicInTask()
		return nil
	}()
	logger.Error("异步任务执行失败", "queue", "default", "action", "agent.run", "error", err)
	reporter.Flush(5 * time.Second)
	// panic 值可能含业务内容，只上报类型与调用栈。
	if body := fake.body(); strings.Contains(body, "boom") {
		t.Fatalf("body = %s", body)
	}

	events, _, _ := fake.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	if event.Tags["queue"] != "default" || event.Tags["action"] != "agent.run" || strings.Join(event.Fingerprint, ",") != "{{ default }},agent.run" {
		t.Fatalf("event = %#v", event)
	}
	frames := event.Exception[len(event.Exception)-1].Stacktrace.Frames
	found := false
	for _, frame := range frames {
		found = found || frame.Function == "panicInTask"
	}
	if !found {
		t.Fatalf("frames = %#v", frames)
	}
}

// panicInTask 模拟任务执行中发生 panic。
func panicInTask() {
	panic("boom")
}

// TestErrorReporterDisabled 校验上报开关关闭时不发送事件。
func TestErrorReporterDisabled(t *testing.T) {
	enabled := &atomic.Bool{}
	fake, reporter, logger, output := newTestReporter(t, enabled)

	logger.Error("业务调用失败", "error", errors.New("boom"))
	reporter.Flush(time.Second)

	if events, paths, _ := fake.snapshot(); len(events) != 0 || len(paths) != 0 {
		t.Fatalf("events = %#v, paths = %v", events, paths)
	}
	if !strings.Contains(output.String(), "业务调用失败") {
		t.Fatalf("output = %s", output.String())
	}
}
