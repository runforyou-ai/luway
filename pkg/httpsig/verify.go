package httpsig

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalid 表示签名请求头缺失或格式不符、签名覆盖范围与本包不同、请求体摘要不一致或签名校验失败。
	ErrInvalid = errors.New("httpsig: invalid signature")
	// ErrExpired 表示签名时间与验签时间相差超过 MaxClockSkew。
	ErrExpired = errors.New("httpsig: signature expired")
)

// MaxClockSkew 是签名时间与验签时间允许相差的最长时间。
const MaxClockSkew = 5 * time.Minute

// Signature 是从请求头解析出的签名，KeyID、Created 与 Nonce 是签名参数。
type Signature struct {
	KeyID   string
	Created time.Time
	Nonce   string
	// base 是按请求重建的签名基串，signature 是请求携带的签名值。
	base      string
	signature []byte
}

// Parse 解析请求的 Signature-Input 与 Signature 请求头，校验签名覆盖本包签名的全部组成部分、签名时间与 now 相差不超过 MaxClockSkew，以及 Content-Digest 与 body 一致；path 是客户端签名时编码后的请求路径，经反向代理或路由前缀剥离后可能与 request.URL 不同。
func Parse(request *http.Request, path string, body []byte, now time.Time) (Signature, error) {
	params, ok := strings.CutPrefix(request.Header.Get("Signature-Input"), signatureLabel+"=")
	if !ok {
		return Signature{}, ErrInvalid
	}
	encoded, ok := strings.CutPrefix(request.Header.Get("Signature"), signatureLabel+"=:")
	if !ok || !strings.HasSuffix(encoded, ":") {
		return Signature{}, ErrInvalid
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(encoded, ":"))
	if err != nil || len(value) != ed25519.SignatureSize {
		return Signature{}, ErrInvalid
	}
	fields, ok := strings.CutPrefix(params, `("`+strings.Join(coveredComponents, `" "`)+`")`)
	if !ok {
		return Signature{}, ErrInvalid
	}
	parsed := Signature{signature: value}
	var created int64
	alg := ""
	for _, field := range strings.Split(fields, ";")[1:] {
		name, raw, found := strings.Cut(field, "=")
		if !found {
			return Signature{}, ErrInvalid
		}
		text := strings.Trim(raw, `"`)
		switch name {
		case "created":
			if created, err = strconv.ParseInt(raw, 10, 64); err != nil {
				return Signature{}, ErrInvalid
			}
		case "nonce":
			parsed.Nonce = text
		case "keyid":
			parsed.KeyID = text
		case "alg":
			alg = text
		}
	}
	if created == 0 || parsed.Nonce == "" || parsed.KeyID == "" || alg != "ed25519" {
		return Signature{}, ErrInvalid
	}
	parsed.Created = time.Unix(created, 0)
	if skew := now.Sub(parsed.Created); skew > MaxClockSkew || skew < -MaxClockSkew {
		return Signature{}, ErrExpired
	}
	digest := sha256.Sum256(body)
	contentDigest := "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
	if request.Header.Get("Content-Digest") != contentDigest {
		return Signature{}, ErrInvalid
	}
	if path == "" {
		path = "/"
	}
	parsed.base = strings.Join([]string{
		`"@method": ` + request.Method,
		`"@authority": ` + receivedAuthority(request.Host),
		`"@path": ` + path,
		`"@query": ?` + request.URL.RawQuery,
		`"content-digest": ` + contentDigest,
		`"@signature-params": ` + params,
	}, "\n")
	return parsed, nil
}

// Verify 用签名方的公钥校验签名。
func (s Signature) Verify(key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, []byte(s.base), s.signature) {
		return ErrInvalid
	}
	return nil
}

// receivedAuthority 返回收到请求的小写主机；服务端不区分请求协议，HTTP 与 HTTPS 的默认端口都省略。
func receivedAuthority(host string) string {
	host = strings.ToLower(host)
	for _, port := range []string{":443", ":80"} {
		host = strings.TrimSuffix(host, port)
	}
	return host
}
