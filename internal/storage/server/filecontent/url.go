//go:build server

package filecontent

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/runforyou-ai/cervi/internal/domain"
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

// Links 定义生成文件稳定公开地址所用的本地存储与对象存储根地址。
type Links struct {
	Local string
	S3    string
}

// NewLinks 以部署地址下的本地存储路径和对象存储公开地址创建文件地址生成器；publicURL 为空时本地存储地址为服务端相对路径，由各端按自身连接地址解析。
func NewLinks(publicURL, s3PublicBaseURL string) Links {
	return Links{Local: strings.TrimRight(publicURL, "/") + domain.LocalFilePublicPath, S3: s3PublicBaseURL}
}

// URL 按文件实际存储类型生成稳定公开地址。
func (l Links) URL(backend domain.FileStorageBackend, key string) (string, error) {
	switch backend {
	case domain.FileStorageBackendLocal:
		return PublicURL(l.Local, key)
	case domain.FileStorageBackendS3:
		return PublicURL(l.S3, key)
	default:
		return "", fmt.Errorf("unsupported file storage backend %q", backend)
	}
}
