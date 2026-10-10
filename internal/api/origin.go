//go:build server

package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/runforyou-ai/luway/internal/appservice"
)

// ClientOriginMiddleware 解析请求来源 IP 与国家代码并写入请求上下文，ipHeader 与 countryHeader 是可信代理写入的请求头，代理必须覆盖客户端同名请求头。
// 来源 IP 取 ipHeader 中第一个有效地址（可带端口），ipHeader 为空或地址无效时取连接对端地址；国家代码取 countryHeader 中的两位字母并转为大写，countryHeader 为空或取值无效时不写入。
func ClientOriginMiddleware(ipHeader, countryHeader string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			ctx := request.Context()
			ip := net.IP(nil)
			if ipHeader != "" {
				ip = parseAddressIP(strings.TrimSpace(strings.Split(request.Header.Get(ipHeader), ",")[0]))
			}
			if ip == nil {
				ip = parseAddressIP(request.RemoteAddr)
			}
			if ip != nil {
				ctx = appservice.WithClientIP(ctx, ip.String())
			}
			// 配置国家请求头时，只接受两位字母并统一转为大写。
			if countryHeader != "" {
				country := strings.ToUpper(strings.TrimSpace(request.Header.Get(countryHeader)))
				if len(country) == 2 && strings.Trim(country, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
					ctx = appservice.WithClientCountry(ctx, country)
				}
			}
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
}

// parseAddressIP 解析 IP 或「IP:端口」形式的地址，无法解析时返回 nil。
func parseAddressIP(value string) net.IP {
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return net.ParseIP(value)
}
