// Package webfetch 按地址抓取公开网页并提取正文。
package webfetch

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxResponseBytes 是单个页面允许读取的最大字节数。
	maxResponseBytes = 10 << 20
	// maxRedirects 是抓取过程中允许跟随的重定向次数。
	maxRedirects = 5
)

// Page 支持的内容类型。
const (
	ContentTypeHTML = "text/html"
	ContentTypeText = "text/plain"
)

// Error 定义网页抓取的语言无关失败原因码。
type Error struct {
	Code string `json:"code"`
}

// Error 返回语言无关的失败原因。
func (e *Error) Error() string { return "web fetch: " + e.Code }

// Page 返回页面内容类型、UTF-8 内容与正文标题；HTML 页面的内容是提取出的正文文档。
type Page struct {
	ContentType string
	Body        []byte
	Title       string // 从 HTML 正文中提取的标题，纯文本或提取不到正文时为空。
}

// Client 抓取单个公开网页。
type Client struct {
	http      *http.Client
	userAgent string
}

// NewClient 创建以指定 User-Agent 发起请求的网页抓取客户端。
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

// Fetch 读取 HTML 或纯文本页面，HTML 页面提取正文与标题。
func (c *Client) Fetch(ctx context.Context, target string) (Page, error) {
	parsed, err := parseTarget(target)
	if err != nil {
		return Page{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Page{}, &Error{Code: "url_unreachable"}
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Page{}, ctx.Err()
		}
		return Page{}, &Error{Code: "url_unreachable"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return Page{}, &Error{Code: "url_unreachable"}
	}
	contentType, err := pageContentType(response.Header.Get("Content-Type"))
	if err != nil {
		return Page{}, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Page{}, &Error{Code: "url_unreachable"}
	}
	if len(body) > maxResponseBytes {
		return Page{}, &Error{Code: "url_content_too_large"}
	}
	body = decodeUTF8(body, response.Header.Get("Content-Type"))
	page := Page{ContentType: contentType, Body: body}
	if contentType == ContentTypeHTML {
		page.Body, page.Title = extractArticle(body, response.Request.URL)
	}
	return page, nil
}

// pageContentType 把响应内容类型归一为 Page 支持的内容类型。
func pageContentType(contentType string) (string, error) {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", &Error{Code: "url_content_unsupported"}
	}
	switch media {
	case "text/html", "application/xhtml+xml":
		return ContentTypeHTML, nil
	case "text/plain":
		return ContentTypeText, nil
	default:
		return "", &Error{Code: "url_content_unsupported"}
	}
}
