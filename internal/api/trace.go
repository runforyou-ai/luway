//go:build server

package api

import (
	"log/slog"
	"net/http"
	"time"

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
		ctx := logscope.WithTrace(request.Context(), traceID)
		started := time.Now() //clock:local
		defer func() {
			duration := time.Since(started) //clock:local
			slog.InfoContext(ctx, "HTTP 请求完成", "method", request.Method, "path", request.URL.Path, "duration_ms", float64(duration.Microseconds())/1000)
		}()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}
