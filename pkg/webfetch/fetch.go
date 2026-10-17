// Package webfetch 按地址读取公开网页并提取 Markdown 正文。
package webfetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/runforyou-ai/mdchunk/convert"
	"github.com/runforyou-ai/mdchunk/convert/html"
)

const (
	// maxResponseBytes 是单个页面允许读取的最大字节数。
	maxResponseBytes = 10 << 20
	// maxRedirects 是抓取过程中允许跟随的重定向次数。
	maxRedirects = 5
)

// 内置抓取支持的内容类型。
const (
	contentTypeHTML = "text/html"
	contentTypeText = "text/plain"
)

// htmlConverter 把网页 HTML 转换为 Markdown，原件大小由抓取响应的读取上限约束。
var htmlConverter = html.New(html.Options{Limits: convert.Limits{MaxBytes: -1}})

// Error 定义网页读取的语言无关失败原因码。
type Error struct {
	Code string `json:"code"`
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "web fetch: " + e.Code }

// Document 是转换为 Markdown 的网页正文。
type Document struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"`
}

// Fetcher 按地址读取网页并返回 Markdown 正文。
type Fetcher interface {
	Fetch(context.Context, string) (Document, error)
}

// Client 以内置 HTTP 抓取实现 Fetcher：不执行 JavaScript，HTML 经正文提取后转为 Markdown。
type Client struct {
	http      *http.Client
	userAgent string
}

// NewClient 创建以指定 User-Agent 发起请求的内置网页读取客户端。
func NewClient(userAgent string) *Client {
	return &Client{userAgent: userAgent, http: &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return nil
		},
	}}
}

// Normalize 校验并规范化页面地址，只接受 http 与 https 的绝对地址并去掉片段。
func Normalize(target string) (string, error) {
	parsed, err := parseTarget(target)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// parseTarget 解析页面地址并去掉片段。
func parseTarget(target string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return nil, &Error{Code: "url_invalid"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &Error{Code: "url_invalid"}
	}
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed, nil
}

// Fetch 读取 HTML 或纯文本页面并返回 Markdown 正文；正文为空时返回 content_empty。
func (c *Client) Fetch(ctx context.Context, target string) (Document, error) {
	parsed, err := parseTarget(target)
	if err != nil {
		return Document{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Document{}, &Error{Code: "url_unreachable"}
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Document{}, ctx.Err()
		}
		return Document{}, &Error{Code: "url_unreachable"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Document{}, &Error{Code: "url_unreachable"}
	}
	contentType, err := pageContentType(response.Header.Get("Content-Type"))
	if err != nil {
		return Document{}, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Document{}, &Error{Code: "url_unreachable"}
	}
	if len(body) > maxResponseBytes {
		return Document{}, &Error{Code: "url_content_too_large"}
	}
	body = decodeUTF8(body, response.Header.Get("Content-Type"))
	pageURL := response.Request.URL.String()
	title := ""
	if contentType == contentTypeHTML {
		body, title = extractArticle(body, response.Request.URL)
	}
	content := string(body)
	if contentType == contentTypeHTML {
		doc, err := htmlConverter.Convert(ctx, convert.Input{Reader: bytes.NewReader(body), Charset: "utf-8", BaseURL: pageURL})
		if err != nil {
			if ctx.Err() != nil {
				return Document{}, ctx.Err()
			}
			return Document{}, &Error{Code: "content_unreadable"}
		}
		content = doc.Markdown
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Document{}, &Error{Code: "content_empty"}
	}
	address, _ := Normalize(target)
	return Document{URL: address, Title: title, Content: content}, nil
}

// pageContentType 把响应内容类型归一为内置抓取支持的内容类型。
func pageContentType(contentType string) (string, error) {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", &Error{Code: "url_content_unsupported"}
	}
	switch media {
	case "text/html", "application/xhtml+xml":
		return contentTypeHTML, nil
	case "text/plain":
		return contentTypeText, nil
	default:
		return "", &Error{Code: "url_content_unsupported"}
	}
}
