package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchJina 调用 Jina Search API，只取摘要不抓取网页正文；Jina 不支持时间范围，由 recencyNotice 说明。
func searchJina(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	header := bearer(config.APIKey)
	header.Set("X-Respond-With", "no-content")
	var response struct {
		Data []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Date        string `json:"date"`
		} `json:"data"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderJina, "https://s.jina.ai/")
	if err := c.postJSON(ctx, endpoint, header, map[string]any{"q": request.Query, "num": request.Count}, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Data))
	for _, result := range response.Data {
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: result.Description,
			SiteName: hostname(result.URL), PublishedAt: result.Date})
	}
	return items, nil
}
