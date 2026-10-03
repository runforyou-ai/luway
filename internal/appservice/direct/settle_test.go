//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// recordingHandler 记录指定业务入口方法的 Error 级别日志。
type recordingHandler struct {
	mu        sync.Mutex
	operation string
	records   []slog.Record
}

// Enabled 对全部级别启用。
func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle 记录指定业务入口方法的 Error 级别日志。
func (h *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	operation := ""
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "operation" {
			operation = attr.Value.String()
		}
		return true
	})
	if record.Level >= slog.LevelError && operation == h.operation {
		h.mu.Lock()
		h.records = append(h.records, record)
		h.mu.Unlock()
	}
	return nil
}

// WithAttrs 返回处理器本身。
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup 返回处理器本身。
func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// causes 返回已记录日志的 error 属性。
func (h *recordingHandler) causes() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	var causes []error
	for _, record := range h.records {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "error" {
				err, _ := attr.Value.Any().(error)
				causes = append(causes, err)
			}
			return true
		})
	}
	return causes
}

// settledCall 以 settle 收尾执行 call，返回收尾后的错误。
func settledCall(ctx context.Context, operation string, call func() error) (err error) {
	defer settle(ctx, operation, &err, internalError(appservice.RequestMeta{}))
	return call()
}

// TestSettle 校验业务入口错误收尾：失败与 panic 以 Error 级别记录原始错误，业务错误与已取消请求不记录，非业务错误转为内部错误。
func TestSettle(t *testing.T) {
	handler := &recordingHandler{operation: "SettleTestOperation"}
	previous := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx := context.Background()
	meta := appservice.RequestMeta{}
	cause := errors.New("connection reset")

	// 携带原始错误的操作失败原样返回并记录原始错误。
	failed := appservice.FailedError(meta, i18n.ErrorInboxLoadFailed, cause)
	if err := settledCall(ctx, handler.operation, func() error { return failed }); err != failed {
		t.Fatalf("failed = %v", err)
	}
	// 业务错误不记录。
	if err := settledCall(ctx, handler.operation, func() error { return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound) }); appservice.SessionStateOf(err) != "" || err == nil {
		t.Fatalf("not found = %v", err)
	}
	// 非业务错误转为内部错误并记录。
	raw := errors.New("unexpected")
	err := settledCall(ctx, handler.operation, func() error { return raw })
	if failure, ok := errors.AsType[*appservice.Error](err); !ok || failure.Kind != appservice.ErrorKindFailed || !errors.Is(err, raw) {
		t.Fatalf("raw = %#v", err)
	}
	// 已取消的请求不记录。
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := settledCall(canceled, handler.operation, func() error { return appservice.FailedError(meta, i18n.ErrorInboxLoadFailed, canceled.Err()) }); err == nil {
		t.Fatal("canceled returned nil")
	}
	// panic 转为带调用栈的内部错误并记录。
	err = settledCall(ctx, handler.operation, func() error { panic("boom") })
	panicError, ok := errors.AsType[*common.PanicError](err)
	if !ok || panicError.Value != "boom" || len(panicError.StackTrace()) == 0 || !strings.Contains(panicError.Error(), "panic: boom") {
		t.Fatalf("panic = %#v", err)
	}

	causes := handler.causes()
	if len(causes) != 3 || causes[0] != cause || causes[1] != raw || causes[2] != panicError {
		t.Fatalf("causes = %#v", causes)
	}
}
