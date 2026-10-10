// Package webasset 在启动时预压缩内嵌静态资源，并按内容 ETag、gzip 协商和缓存策略提供 HTTP 响应。
package webasset

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
)

// ImmutableCache 是地址随内容变化的资源使用的长期缓存策略。
const ImmutableCache = "public, max-age=31536000, immutable"

// RevalidateCache 是地址固定的资源使用的缓存策略，每次使用前按 ETag 校验。
const RevalidateCache = "no-cache"

// representation 是资源的一种编码表示及其 ETag。
type representation struct {
	etag string
	body []byte
}

// Asset 是预先计算 ETag 的静态资源；gzip 表示只在压缩后更小时存在。
type Asset struct {
	contentType string
	digest      [sha256.Size]byte
	identity    representation
	gzip        *representation
}

// New 按内容计算 ETag 并生成 gzip 表示。
func New(contentType string, raw []byte) Asset {
	digest := sha256.Sum256(raw)
	etag := hex.EncodeToString(digest[:16])
	asset := Asset{
		contentType: contentType,
		digest:      digest,
		identity:    representation{etag: `"` + etag + `"`, body: raw},
	}
	var compressed bytes.Buffer
	encoder, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	_, _ = encoder.Write(raw)
	_ = encoder.Close()
	if compressed.Len() < len(raw) {
		asset.gzip = &representation{etag: `"` + etag + `-gzip"`, body: compressed.Bytes()}
	}
	return asset
}

// Digest 返回资源内容的 SHA-256 摘要。
func (a Asset) Digest() [sha256.Size]byte {
	return a.digest
}

// Serve 按客户端接受的编码返回资源，If-None-Match 命中所选表示时返回未修改。
func (a Asset) Serve(writer http.ResponseWriter, request *http.Request, cacheControl string) {
	header := writer.Header()
	header.Set("Content-Type", a.contentType)
	header.Set("Cache-Control", cacheControl)
	header.Set("X-Content-Type-Options", "nosniff")
	selected := a.identity
	if a.gzip != nil {
		header.Set("Vary", "Accept-Encoding")
		if acceptsGzip(request.Header.Get("Accept-Encoding")) {
			selected = *a.gzip
			header.Set("Content-Encoding", "gzip")
		}
	}
	header.Set("ETag", selected.etag)
	// If-None-Match 含所选表示的 ETag 或通配符时返回未修改。
	for _, candidate := range strings.Split(request.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == selected.etag || candidate == "*" {
			header.Del("Content-Encoding")
			writer.WriteHeader(http.StatusNotModified)
			return
		}
	}
	header.Set("Content-Length", strconv.Itoa(len(selected.body)))
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(selected.body)
	}
}

// acceptsGzip 按 Accept-Encoding 的编码与权重判断客户端是否接受 gzip，q=0 表示拒绝。
func acceptsGzip(acceptEncoding string) bool {
	wildcard := false
	for _, part := range strings.Split(acceptEncoding, ",") {
		coding, params, _ := strings.Cut(part, ";")
		accepted := true
		if weight, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			q, err := strconv.ParseFloat(weight, 64)
			accepted = err == nil && q > 0
		}
		switch strings.ToLower(strings.TrimSpace(coding)) {
		case "gzip", "x-gzip":
			return accepted
		case "*":
			wildcard = accepted
		}
	}
	return wildcard
}
