// Package webfetch 按企业配置调用网页读取服务，把各服务商结果整理为统一的 Markdown 文档。
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/luway/pkg/httpjson"
	pkgwebfetch "github.com/runforyou-ai/luway/pkg/webfetch"
)

const (
	// maxResponseBytes 是单个读取响应允许的最大字节数。
	maxResponseBytes = 10 << 20
	// testPageURL 是连接测试使用的固定公开页面。
	testPageURL = "https://example.com/"
)

// Config 定义调用网页读取服务所需的服务商与凭据。
type Config struct {
	Provider domain.WebFetchProvider
	APIKey   string
	BaseURL  string // 自建服务的实例地址，官方服务为空。
}

// Client 按企业配置调用网页读取服务。
type Client struct {
	http   *http.Client
	direct *pkgwebfetch.Client
}

// NewClient 创建网页读取客户端。
func NewClient() *Client {
	return &Client{
		http:   &http.Client{Timeout: 60 * time.Second},
		direct: pkgwebfetch.NewClient(common.WebFetchUserAgent()),
	}
}

// provider 调用一家读取服务并返回 Markdown 文档。
type provider func(ctx context.Context, c *Client, config Config, target string) (pkgwebfetch.Document, error)

// providers 按服务商登记调用实现。
var providers = map[domain.WebFetchProvider]provider{
	domain.WebFetchProviderDirect:    fetchDirect,
	domain.WebFetchProviderJina:      fetchJina,
	domain.WebFetchProviderFirecrawl: fetchFirecrawl,
}

// Fetch 用配置的服务商读取页面，返回 Markdown 正文；失败时返回 webfetch.Error 或 connectiontest 分类错误。
func (c *Client) Fetch(ctx context.Context, config Config, target string) (pkgwebfetch.Document, error) {
	fetch, ok := providers[config.Provider]
	if !ok {
		return pkgwebfetch.Document{}, connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureInvalidConfig,
			fmt.Errorf("unsupported web fetch provider %q", config.Provider))
	}
	return fetch(ctx, c, config, target)
}

// Probe 用固定公开页验证读取服务可用。
func (c *Client) Probe(ctx context.Context, config Config) error {
	doc, err := c.Fetch(ctx, config, testPageURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(doc.Content) == "" {
		return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, errors.New("empty web fetch content"))
	}
	return nil
}

// fetchDirect 使用内置直连抓取。
func fetchDirect(ctx context.Context, c *Client, _ Config, target string) (pkgwebfetch.Document, error) {
	return c.direct.Fetch(ctx, target)
}

// endpoint 返回服务商接口地址：配置了实例地址时优先使用，否则用官方默认地址。
func endpoint(config Config, fallback string) string {
	if base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"); base != "" {
		return base
	}
	return fallback
}

// bearer 返回带 Bearer 凭据的请求头。
func bearer(apiKey string) http.Header {
	return http.Header{"Authorization": {"Bearer " + apiKey}}
}

// doJSON 发送请求并解码成功 JSON；失败状态按认证、权限、额度与服务不可用分类。
func (c *Client) doJSON(ctx context.Context, input httpjson.Request, out any) error {
	input.MaxResponseBytes = maxResponseBytes
	startedAt := time.Now()
	response, err := httpjson.Do(ctx, c.http, input)
	switch httpjson.Classify(err) {
	case httpjson.KindNone:
	case httpjson.KindCanceled:
		return err
	case httpjson.KindTimeout, httpjson.KindTransport:
		return connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	case httpjson.KindTooLarge:
		return &pkgwebfetch.Error{Code: "url_content_too_large"}
	default:
		return connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureInvalidConfig, err)
	}
	slog.DebugContext(ctx, "网页读取请求", "url", httpjson.RedactURL(input.URL),
		"status_code", response.StatusCode, "duration_ms", time.Since(startedAt).Milliseconds())
	if !response.OK() {
		status := httpjson.NewStatusError(response.StatusCode, response.Body)
		switch response.StatusCode {
		case http.StatusPaymentRequired, 432, 433:
			return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureRateLimited, status)
		}
		return connectiontest.HTTPStatusError(response.StatusCode, status.Body)
	}
	if err := httpjson.Decode(response.Body, out); err != nil {
		return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, fmt.Errorf("decode web fetch response: %w", err))
	}
	return nil
}

// documentFromMarkdown 把服务商返回的 Markdown 整理为文档；正文为空时返回 content_empty。
func documentFromMarkdown(target, title, content string) (pkgwebfetch.Document, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return pkgwebfetch.Document{}, &pkgwebfetch.Error{Code: "content_empty"}
	}
	address, err := pkgwebfetch.Normalize(target)
	if err != nil {
		return pkgwebfetch.Document{}, err
	}
	return pkgwebfetch.Document{URL: address, Title: strings.TrimSpace(title), Content: content}, nil
}
