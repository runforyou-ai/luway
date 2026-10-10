package wechat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/pkg/httpjson"
)

const (
	// defaultBaseURL 是微信接口的默认基础地址。
	defaultBaseURL = "https://api.weixin.qq.com"
	// maxResponseSize 是接口响应体的读取上限。
	maxResponseSize = 1 << 20
)

// ErrUnavailable 表示无法连接微信接口或微信返回了不符合约定的响应。
var ErrUnavailable = errors.New("wechat unavailable")

// requestIPPattern 从白名单错误信息中取出发起调用的 IP。
var requestIPPattern = regexp.MustCompile(`(?i)(?:invalid ip|requestIP:?)\s*([0-9a-f:.]+[0-9a-f])`)

// APIError 定义微信接口返回的业务错误码与说明。
type APIError struct {
	Code    int
	Message string
}

// Error 返回错误码与说明。
func (e *APIError) Error() string {
	return fmt.Sprintf("wechat api error %d: %s", e.Code, e.Message)
}

// IPNotWhitelisted 判断错误是否为调用方 IP 不在白名单。
func (e *APIError) IPNotWhitelisted() bool {
	return e.Code == 40164 || e.Code == 61004
}

// TokenInvalid 判断错误是否为接口调用凭据无效或已过期。
func (e *APIError) TokenInvalid() bool {
	return e.Code == 40001 || e.Code == 40014 || e.Code == 42001
}

// RequestIP 返回白名单错误信息中微信识别到的调用方 IP，无法识别时为空。
func (e *APIError) RequestIP() string {
	if match := requestIPPattern.FindStringSubmatch(e.Message); match != nil {
		return match[1]
	}
	return ""
}

// AccessToken 定义接口调用凭据与有效时长。
type AccessToken struct {
	Value     string
	ExpiresIn time.Duration
}

// Client 调用微信开放平台与公众号接口。
type Client struct {
	http *http.Client
	// media 用于下载素材内容，超时由调用方的 context 控制。
	media   *http.Client
	baseURL string
}

// Option 配置微信客户端。
type Option func(*Client)

// WithBaseURL 覆盖微信接口基础地址，供受控环境和测试使用。
func WithBaseURL(baseURL string) Option {
	return func(client *Client) {
		client.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// WithDownloadClient 指定下载素材内容使用的 HTTP 客户端，未指定时与接口调用共用同一客户端。
func WithDownloadClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.media = httpClient
	}
}

// NewClient 创建微信客户端。
func NewClient(httpClient *http.Client, options ...Option) *Client {
	client := &Client{http: httpClient, media: httpClient, baseURL: defaultBaseURL}
	for _, option := range options {
		option(client)
	}
	return client
}

// ComponentAccessToken 用第三方平台凭据与验证票据获取平台接口调用凭据。
func (c *Client) ComponentAccessToken(ctx context.Context, appID, appSecret, verifyTicket string) (AccessToken, error) {
	var response struct {
		Token     string `json:"component_access_token"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := c.post(ctx, "/cgi-bin/component/api_component_token", map[string]string{
		"component_appid": appID, "component_appsecret": appSecret, "component_verify_ticket": verifyTicket,
	}, &response); err != nil {
		return AccessToken{}, err
	}
	if response.Token == "" || response.ExpiresIn <= 0 {
		return AccessToken{}, fmt.Errorf("%w: component access token missing", ErrUnavailable)
	}
	return AccessToken{Value: response.Token, ExpiresIn: time.Duration(response.ExpiresIn) * time.Second}, nil
}

// StableToken 用公众号 AppID 与 AppSecret 以普通模式获取稳定版接口调用凭据。
func (c *Client) StableToken(ctx context.Context, appID, appSecret string) (AccessToken, error) {
	var response struct {
		Token     string `json:"access_token"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if err := c.post(ctx, "/cgi-bin/stable_token", map[string]any{
		"grant_type": "client_credential", "appid": appID, "secret": appSecret, "force_refresh": false,
	}, &response); err != nil {
		return AccessToken{}, err
	}
	if response.Token == "" || response.ExpiresIn <= 0 {
		return AccessToken{}, fmt.Errorf("%w: access token missing", ErrUnavailable)
	}
	return AccessToken{Value: response.Token, ExpiresIn: time.Duration(response.ExpiresIn) * time.Second}, nil
}

// post 以 JSON 请求微信接口，返回业务错误码时给出 APIError，传输或格式错误时给出 ErrUnavailable。
func (c *Client) post(ctx context.Context, path string, payload any, output any) error {
	return call(ctx, c.http, httpjson.Request{Method: http.MethodPost, URL: c.baseURL + path, JSON: payload}, output)
}

// call 发送一次微信接口请求并解码响应，返回业务错误码时给出 APIError，传输或格式错误时给出 ErrUnavailable；请求地址中的凭据不进入错误。
func call(ctx context.Context, client httpjson.Doer, input httpjson.Request, output any) error {
	input.MaxResponseBytes = maxResponseSize
	response, err := httpjson.Do(ctx, client, input)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: http status %d", ErrUnavailable, response.StatusCode)
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := httpjson.Decode(response.Body, &result); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if result.ErrCode != 0 {
		return &APIError{Code: result.ErrCode, Message: result.ErrMsg}
	}
	if err := httpjson.Decode(response.Body, output); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}
