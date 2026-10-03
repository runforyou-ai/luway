//go:build server

package api

import (
	"net/http"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// ClientVersionMiddleware 拒绝原生端接口版本低于服务端接受的最低版本的请求，返回需要升级客户端的会话错误。
func ClientVersionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if appservice.RequestClientOutdated(request.Header) {
			appservice.WriteHTTPError(writer, request, appservice.ClientUpgradeError(appservice.RequestMetaFromHTTP(request.Header)))
			return
		}
		next.ServeHTTP(writer, request)
	})
}
