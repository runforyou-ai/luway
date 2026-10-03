// Package httpsig 按 RFC 9421 用 Ed25519 为 HTTP 请求签名，并按 RFC 9530 附加请求体摘要。
package httpsig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// signatureLabel 是请求头中签名的标签。
const signatureLabel = "sig"

// coveredComponents 是签名覆盖的请求组成部分。
var coveredComponents = []string{"@method", "@authority", "@path", "@query", "content-digest"}

// Signer 使用 Ed25519 私钥签名请求，KeyID 写入签名参数 keyid。
type Signer struct {
	KeyID string
	Key   ed25519.PrivateKey
}

// Sign 以当前时间和随机 nonce 为请求附加 Content-Digest、Signature-Input 与 Signature 请求头，body 是实际发送的请求体字节。
func (s Signer) Sign(request *http.Request, body []byte) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate signature nonce: %w", err)
	}
	s.sign(request, body, time.Now(), base64.RawURLEncoding.EncodeToString(nonce))
	return nil
}

// sign 按给定签名时间和 nonce 计算并写入签名请求头。
func (s Signer) sign(request *http.Request, body []byte, created time.Time, nonce string) {
	digest := sha256.Sum256(body)
	contentDigest := "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
	request.Header.Set("Content-Digest", contentDigest)

	params := `("` + strings.Join(coveredComponents, `" "`) + `");created=` + strconv.FormatInt(created.Unix(), 10) +
		`;nonce="` + nonce + `";keyid="` + s.KeyID + `";alg="ed25519"`
	base := strings.Join([]string{
		`"@method": ` + request.Method,
		`"@authority": ` + authority(request),
		`"@path": ` + path(request),
		`"@query": ?` + request.URL.RawQuery,
		`"content-digest": ` + contentDigest,
		`"@signature-params": ` + params,
	}, "\n")
	signature := ed25519.Sign(s.Key, []byte(base))
	request.Header.Set("Signature-Input", signatureLabel+"="+params)
	request.Header.Set("Signature", signatureLabel+"=:"+base64.StdEncoding.EncodeToString(signature)+":")
}

// authority 返回小写的目标主机，省略协议默认端口。
func authority(request *http.Request) string {
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	host = strings.ToLower(host)
	switch {
	case request.URL.Scheme == "https" && strings.HasSuffix(host, ":443"):
		return strings.TrimSuffix(host, ":443")
	case request.URL.Scheme == "http" && strings.HasSuffix(host, ":80"):
		return strings.TrimSuffix(host, ":80")
	}
	return host
}

// path 返回编码后的请求路径，空路径按根路径处理。
func path(request *http.Request) string {
	if escaped := request.URL.EscapedPath(); escaped != "" {
		return escaped
	}
	return "/"
}

// Transport 是逐次签名的 HTTP 传输层，每次发送（包括重试）都重新取得签名者并生成新的 nonce。
type Transport struct {
	// Base 发送已签名的请求，为空时使用 http.DefaultTransport。
	Base http.RoundTripper
	// Signer 返回本次请求使用的签名者。
	Signer func(*http.Request) (Signer, error)
}

// RoundTrip 读取请求体，签名后交给 Base 发送。
func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	signer, err := t.Signer(request)
	if err != nil {
		if request.Body != nil {
			_ = request.Body.Close()
		}
		return nil, err
	}
	var body []byte
	if request.Body != nil {
		body, err = io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
	}
	signed := request.Clone(request.Context())
	signed.Header = request.Header.Clone()
	signed.Body = io.NopCloser(bytes.NewReader(body))
	signed.ContentLength = int64(len(body))
	signed.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	if err := signer.Sign(signed, body); err != nil {
		return nil, err
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(signed)
}
