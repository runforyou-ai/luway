// Package logscope 在 context 中携带日志作用域：串联编号、业务入口、后台任务与请求身份，日志处理器据此给每条日志附加字段。
package logscope

import (
	"context"
	"log/slog"
	"strings"

	"github.com/runforyou-ai/support/random"
)

// Scope 是一条日志所属的作用域，未知的字段为空。
type Scope struct {
	// TraceID 串联一次入站请求或定时触发及其后续异步任务的全部日志，是 W3C Trace Context 格式的 32 位小写十六进制编号。
	TraceID string
	// Operation 是业务入口方法名，不经业务入口的 HTTP 请求为路由。
	Operation string
	// TaskRunID 是正在执行的异步任务运行编号。
	TaskRunID string
	// Action 是正在执行的异步任务名称。
	Action string
	// Queue 是正在执行的异步任务所在队列。
	Queue string
	// WorkspaceID 是请求或任务所属的工作区编号。
	WorkspaceID string
	// AccountID 是发起请求的登录账号编号。
	AccountID string
}

// scopeKey 是 context 中日志作用域的键。
type scopeKey struct{}

// From 返回 context 携带的日志作用域，未携带时返回空作用域。
func From(ctx context.Context) Scope {
	scope, _ := ctx.Value(scopeKey{}).(Scope)
	return scope
}

// With 返回以 scope 替换日志作用域的 context。
func With(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// WithTrace 返回把串联编号设为 traceID 的 context。
func WithTrace(ctx context.Context, traceID string) context.Context {
	scope := From(ctx)
	scope.TraceID = traceID
	return With(ctx, scope)
}

// WithOperation 返回把业务入口设为 operation 的 context。
func WithOperation(ctx context.Context, operation string) context.Context {
	scope := From(ctx)
	scope.Operation = operation
	return With(ctx, scope)
}

// WithAccount 返回记录发起请求账号的 context。
func WithAccount(ctx context.Context, accountID string) context.Context {
	scope := From(ctx)
	scope.AccountID = accountID
	return With(ctx, scope)
}

// WithWorkspace 返回记录所属工作区的 context。
func WithWorkspace(ctx context.Context, workspaceID string) context.Context {
	scope := From(ctx)
	scope.WorkspaceID = workspaceID
	return With(ctx, scope)
}

// WithMember 返回记录所属工作区与发起请求账号的 context。
func WithMember(ctx context.Context, workspaceID, accountID string) context.Context {
	scope := From(ctx)
	scope.WorkspaceID, scope.AccountID = workspaceID, accountID
	return With(ctx, scope)
}

// NewTraceID 生成新的串联编号。
func NewTraceID() string {
	return random.Hex(16)
}

// Traceparent 返回携带串联编号的 W3C traceparent 请求头，父编号每次随机生成。
func Traceparent(traceID string) string {
	return "00-" + traceID + "-" + random.Hex(8) + "-01"
}

// ParseTraceparent 从 W3C traceparent 请求头取出串联编号：版本 00 恰好四段，更高版本允许追加扩展段；格式不合法或编号全为零时 ok 为 false。
func ParseTraceparent(value string) (traceID string, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) < 4 || (parts[0] == "00" && len(parts) != 4) || len(parts[0]) != 2 || parts[0] == "ff" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return "", false
	}
	// 各段只允许小写十六进制字符，串联编号与父编号不能全为零。
	for _, part := range parts[:4] {
		if strings.Trim(part, "0123456789abcdef") != "" {
			return "", false
		}
	}
	if strings.Trim(parts[1], "0") == "" || strings.Trim(parts[2], "0") == "" {
		return "", false
	}
	return parts[1], true
}

// Attrs 返回作用域中非空字段对应的日志属性。
func (s Scope) Attrs() []slog.Attr {
	fields := []struct{ key, value string }{
		{"trace_id", s.TraceID}, {"operation", s.Operation}, {"task_run_id", s.TaskRunID}, {"action", s.Action},
		{"queue", s.Queue}, {"workspace_id", s.WorkspaceID}, {"account_id", s.AccountID},
	}
	attrs := make([]slog.Attr, 0, len(fields))
	for _, field := range fields {
		if field.value != "" {
			attrs = append(attrs, slog.String(field.key, field.value))
		}
	}
	return attrs
}

// Handler 返回 slog 处理器：把 context 中日志作用域的非空字段附加到日志记录后交给 next 输出。
func Handler(next slog.Handler) slog.Handler {
	return &handler{next: next}
}

// handler 给日志记录附加作用域字段。
type handler struct {
	next slog.Handler
}

// Enabled 按下一个处理器判断。
func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle 附加作用域字段后交给下一个处理器。
func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	if attrs := From(ctx).Attrs(); len(attrs) > 0 {
		record = record.Clone()
		record.AddAttrs(attrs...)
	}
	return h.next.Handle(ctx, record)
}

// WithAttrs 返回附加属性后的处理器。
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{next: h.next.WithAttrs(attrs)}
}

// WithGroup 返回进入分组后的处理器。
func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name)}
}
