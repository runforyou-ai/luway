// Package httpjson 发送 JSON 请求并读取外部 HTTP 服务的响应：响应体按上限读取，错误中的地址与凭据已脱敏，并按传输、状态、超限与解码分类；请求只发送一次。
package httpjson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/runforyou-ai/support/str"
)

const (
	// DefaultMaxResponseBytes 是未指定上限时允许读取的响应体字节数。
	DefaultMaxResponseBytes = 16 << 20
	// maxErrorBodyRunes 是状态错误中保留的响应体字符数。
	maxErrorBodyRunes = 512
	// redacted 是脱敏后替换凭据的文本。
	redacted = "REDACTED"
)

// ErrResponseTooLarge 表示响应体超过读取上限。
var ErrResponseTooLarge = errors.New("response body too large")

// Doer 是发送 HTTP 请求的最小客户端契约。
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Request 描述一次外部 HTTP 调用。
type Request struct {
	// Method 是请求方法，为空时为 GET。
	Method string
	// URL 是完整请求地址。
	URL string
	// Header 是附加请求头，发送前复制。
	Header http.Header
	// JSON 是编码为 JSON 的请求体，HTML 字符不转义；为 nil 时按 Body 发送。
	JSON any
	// Body 是原样发送的请求体，ContentType 给出其类型；JSON 不为 nil 时忽略。
	Body io.Reader
	// ContentType 是 Body 的内容类型。
	ContentType string
	// MaxResponseBytes 是响应体读取上限，不大于 0 时使用 DefaultMaxResponseBytes。
	MaxResponseBytes int64
	// Secrets 是需要从错误文本中隐去的凭据，例如写在路径中的令牌。
	Secrets []string
}

// Response 是已读取完整响应体的外部 HTTP 响应。
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// OK 返回响应状态是否为 2xx。
func (r *Response) OK() bool {
	return r.StatusCode >= http.StatusOK && r.StatusCode < http.StatusMultipleChoices
}

// TransportError 表示请求未取得响应，URL 已脱敏，Err 是底层传输错误。
type TransportError struct {
	Method string
	URL    string
	Err    error
}

// Error 返回不含凭据的传输错误描述。
func (e *TransportError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Method, e.URL, e.Err)
}

// Unwrap 返回底层传输错误。
func (e *TransportError) Unwrap() error {
	return e.Err
}

// Timeout 返回传输错误是否由超时引起。
func (e *TransportError) Timeout() bool {
	if errors.Is(e.Err, context.DeadlineExceeded) {
		return true
	}
	networkError, ok := errors.AsType[net.Error](e.Err)
	return ok && networkError.Timeout()
}

// StatusError 表示响应状态不是 2xx，Body 是截断并脱敏后的响应体。
type StatusError struct {
	StatusCode int
	Body       string
}

// Error 返回带状态码与响应体摘要的错误描述。
func (e *StatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("unexpected HTTP status %d", e.StatusCode)
	}
	return fmt.Sprintf("unexpected HTTP status %d: %s", e.StatusCode, e.Body)
}

// DecodeError 表示成功响应的响应体无法按 JSON 解码。
type DecodeError struct {
	Err error
}

// Error 返回解码错误描述。
func (e *DecodeError) Error() string {
	return "decode response: " + e.Err.Error()
}

// Unwrap 返回 JSON 解码错误。
func (e *DecodeError) Unwrap() error {
	return e.Err
}

// Kind 是与具体外部服务无关的调用失败类别。
type Kind string

const (
	// KindNone 表示调用成功。
	KindNone Kind = ""
	// KindCanceled 表示调用方取消了请求。
	KindCanceled Kind = "canceled"
	// KindTimeout 表示请求超时。
	KindTimeout Kind = "timeout"
	// KindTransport 表示网络或连接失败，未取得响应。
	KindTransport Kind = "transport"
	// KindStatus 表示响应状态不是 2xx。
	KindStatus Kind = "status"
	// KindTooLarge 表示响应体超过读取上限。
	KindTooLarge Kind = "too_large"
	// KindDecode 表示成功响应无法解码。
	KindDecode Kind = "decode"
	// KindOther 表示请求构造等其他失败。
	KindOther Kind = "other"
)

// Classify 返回调用错误的失败类别。
func Classify(err error) Kind {
	switch {
	case err == nil:
		return KindNone
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.Is(err, ErrResponseTooLarge):
		return KindTooLarge
	}
	if transport, ok := errors.AsType[*TransportError](err); ok {
		if transport.Timeout() {
			return KindTimeout
		}
		return KindTransport
	}
	if _, ok := errors.AsType[*StatusError](err); ok {
		return KindStatus
	}
	if _, ok := errors.AsType[*DecodeError](err); ok {
		return KindDecode
	}
	return KindOther
}

