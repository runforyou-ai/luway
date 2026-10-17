package webfetch

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/luway/pkg/httpjson"
	pkgwebfetch "github.com/runforyou-ai/luway/pkg/webfetch"
)

// fetchJina 调用 Jina Reader，返回页面 Markdown 正文。
func fetchJina(ctx context.Context, c *Client, config Config, target string) (pkgwebfetch.Document, error) {
	address, err := pkgwebfetch.Normalize(target)
	if err != nil {
		return pkgwebfetch.Document{}, err
	}
	base := endpoint(config, "https://r.jina.ai")
	requestURL := strings.TrimRight(base, "/") + "/" + address
	header := bearer(config.APIKey)
	header.Set("Accept", "application/json")
	var response struct {
		Code int `json:"code"`
		Data struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, httpjson.Request{URL: requestURL, Header: header}, &response); err != nil {
		return pkgwebfetch.Document{}, err
	}
	if response.Code != 0 && response.Code != http.StatusOK {
		return pkgwebfetch.Document{}, connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol,
			fmt.Errorf("jina reader status %d", response.Code))
	}
	return documentFromMarkdown(address, response.Data.Title, response.Data.Content)
}
