// Package embedhost 规范化并校验网站渠道允许嵌入的主机。
package embedhost

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	// MaxHosts 是单个网站渠道最多允许配置的主机数。
	MaxHosts = 50
	// MaxHostLength 是单个主机配置的最大长度。
	MaxHostLength = 253
)

var hostLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// Normalize 把域名或 HTTP(S) 地址整理为可比较的主机配置。
func Normalize(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", false
	}
	if value == "*" {
		return value, true
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" {
			return "", false
		}
		value = parsed.Host
	} else if strings.ContainsAny(value, "/?#@") {
		return "", false
	}

	prefix := ""
	switch {
	case strings.HasPrefix(value, "*."):
		prefix = "*."
		value = strings.TrimPrefix(value, "*.")
	case strings.HasPrefix(value, "."):
		prefix = "."
		value = strings.TrimPrefix(value, ".")
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return "", false
	}

	host, port, hasPort := strings.Cut(value, ":")
	if hasPort {
		if strings.Contains(port, ":") {
			return "", false
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", false
		}
	}
	// 校验 ASCII 主机名。
	validHost := len(host) <= MaxHostLength
	if validHost {
		for _, label := range strings.Split(host, ".") {
			if !hostLabelPattern.MatchString(label) {
				validHost = false
				break
			}
		}
	}
	if !validHost {
		return "", false
	}

	normalized := prefix + host
	if hasPort {
		normalized += ":" + port
	}
	if len(normalized) > MaxHostLength {
		return "", false
	}
	return normalized, true
}

// NormalizeAll 整理主机列表、去重，并在存在非法值时返回失败。
func NormalizeAll(values []string) ([]string, bool) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		normalized, ok := Normalize(value)
		if !ok {
			return nil, false
		}
		if normalized == "*" {
			return []string{"*"}, true
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, true
}

// Allows 判断请求主机是否符合允许列表；空列表或星号表示不限制。
func Allows(allowed []string, requestHost string) bool {
	if len(allowed) == 0 {
		return true
	}
	host, ok := Normalize(requestHost)
	if !ok || host == "*" || strings.HasPrefix(host, ".") {
		return false
	}
	for _, pattern := range allowed {
		// 判断主机是否命中精确或子域名配置。
		matched := host == pattern
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			matched = strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".")
		} else if strings.HasPrefix(pattern, ".") {
			matched = strings.HasSuffix(host, pattern) && host != strings.TrimPrefix(pattern, ".")
		}
		if pattern == "*" || matched {
			return true
		}
	}
	return false
}

// FrameAncestors 返回与允许列表一致的 CSP frame-ancestors 来源。
func FrameAncestors(allowed []string) string {
	if len(allowed) == 0 {
		return "*"
	}
	sources := make([]string, 0, len(allowed))
	for _, value := range allowed {
		if value == "*" {
			return "*"
		}
		if strings.HasPrefix(value, ".") {
			value = "*" + value
		}
		sources = append(sources, value)
	}
	return strings.Join(sources, " ")
}
