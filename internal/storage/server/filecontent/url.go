//go:build server

package filecontent

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
)

// ImmutableCacheControl 是 UUID 对象统一使用的浏览器和 CDN 缓存策略。
const ImmutableCacheControl = "public, max-age=31536000, immutable"

// PublicURL 将公开基础地址与受控对象键拼成稳定访问地址。
func PublicURL(baseURL, key string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || key == "" || strings.TrimSpace(key) != key || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return "", errors.New("invalid public file URL")
	}
	cleaned := path.Clean(key)
	if cleaned != key || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("invalid public file key")
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.RawQuery != "" || base.Fragment != "" || base.User != nil {
		return "", errors.New("invalid public file base URL")
	}
	if base.IsAbs() {
		if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
			return "", errors.New("invalid public file base URL")
		}
	} else if !strings.HasPrefix(base.Path, "/") {
		return "", errors.New("invalid public file base path")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + key
	base.RawPath = ""
	return base.String(), nil
}

// Links 按生成地址时的部署地址与对象存储公开地址生成文件稳定公开地址。
type Links struct {
	publicURL func() string
	s3        S3Settings
}

// NewLinks 创建文件地址生成器；publicURL 为 nil 时本地存储地址为服务端相对路径，由各端按自身连接地址解析。
func NewLinks(publicURL func() string, s3 S3Settings) Links {
	return Links{publicURL: publicURL, s3: s3}
}

// URL 按文件实际存储类型生成稳定公开地址。
func (l Links) URL(backend domain.FileStorageBackend, key string) (string, error) {
	switch backend {
	case domain.FileStorageBackendLocal:
		base := ""
		if l.publicURL != nil {
			base = strings.TrimRight(l.publicURL(), "/")
		}
		return PublicURL(base+domain.LocalFilePublicPath, key)
	case domain.FileStorageBackendS3:
		return PublicURL(l.s3().PublicBaseURL, key)
	default:
		return "", fmt.Errorf("unsupported file storage backend %q", backend)
	}
}
