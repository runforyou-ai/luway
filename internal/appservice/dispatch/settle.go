//go:build server

// Package dispatch 提供服务端应用契约分发层共用的会话认证、错误收尾与动作错误转换。
package dispatch

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/support"
)

// Settle 由业务入口方法以 defer 直接调用，统一收尾方法返回的错误：panic 转为带调用栈的错误，非业务错误由 internal 转为内部错误，请求未取消或发生 panic 时以 Error 级别记录操作失败的原始错误；ctx 指向方法内的 context 变量，记录时带上认证后补充的日志作用域。
func Settle(ctx *context.Context, err *error, internal func(cause error) *appservice.Error) {
	recovered := recover()
	if recovered != nil {
		*err = support.NewPanicError(recovered)
	}
	if *err == nil {
		return
	}
	failure, ok := errors.AsType[*appservice.Error](*err)
	if !ok {
		failure = internal(*err)
		*err = failure
	}
	if cause := failure.Unwrap(); cause != nil && ((*ctx).Err() == nil || recovered != nil) {
		slog.ErrorContext(*ctx, "业务调用失败", "error", cause)
	}
}

// InternalError 返回按成员请求语言构造内部错误的函数。
func InternalError(meta appservice.RequestMeta) func(error) *appservice.Error {
	return func(cause error) *appservice.Error {
		return appservice.FailedError(meta, i18n.ErrorInternal, cause)
	}
}
