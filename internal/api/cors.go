//go:build server

package api

import (
	"net/http"
	"strings"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// corsMaxAge 是浏览器缓存跨域预检结果的秒数。
const corsMaxAge = "7200"

// corsAllowedHeaders 是原生端与执行器跨域调用接口时携带的请求头。
var corsAllowedHeaders = strings.Join([]string{
	"Authorization", "Content-Type", "Accept-Language", appservice.WorkspaceHeader, appservice.ClientAPIVersionHeader, appservice.TraceparentHeader,
}, ", ")

// CORSMiddleware 允许原生端 WebView 以 Bearer 令牌跨域调用 /api 下的接口与事件流；/api/public 下以 Cookie 或签名认证的公开入口不开放跨域。
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/api/public/") {
			next.ServeHTTP(writer, request)
			return
		}
		header := writer.Header()
		header.Set("Access-Control-Allow-Origin", "*")
		header.Set("Access-Control-Expose-Headers", appservice.TraceHeader+", Content-Language, Retry-After")
		// 跨域预检请求直接应答，不进入业务路由。
		if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
			header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			header.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			header.Set("Access-Control-Max-Age", corsMaxAge)
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
