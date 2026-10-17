//go:build server

package filecontent

import (
	"errors"
	"net/url"
	"path"
	"strings"
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
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", errors.New("invalid public file base URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + key
	base.RawPath = ""
	return base.String(), nil
}

// Links 根据部署的文件访问地址生成稳定 URL。
type Links struct{ s3 S3Settings }

// NewLinks 创建文件访问地址生成器。
func NewLinks(s3 S3Settings) Links { return Links{s3: s3} }

// URL 拼接访问地址与对象键，不重复追加桶名。
func (l Links) URL(key string) (string, error) { return PublicURL(l.s3().PublicBaseURL, key) }
