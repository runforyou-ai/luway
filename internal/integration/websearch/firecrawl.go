package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchFirecrawl 调用 Firecrawl Search v2，只取网页结果的摘要。
func searchFirecrawl(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"query": request.Query, "limit": request.Count}
	if filter := googleTimeFilter(request.Recency); filter != "" {
		body["tbs"] = filter
	}
	var response struct {
		Data struct {
			Web []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"web"`
		} `json:"data"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderFirecrawl, "https://api.firecrawl.dev/v2/search")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Data.Web))
	for _, result := range response.Data.Web {
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: result.Description, SiteName: hostname(result.URL)})
	}
	return items, nil
}
