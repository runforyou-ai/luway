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
)

const (
	// requestTimeout 是单次服务端请求的时限。
	requestTimeout = 30 * time.Second
	// maxResponseBytes 是服务端响应体的读取上限。
	maxResponseBytes = 16 << 20
)

// ErrCredentialInvalid 表示服务端不再接受电脑凭据：电脑已撤销、主人已不是工作区的有效成员或凭据已更换。
var ErrCredentialInvalid = errors.New("computer credential invalid")

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

// reportCapabilities 上报执行能力。
func (c *client) reportCapabilities(ctx context.Context, input appservice.ComputerCapabilitiesInput) error {
	return c.do(ctx, http.MethodPut, "/computer/capabilities", input, nil)
}

// claim 带上本机仍在执行的操作，领取最多 limit 个待执行操作，并取回应当中止的操作。
func (c *client) claim(ctx context.Context, limit int, running []string) (appservice.ComputerOperationList, error) {
	var output appservice.ComputerOperationList
	if err := c.do(ctx, http.MethodPost, "/computer/operations/claim", appservice.ComputerClaimInput{Limit: limit, Running: running}, &output); err != nil {
		return appservice.ComputerOperationList{}, err
	}
	return output, nil
}

// complete 上报一次操作的结果。
func (c *client) complete(ctx context.Context, operationID string, input appservice.ComputerOutcomeInput) error {
	return c.do(ctx, http.MethodPost, "/computer/operations/"+url.PathEscape(operationID)+"/result", input, nil)
}

// openStream 建立执行器事件流，返回响应体，关闭响应体即结束事件流。
func (c *client) openStream(ctx context.Context) (io.ReadCloser, error) {
	request, err := c.request(ctx, http.MethodGet, "/realtime/computer", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.stream.Do(request)
	if err != nil {
		return nil, err
	}
	if err := responseError(response); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response.Body, nil
}

// do 发送 JSON 请求并解码 JSON 响应，output 为空时丢弃响应体。
func (c *client) do(ctx context.Context, method, path string, input, output any) error {
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
	response, err := c.http.Do(request)
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

// request 构造携带电脑凭据与接口版本的请求，路径位于服务端 /api 之下。
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
	return request, nil
}

// responseError 把非 2xx 响应转换为错误，401 表示电脑凭据已失效。
func responseError(response *http.Response) error {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	if response.StatusCode == http.StatusUnauthorized {
		return ErrCredentialInvalid
	}
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	return fmt.Errorf("server responded %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
}
