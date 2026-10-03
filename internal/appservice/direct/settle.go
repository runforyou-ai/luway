//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// settle 由业务入口方法以 defer 直接调用，统一收尾方法返回的错误：panic 转为带调用栈的错误，非业务错误由 internal 转为内部错误，
// 请求未取消或发生 panic 时以 Error 级别记录操作失败的原始错误；operation 是业务入口方法名。
func settle(ctx context.Context, operation string, err *error, internal func(cause error) *appservice.Error) {
	recovered := recover()
	if recovered != nil {
		*err = common.NewPanicError(recovered)
	}
	if *err == nil {
		return
	}
	failure, ok := errors.AsType[*appservice.Error](*err)
	if !ok {
		failure = internal(*err)
		*err = failure
	}
	if cause := failure.Unwrap(); cause != nil && (ctx.Err() == nil || recovered != nil) {
		slog.ErrorContext(ctx, "业务调用失败", "operation", operation, "error", cause)
	}
}

// visitorInternalError 返回按访客语言构造内部错误的函数。
func visitorInternalError(meta appservice.WebsiteVisitorMeta) func(error) *appservice.Error {
	return func(cause error) *appservice.Error {
		return appservice.WebsiteVisitorFailedError(meta.Locale, i18n.VisitorErrorInternal, cause)
	}
}

// internalError 返回按成员请求语言构造内部错误的函数。
func internalError(meta appservice.RequestMeta) func(error) *appservice.Error {
	return func(cause error) *appservice.Error {
		return appservice.FailedError(meta, i18n.ErrorInternal, cause)
	}
}
