//go:build server

package api

import (
	"net/http"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/logscope"
)

// TraceMiddleware 为每个入站请求确定串联编号，写入请求上下文的日志作用域与响应头：请求带合法 traceparent 时沿用其中的编号，否则生成新编号。
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		traceID, ok := logscope.ParseTraceparent(request.Header.Get(appservice.TraceparentHeader))
		if !ok {
			traceID = logscope.NewTraceID()
		}
		writer.Header().Set(appservice.TraceHeader, traceID)
		next.ServeHTTP(writer, request.WithContext(logscope.WithTrace(request.Context(), traceID)))
	})
}
