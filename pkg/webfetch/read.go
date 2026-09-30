package webfetch

import (
	"context"
	"strings"

	"github.com/runforyou-ai/cervi/pkg/htmlmarkdown"
)

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
		if content, err = htmlmarkdown.Convert(ctx, content); err != nil {
			if ctx.Err() != nil {
				return Document{}, ctx.Err()
			}
			return Document{}, &Error{Code: "content_unreadable"}
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Document{}, &Error{Code: "content_empty"}
	}
	address, _ := Normalize(target)
	return Document{URL: address, Title: page.Title, Content: content}, nil
}