// Do 发送一次请求并按上限读取响应体，任何响应状态都返回响应；未取得响应时返回 *TransportError，响应体超限时返回包装 ErrResponseTooLarge 并带脱敏地址的错误，调用方取消时返回上下文错误。
func Do(ctx context.Context, client Doer, input Request) (*Response, error) {
	method := input.Method
	if method == "" {
		method = http.MethodGet
	}
	body := input.Body
	contentType := input.ContentType
	if input.JSON != nil {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(input.JSON); err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
		contentType = "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, method, input.URL, body)
	if err != nil {
		return nil, fmt.Errorf("create request %s %s: %w", method, RedactURL(input.URL, input.Secrets...), unwrapURLError(err))
	}
	if input.Header != nil {
		request.Header = input.Header.Clone()
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, &TransportError{Method: method, URL: RedactURL(input.URL, input.Secrets...), Err: redactError(unwrapURLError(err), input.Secrets)}
	}
	defer response.Body.Close()
	limit := input.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, &TransportError{Method: method, URL: RedactURL(input.URL, input.Secrets...), Err: redactError(err, input.Secrets)}
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s %s: %w", method, RedactURL(input.URL, input.Secrets...), ErrResponseTooLarge)
	}
	return &Response{StatusCode: response.StatusCode, Header: response.Header, Body: data}, nil
}

// Call 发送一次请求，响应状态不是 2xx 时返回 *StatusError，成功时把响应体解码到 output；output 为 nil 时不解码。
func Call(ctx context.Context, client Doer, input Request, output any) error {
	response, err := Do(ctx, client, input)
	if err != nil {
		return err
	}
	if !response.OK() {
		return NewStatusError(response.StatusCode, response.Body, input.Secrets...)
	}
	if output == nil {
		return nil
	}
	return Decode(response.Body, output)
}

// Decode 把响应体按 JSON 解码到 output，失败时返回 *DecodeError。
func Decode(body []byte, output any) error {
	if err := json.Unmarshal(body, output); err != nil {
		return &DecodeError{Err: err}
	}
	return nil
}

// NewStatusError 用截断并隐去凭据的响应体创建状态错误。
func NewStatusError(status int, body []byte, secrets ...string) *StatusError {
	text := strings.TrimSpace(strings.ToValidUTF8(string(body), "�"))
	text = redactText(text, secrets)
	return &StatusError{StatusCode: status, Body: str.Limit(text, maxErrorBodyRunes, "…")}
}

// RedactURL 返回去掉用户信息、查询参数值与片段并隐去凭据的地址，地址无法解析时只返回脱敏标记。
func RedactURL(address string, secrets ...string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return redacted
	}
	parsed.User = nil
	parsed.Fragment = ""
	if parsed.RawQuery != "" {
		query := parsed.Query()
		for name := range query {
			query[name] = []string{redacted}
		}
		parsed.RawQuery = query.Encode()
	}
	return redactText(parsed.String(), secrets)
}

// redactText 把文本中出现的凭据及其路径转义形式替换为脱敏标记。
func redactText(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, redacted)
		text = strings.ReplaceAll(text, url.PathEscape(secret), redacted)
		text = strings.ReplaceAll(text, url.QueryEscape(secret), redacted)
	}
	return text
}

// redactError 在错误文本含凭据时返回文本已脱敏、仍可经 errors.Is 与 errors.As 匹配原错误的错误。
func redactError(err error, secrets []string) error {
	text := err.Error()
	if cleaned := redactText(text, secrets); cleaned != text {
		return &redactedError{text: cleaned, err: err}
	}
	return err
}

// redactedError 是错误文本隐去凭据的传输错误，err 是原错误。
type redactedError struct {
	text string
	err  error
}

// Error 返回脱敏后的错误文本。
func (e *redactedError) Error() string { return e.text }

// Unwrap 返回原错误。
func (e *redactedError) Unwrap() error { return e.err }

// Timeout 返回原错误是否由超时引起。
func (e *redactedError) Timeout() bool { return (&TransportError{Err: e.err}).Timeout() }

// Temporary 满足 net.Error 接口，固定返回 false。
func (e *redactedError) Temporary() bool { return false }

// unwrapURLError 去掉 *url.Error 外层，其中带有未脱敏的完整地址。
func unwrapURLError(err error) error {
	if urlError, ok := errors.AsType[*url.Error](err); ok {
		return urlError.Err
	}
	return err
}
