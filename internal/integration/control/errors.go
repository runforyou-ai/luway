package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"time"

	"github.com/getsentry/sentry-go"
)

// errorTagKeys 是作为错误事件标签上报的日志属性，其余日志属性不上报。
var errorTagKeys = []string{"operation", "action", "queue"}

// errorGroupKeys 是参与错误归组的事件标签：同一入口方法或任务中类型链与数据库错误码相同的错误归为一组。
var errorGroupKeys = []string{"operation", "action", "sqlstate"}

// ErrorReporter 把 Error 级别日志作为错误事件经 Sentry 协议上报给 control，上报开关关闭时丢弃。
type ErrorReporter struct {
	client  *sentry.Client
	enabled func() bool
}

// NewErrorReporter 创建错误上报器，enabled 返回当前是否上报。
func (c *Client) NewErrorReporter(enabled func() bool) (*ErrorReporter, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse control url: %w", err)
	}
	// DSN 中的密钥与项目编号不起作用，control 按请求签名归属事件。
	endpoint.User = url.User("public")
	endpoint.Path += "/sentry/1"
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:            endpoint.String(),
		HTTPClient:     c.http,
		Release:        c.version,
		Environment:    Environment,
		MaxBreadcrumbs: -1,
		// 经典传输在 Flush 时等待已排队的事件发送完成。
		DisableTelemetryBuffer: true,
		Integrations:           func([]sentry.Integration) []sentry.Integration { return nil },
		BeforeSend:             redactEvent,
	})
	if err != nil {
		return nil, fmt.Errorf("create error reporter: %w", err)
	}
	return &ErrorReporter{client: client, enabled: enabled}, nil
}

// Handler 返回 slog 处理器：日志交给 next 输出，Error 级别的日志同时上报，本地日志附带事件编号。
func (r *ErrorReporter) Handler(next slog.Handler) slog.Handler {
	return &errorHandler{next: next, reporter: r}
}

// Flush 等待已上报的事件发送完成，最长等待 timeout。
func (r *ErrorReporter) Flush(timeout time.Duration) {
	r.client.Flush(timeout)
}

// capture 把一条 Error 级别日志转为错误事件发送并返回事件编号：values 是日志的顶层属性，白名单属性转为标签并参与归组。
// error 属性只上报异常链的类型、数据库错误码和错误自身携带的调用栈；错误信息原文可能含业务内容、上游响应或连接参数，只写本地日志。
func (r *ErrorReporter) capture(record slog.Record, values map[string]slog.Value) *sentry.EventID {
	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Timestamp = record.Time
	event.Message = record.Message
	event.Contexts["runtime"] = sentry.Context{"name": "go", "version": runtime.Version()}
	event.Contexts["os"] = sentry.Context{"name": runtime.GOOS}
	if value, ok := values["error"]; ok {
		if err, ok := value.Any().(error); ok {
			event.SetException(err, r.client.Options().MaxErrorDepth)
			for index := range event.Exception {
				event.Exception[index].Value = ""
			}
			// 最外层错误没有自带调用栈时，SDK 补上的是写日志处的调用栈，不能定位出错位置。
			if sentry.ExtractStacktrace(err) == nil {
				event.Exception[len(event.Exception)-1].Stacktrace = nil
			}
			var database interface{ Field(byte) string }
			if errors.As(err, &database) && database.Field('C') != "" {
				event.Tags["sqlstate"] = database.Field('C')
			}
		}
	}
	for _, key := range errorTagKeys {
		if value, ok := values[key]; ok {
			event.Tags[key] = value.String()
		}
	}
	event.Fingerprint = []string{"{{ default }}"}
	for _, key := range errorGroupKeys {
		if value, ok := event.Tags[key]; ok {
			event.Fingerprint = append(event.Fingerprint, value)
		}
	}
	return r.client.CaptureEvent(event, nil, nil)
}

// redactEvent 只保留异常、消息、标签、归组、版本与运行环境，清除 SDK 自动补充的主机名、用户、请求、依赖模块、线程和附件。
func redactEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	event.ServerName = ""
	event.User = sentry.User{}
	event.Request = nil
	event.Breadcrumbs = nil
	event.Modules = nil
	event.Threads = nil
	event.DebugMeta = nil
	event.Attachments = nil
	return event
}

// errorHandler 把日志交给下一个处理器输出，并上报 Error 级别的日志。
type errorHandler struct {
	next     slog.Handler
	reporter *ErrorReporter
	attrs    []slog.Attr
	grouped  bool
}

// Enabled 对 Error 级别始终启用，其余级别按下一个处理器判断。
func (h *errorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelError || h.next.Enabled(ctx, level)
}

// Handle 上报 Error 级别的日志，再交给下一个处理器输出。
func (h *errorHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelError && h.reporter.enabled() {
		values := make(map[string]slog.Value, len(h.attrs)+record.NumAttrs())
		for _, attr := range h.attrs {
			values[attr.Key] = attr.Value.Resolve()
		}
		// 分组内的属性不属于顶层属性，不参与提取异常与标签。
		if !h.grouped {
			record.Attrs(func(attr slog.Attr) bool {
				values[attr.Key] = attr.Value.Resolve()
				return true
			})
		}
		// 本地日志记录事件编号，用于对照 control 中的错误事件与本地的完整错误信息。
		if id := h.reporter.capture(record, values); id != nil {
			record = record.Clone()
			record.AddAttrs(slog.String("event_id", string(*id)))
		}
	}
	if !h.next.Enabled(ctx, record.Level) {
		return nil
	}
	return h.next.Handle(ctx, record)
}

// WithAttrs 返回附加属性后的处理器。
func (h *errorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.next = h.next.WithAttrs(attrs)
	if !h.grouped {
		clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	}
	return &clone
}

// WithGroup 返回进入分组后的处理器。
func (h *errorHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.next = h.next.WithGroup(name)
	clone.grouped = true
	return &clone
}
