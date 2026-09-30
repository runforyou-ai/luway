package websearch

import (
	"context"
	"net/http"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchSerper 调用 Serper 的 Google 搜索接口。
func searchSerper(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"q": request.Query, "num": request.Count}
	if filter := googleTimeFilter(request.Recency); filter != "" {
		body["tbs"] = filter
	}
	var response struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
			Date    string `json:"date"`
		} `json:"organic"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderSerper, "https://google.serper.dev/search")
	if err := c.postJSON(ctx, endpoint, http.Header{"X-Api-Key": {config.APIKey}}, body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Organic))
	for _, result := range response.Organic {
		items = append(items, Item{Title: result.Title, URL: result.Link, Snippet: result.Snippet,
			SiteName: hostname(result.Link), PublishedAt: result.Date})
	}
	return items, nil
}
