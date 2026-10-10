package webfetch

import (
	"bytes"
	"context"
	"strings"

	"github.com/runforyou-ai/mdchunk/convert"
	"github.com/runforyou-ai/mdchunk/convert/html"
)

// htmlConverter 把网页 HTML 转换为 Markdown，原件大小由抓取响应的读取上限约束。
var htmlConverter = html.New(html.Options{Limits: convert.Limits{MaxBytes: -1}})

// Document 是转换为 Markdown 的网页正文。
type Document struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content"`
}

// Read 抓取网页并把正文转换为 Markdown；正文为空时返回 content_empty。
func (c *Client) Read(ctx context.Context, target string) (Document, error) {
	page, err := c.Fetch(ctx, target)
	if err != nil {
		return Document{}, err
	}
	content := string(page.Body)
	if page.ContentType == ContentTypeHTML {
		// 抓取结果已转为 UTF-8，相对链接按跟随重定向后的地址解析。
		doc, err := htmlConverter.Convert(ctx, convert.Input{Reader: bytes.NewReader(page.Body), Charset: "utf-8", BaseURL: page.URL})
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
	return Document{URL: address, Title: page.Title, Content: content}, nil
}
