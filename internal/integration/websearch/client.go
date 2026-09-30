package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

const (
	// maxSnippetRunes 是单条结果摘要保留的最大字符数。
	maxSnippetRunes = 500
	// maxErrorBodyBytes 是失败响应保留到错误中的最大字节数。
	maxErrorBodyBytes = 1024
)

// htmlTag 匹配摘要中服务商用于高亮关键词的 HTML 标签。
var htmlTag = regexp.MustCompile(`<[^>]+>`)

// Client 按企业配置调用搜索服务。
type Client struct {
	http      *http.Client
	endpoints map[domain.WebSearchProvider]string // 覆盖服务商的默认接口地址。
}

// NewClient 创建联网搜索客户端。
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second}}
}

// provider 调用一家搜索服务并返回整理前的结果。
type provider func(ctx context.Context, c *Client, config Config, request Request) ([]Item, error)

// providers 按服务商登记调用实现。
var providers = map[domain.WebSearchProvider]provider{
	domain.WebSearchProviderTavily:     searchTavily,
	domain.WebSearchProviderBrave:      searchBrave,
	domain.WebSearchProviderExa:        searchExa,
	domain.WebSearchProviderPerplexity: searchPerplexity,
	domain.WebSearchProviderSerper:     searchSerper,
	domain.WebSearchProviderSerpAPI:    searchSerpAPI,
	domain.WebSearchProviderJina:       searchJina,
	domain.WebSearchProviderFirecrawl:  searchFirecrawl,
	domain.WebSearchProviderBocha:      searchBocha,
	domain.WebSearchProviderAliyunIQS:  searchAliyunIQS,
	domain.WebSearchProviderBaidu:      searchBaidu,
	domain.WebSearchProviderVolcengine: searchVolcengine,
	domain.WebSearchProviderZhipu:      searchZhipu,
	domain.WebSearchProviderSearXNG:    searchSearXNG,
}

// Search 用配置的服务商执行搜索，结果按请求条数截断，摘要去掉 HTML 标签并限制长度；失败时返回 connectiontest 分类的错误。
func (c *Client) Search(ctx context.Context, config Config, request Request) (Result, error) {
	search, ok := providers[config.Provider]
	if !ok {
		return Result{}, connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureInvalidConfig,
			fmt.Errorf("unsupported web search provider %q", config.Provider))
	}
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return Result{}, connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureInvalidConfig, fmt.Errorf("query is required"))
	}
	if request.Count <= 0 {
		request.Count = DefaultCount
	}
	request.Count = min(request.Count, MaxCount)
	items, err := search(ctx, c, config, request)
	if err != nil {
		return Result{}, err
	}
	result := Result{Items: make([]Item, 0, min(len(items), request.Count)), Notice: recencyNotice(config.Provider, request.Recency)}
	for _, item := range items {
		if len(result.Items) == request.Count {
			break
		}
		item.Title = strings.TrimSpace(plainText(item.Title))
		item.URL = strings.TrimSpace(item.URL)
		if item.URL == "" {
			continue
		}
		item.Snippet = truncateRunes(strings.TrimSpace(plainText(item.Snippet)), maxSnippetRunes)
		item.SiteName = strings.TrimSpace(item.SiteName)
		item.PublishedAt = strings.TrimSpace(item.PublishedAt)
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// recencyNotice 返回服务商无法按请求的时间范围筛选时的说明：Jina 不支持时间范围，百度最短按一周筛选。
func recencyNotice(provider domain.WebSearchProvider, recency Recency) string {
	switch {
	case recency == "":
		return ""
	case provider == domain.WebSearchProviderJina:
		return "该搜索服务不支持按发布时间筛选，结果没有按时间范围过滤。"
	case provider == domain.WebSearchProviderBaidu && recency == RecencyDay:
		return "该搜索服务最短只能按一周筛选，结果是最近一周内发布的内容。"
	}
	return ""
}

// endpoint 返回服务商的接口地址，客户端登记了覆盖地址时使用覆盖地址。
func (c *Client) endpoint(provider domain.WebSearchProvider, fallback string) string {
	if override, ok := c.endpoints[provider]; ok {
		return override
	}
	return fallback
}

// postJSON 以 JSON 请求体调用搜索服务并解码 JSON 响应。
func (c *Client) postJSON(ctx context.Context, endpoint string, header http.Header, body any, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode web search request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureInvalidConfig, err)
	}
	request.Header = header.Clone()
	request.Header.Set("Content-Type", "application/json")
	return c.do(request, out)
}

