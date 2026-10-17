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

const firecrawlScrapePath = "/v2/scrape"

// fetchFirecrawl 调用 Firecrawl Scrape v2，返回页面 Markdown 正文。
func fetchFirecrawl(ctx context.Context, c *Client, config Config, target string) (pkgwebfetch.Document, error) {
	address, err := pkgwebfetch.Normalize(target)
	if err != nil {
		return pkgwebfetch.Document{}, err
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
			Metadata struct {
				Title string `json:"title"`
			} `json:"metadata"`
		} `json:"data"`
		Error string `json:"error"`
	}
	err = c.doJSON(ctx, httpjson.Request{
		Method: http.MethodPost,
		URL:    firecrawlScrapeURL(config),
		Header: bearer(config.APIKey),
		JSON:   map[string]any{"url": address, "formats": []string{"markdown"}},
	}, &response)
	if err != nil {
		return pkgwebfetch.Document{}, err
	}
	if !response.Success {
		message := response.Error
		if message == "" {
			message = "firecrawl scrape failed"
		}
		return pkgwebfetch.Document{}, connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, fmt.Errorf("%s", message))
	}
	return documentFromMarkdown(address, response.Data.Metadata.Title, response.Data.Markdown)
}

// firecrawlScrapeURL 返回 Firecrawl 抓取接口：官方默认完整地址，自建实例在根地址后追加 /v2/scrape。
func firecrawlScrapeURL(config Config) string {
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if base == "" {
		return "https://api.firecrawl.dev" + firecrawlScrapePath
	}
	if strings.HasSuffix(base, firecrawlScrapePath) {
		return base
	}
	return base + firecrawlScrapePath
}
