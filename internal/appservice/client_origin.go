package appservice

import "context"

// clientIPKey 是请求上下文中来源 IP 的键。
type clientIPKey struct{}

// clientCountryKey 是请求上下文中来源国家代码的键。
type clientCountryKey struct{}

// WithClientIP 返回携带服务端解析的请求来源 IP 的上下文。
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIP 返回服务端按可信代理配置解析的请求来源 IP，未解析时为空。
func ClientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// WithClientCountry 返回携带可信代理提供的请求来源国家代码的上下文。
func WithClientCountry(ctx context.Context, country string) context.Context {
	return context.WithValue(ctx, clientCountryKey{}, country)
}

// ClientCountry 返回可信代理提供的请求来源国家代码（ISO 3166-1 两位大写字母），未采集时为空。
func ClientCountry(ctx context.Context) string {
	country, _ := ctx.Value(clientCountryKey{}).(string)
	return country
}
