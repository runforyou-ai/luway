//go:build !server && !ios && !android

package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// requestTimeout 是单次服务端请求的时限。
	requestTimeout = 30 * time.Second
	// maxResponseBytes 是服务端响应体的读取上限。
	maxResponseBytes = 16 << 20
)

// ErrCredentialInvalid 表示服务端不再接受电脑凭据：电脑已撤销、主人已不是工作区的有效成员或凭据已更换。
var ErrCredentialInvalid = errors.New("computer credential invalid")

// ErrExecutorOutdated 表示服务端不接受这个版本的执行器，需要升级到与服务端一致的版本。
var ErrExecutorOutdated = errors.New("executor version does not match the server")

// client 以电脑凭据调用服务端的执行器契约。
type client struct {
	baseURL    *url.URL
	credential string
	http       *http.Client
	stream     *http.Client
}

// newClient 创建连接指定服务器的执行器客户端。
func newClient(serverURL, credential string) (*client, error) {
	baseURL, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid server url %q", serverURL)
	}
	return &client{baseURL: baseURL, credential: credential, http: &http.Client{Timeout: requestTimeout}, stream: &http.Client{}}, nil
}

// download 读取文件内容地址的响应体，服务端相对路径按服务器地址补全；关闭响应体即结束读取。
func (c *client) download(ctx context.Context, address string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve(address), nil)
	if err != nil {
		return nil, err
	}
	response, err := c.stream.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("download responded %d", response.StatusCode)
	}
	return response.Body, nil
}

// upload 按服务端给出的请求整体上传内容，服务端相对路径按服务器地址补全。
func (c *client) upload(ctx context.Context, target appservice.FileUploadRequest, body io.Reader, size int64) error {
	request, err := http.NewRequestWithContext(ctx, target.Method, c.resolve(target.URL), body)
	if err != nil {
		return err
	}
	request.ContentLength = size
	for name, value := range target.Headers {
		request.Header.Set(name, value)
	}
	response, err := c.stream.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("upload responded %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// resolve 把以 / 开头的服务端相对路径补全为服务器地址下的完整地址，完整地址原样返回。
func (c *client) resolve(address string) string {
	if !strings.HasPrefix(address, "/") {
		return address
	}
	return strings.TrimRight(c.baseURL.String(), "/") + address
}

// do 以普通请求时限发送 JSON 请求并解码 JSON 响应，output 为空时丢弃响应体。
func (c *client) do(ctx context.Context, method, path string, input, output any) error {
	return c.send(ctx, c.http, method, path, input, output)
}

// doUnbounded 不设请求时限发送 JSON 请求并解码 JSON 响应，用于服务端处理时间随内容大小增长的调用。
func (c *client) doUnbounded(ctx context.Context, method, path string, input, output any) error {
	return c.send(ctx, c.stream, method, path, input, output)
}

// send 以指定的 HTTP 客户端发送 JSON 请求并解码 JSON 响应，output 为空时丢弃响应体。
func (c *client) send(ctx context.Context, httpClient *http.Client, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode executor request: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	request, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := responseError(response); err != nil {
		return err
	}
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(output); err != nil {
		return fmt.Errorf("decode executor response: %w", err)
	}
	return nil
}

// request 构造携带电脑凭据、接口版本与执行器版本的请求，ctx 带有串联编号时以 traceparent 传给服务端，路径位于服务端 /api 之下。
func (c *client) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	endpoint := c.baseURL.Clone()
	endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + "/api" + path
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.credential)
	request.Header.Set("Accept", "application/json")
	request.Header.Set(appservice.ClientAPIVersionHeader, strconv.Itoa(appservice.APIVersion))
	request.Header.Set(appservice.ExecutorVersionHeader, domain.ExecutorVersion)
	if traceID := logscope.From(ctx).TraceID; traceID != "" {
		request.Header.Set(appservice.TraceparentHeader, logscope.Traceparent(traceID))
	}
	return request, nil
}

// responseError 把非 2xx 响应转换为错误，401 表示电脑凭据已失效，412 表示执行器版本与服务端不一致。
func responseError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrCredentialInvalid
	}
	if response.StatusCode == http.StatusPreconditionFailed {
		return ErrExecutorOutdated
	}
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	return &statusError{code: response.StatusCode, detail: strings.TrimSpace(string(detail))}
}

// statusError 是服务端返回的非 2xx 响应。
type statusError struct {
	code   int
	detail string
}

// Error 返回响应状态与响应正文。
func (e *statusError) Error() string {
	return fmt.Sprintf("server responded %d: %s", e.code, e.detail)
}
