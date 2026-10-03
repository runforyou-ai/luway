// Package httpsig 按 RFC 9421 用 Ed25519 为 HTTP 请求签名与验签，并按 RFC 9530 附加和校验请求体摘要。
package httpsig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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
	contentDigest := sha256Digest(body)
	request.Header.Set("Content-Digest", contentDigest)

	params := `("` + strings.Join(coveredComponents, `" "`) + `");created=` + strconv.FormatInt(created.Unix(), 10) +
		`;nonce="` + nonce + `";keyid="` + s.KeyID + `";alg="ed25519"`
	base := signatureBase(coveredComponents, componentValues(request, request.URL, contentDigest, nil), params)
	signature := ed25519.Sign(s.Key, []byte(base))
	request.Header.Set("Signature-Input", signatureLabel+"="+params)
	request.Header.Set("Signature", signatureLabel+"=:"+base64.StdEncoding.EncodeToString(signature)+":")
}

// sha256Digest 返回请求体的 sha-256 Content-Digest 取值。
func sha256Digest(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
}

// signatureBase 按 components 的顺序生成签名基串，values 是各组成部分的取值。
func signatureBase(components []string, values map[string]string, params string) string {
	lines := make([]string, 0, len(components)+1)
	for _, component := range components {
		lines = append(lines, `"`+component+`": `+values[component])
	}
	return strings.Join(append(lines, `"@signature-params": `+params), "\n")
}

// componentValues 返回请求各组成部分的取值，target 提供路径与查询，额外的请求头组成部分取对应请求头。
func componentValues(request *http.Request, target *url.URL, contentDigest string, components []string) map[string]string {
	values := map[string]string{
		"@method": request.Method, "@authority": authority(request), "@path": path(target),
		"@query": "?" + target.RawQuery, "content-digest": contentDigest,
	}
	for _, component := range components {
		if _, derived := values[component]; !derived {
			values[component] = strings.TrimSpace(request.Header.Get(component))
		}
	}
	return values
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
func path(target *url.URL) string {
	if escaped := target.EscapedPath(); escaped != "" {
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

// ErrInvalid 表示请求缺少签名、签名格式或参数不符、请求体摘要不一致、超出时间窗口或签名与公钥不匹配。
var ErrInvalid = errors.New("httpsig: invalid signature")

// Verify 校验服务端收到的请求：Signature-Input 中任一标签的签名按声明顺序覆盖本包签名的全部组成部分（可另含请求头），keyid 为 keyID，created 与 now 相差不超过 maxSkew，且 Signature 中同一标签的签名由 key 签发；Content-Digest 须与 body 一致，路径与查询取原始请求目标。
func Verify(request *http.Request, body []byte, keyID string, key ed25519.PublicKey, now time.Time, maxSkew time.Duration) error {
	contentDigest := request.Header.Get("Content-Digest")
	sha512Sum := sha512.Sum512(body)
	if contentDigest != sha256Digest(body) && contentDigest != "sha-512=:"+base64.StdEncoding.EncodeToString(sha512Sum[:])+":" {
		return fmt.Errorf("%w: content digest mismatch", ErrInvalid)
	}
	target := request.URL
	if request.RequestURI != "" {
		var err error
		if target, err = url.ParseRequestURI(request.RequestURI); err != nil {
			return fmt.Errorf("%w: malformed request target", ErrInvalid)
		}
	}
	// 按标签收集 Signature 中的签名字节。
	signatures := map[string]string{}
	for _, member := range strings.Split(request.Header.Get("Signature"), ",") {
		label, value, _ := strings.Cut(strings.TrimSpace(member), "=")
		signatures[label] = value
	}
	for _, member := range strings.Split(request.Header.Get("Signature-Input"), ",") {
		label, params, _ := strings.Cut(strings.TrimSpace(member), "=")
		encoded, found := signatures[label]
		components, valid := signatureParams(params, keyID, now, maxSkew)
		if !found || !valid {
			continue
		}
		signature, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(encoded, ":"), ":"))
		if err != nil {
			continue
		}
		base := signatureBase(components, componentValues(request, target, contentDigest, components), params)
		if ed25519.Verify(key, []byte(base), signature) {
			return nil
		}
	}
	return fmt.Errorf("%w: no valid signature", ErrInvalid)
}

// signatureParams 解析签名参数并按声明顺序返回覆盖的组成部分；须覆盖本包签名的全部组成部分，keyid 为 keyID、算法为 ed25519、带 nonce，且 created 与 now 相差不超过 maxSkew。
func signatureParams(params, keyID string, now time.Time, maxSkew time.Duration) ([]string, bool) {
	list, rest, found := strings.Cut(strings.TrimPrefix(params, "("), ")")
	if !found || !strings.HasPrefix(params, "(") {
		return nil, false
	}
	components := make([]string, 0, len(coveredComponents))
	for _, item := range strings.Fields(list) {
		components = append(components, strings.ToLower(strings.Trim(item, `"`)))
	}
	for _, required := range coveredComponents {
		if !slices.Contains(components, required) {
			return nil, false
		}
	}
	values := map[string]string{}
	for _, item := range strings.Split(strings.TrimPrefix(rest, ";"), ";") {
		name, value, _ := strings.Cut(item, "=")
		values[name] = strings.Trim(value, `"`)
	}
	created, err := strconv.ParseInt(values["created"], 10, 64)
	skew := now.Sub(time.Unix(created, 0))
	valid := err == nil && values["keyid"] == keyID && values["alg"] == "ed25519" && values["nonce"] != "" && skew <= maxSkew && skew >= -maxSkew
	return components, valid
}
