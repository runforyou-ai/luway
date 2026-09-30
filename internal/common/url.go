package common

import (
	"net/url"
	"strings"
)

// ValidHTTPBaseURL 校验地址为不含认证信息、查询和片段的完整 HTTP 地址，用于 API 端点。
func ValidHTTPBaseURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.IsAbs() && parsed.Host != "" && parsed.User == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.RawQuery == "" && parsed.Fragment == ""
}

// ValidHTTPURL 校验地址为不含认证信息的完整 HTTP 地址，允许查询和片段，用于浏览器打开的页面地址。
func ValidHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.Host != "" && parsed.User == nil &&
		(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
}