// getJSON 以查询参数调用搜索服务并解码 JSON 响应。
func (c *Client) getJSON(ctx context.Context, endpoint string, query url.Values, header http.Header, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureInvalidConfig, err)
	}
	request.URL.RawQuery = query.Encode()
	request.Header = header.Clone()
	return c.do(request, out)
}

// do 发送请求并解码成功响应；失败状态按认证、权限、额度与服务不可用分类，402 及服务商自定义的额度状态归为额度超限。
func (c *Client) do(request *http.Request, out any) error {
	request.Header.Set("Accept", "application/json")
	startedAt := time.Now()
	response, err := c.http.Do(request)
	if err != nil {
		if request.Context().Err() != nil {
			return request.Context().Err()
		}
		// 错误中的请求地址去掉查询参数，查询参数可能携带凭据。
		if urlError, ok := errors.AsType[*url.Error](err); ok {
			urlError.URL = request.URL.Scheme + "://" + request.URL.Host + request.URL.Path
		}
		return connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, err)
	}
	slog.Info("联网搜索请求", "host", request.URL.Host, "path", request.URL.Path,
		"status_code", response.StatusCode, "duration_ms", time.Since(startedAt).Milliseconds())
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		detail := truncateRunes(strings.TrimSpace(string(body)), maxErrorBodyBytes)
		switch response.StatusCode {
		case http.StatusPaymentRequired, 432, 433:
			return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureRateLimited,
				fmt.Errorf("unexpected HTTP status %d: %s", response.StatusCode, detail))
		}
		return connectiontest.HTTPStatusError(response.StatusCode, detail)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, fmt.Errorf("decode web search response: %w", err))
	}
	return nil
}

// bearer 返回带 Bearer 凭据的请求头。
func bearer(apiKey string) http.Header {
	return http.Header{"Authorization": {"Bearer " + apiKey}}
}

// hostname 返回网址的主机名，解析失败时为空。
func hostname(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// plainText 去掉 HTML 标签并还原字符实体。
func plainText(text string) string {
	return html.UnescapeString(htmlTag.ReplaceAllString(text, ""))
}

// truncateRunes 按字符数截断文本，截断时以省略号结尾。
func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

// recencyDuration 返回时间范围对应的时长，不限时间时为零。
func recencyDuration(recency Recency) time.Duration {
	switch recency {
	case RecencyDay:
		return 24 * time.Hour
	case RecencyWeek:
		return 7 * 24 * time.Hour
	case RecencyMonth:
		return 31 * 24 * time.Hour
	case RecencyYear:
		return 365 * 24 * time.Hour
	}
	return 0
}

// googleTimeFilter 返回 Google 语法的时间范围参数，不限时间时为空。
func googleTimeFilter(recency Recency) string {
	switch recency {
	case RecencyDay:
		return "qdr:d"
	case RecencyWeek:
		return "qdr:w"
	case RecencyMonth:
		return "qdr:m"
	case RecencyYear:
		return "qdr:y"
	}
	return ""
}

// errorAs 构造服务商在成功状态中返回的业务错误，按失败原因分类。
func errorAs(kind connectiontest.FailureKind, format string, args ...any) error {
	stage := connectiontest.StageCapability
	switch kind {
	case connectiontest.FailureUnauthorized:
		stage = connectiontest.StageAuthenticate
	case connectiontest.FailureForbidden:
		stage = connectiontest.StageAuthorize
	}
	return connectiontest.NewError(stage, kind, fmt.Errorf(format, args...))
}
