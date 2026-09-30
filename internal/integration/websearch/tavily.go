package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchTavily 调用 Tavily Search API。
func searchTavily(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"query": request.Query, "max_results": request.Count, "include_published_date": true}
	if request.Recency != "" {
		body["time_range"] = string(request.Recency)
	}
	var response struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderTavily, "https://api.tavily.com/search")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Results))
	for _, result := range response.Results {
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: result.Content,
			SiteName: hostname(result.URL), PublishedAt: result.PublishedDate})
	}
	return items, nil
}
