package modelprovider

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// CompatibleBaseURL 把供应商地址规范为 OpenAI 兼容入口。
func CompatibleBaseURL(brand, value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("parse model base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("model base URL must include scheme and host")
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	switch brand {
	case "alibaba":
		switch {
		case strings.HasSuffix(path, "/compatible-mode/v1"):
		case strings.HasSuffix(path, "/api/v1"):
			path = strings.TrimSuffix(path, "/api/v1") + "/compatible-mode/v1"
		default:
			path += "/compatible-mode/v1"
		}
	case "ollama":
		// Ollama 按服务根地址配置，OpenAI 兼容入口固定在 /v1。
		path = strings.TrimSuffix(path, "/v1") + "/v1"
	default:
		return strings.TrimSuffix(parsed.String(), "/"), nil
	}
	parsed.Path = path
	parsed.RawPath = ""
	return parsed.String(), nil
}
