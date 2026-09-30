package websearch

import (
	"context"
	"net/http"
	"net/url"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchSearXNG 调用企业自行部署的 SearXNG 实例，实例须在 search.formats 中开启 json。
func searchSearXNG(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	query := url.Values{"q": {request.Query}, "format": {"json"}}
	if request.Recency != "" {
		query.Set("time_range", string(request.Recency))
	}
	var response struct {
		Results []struct {
			Title         string  `json:"title"`
			URL           string  `json:"url"`
			Content       string  `json:"content"`
			PublishedDate *string `json:"publishedDate"`
		} `json:"results"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderSearXNG, config.BaseURL+"/search")
	if err := c.getJSON(ctx, endpoint, query, http.Header{}, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Results))
	for _, result := range response.Results {
		item := Item{Title: result.Title, URL: result.URL, Snippet: result.Content, SiteName: hostname(result.URL)}
		if result.PublishedDate != nil {
			item.PublishedAt = *result.PublishedDate
		}
		items = append(items, item)
	}
	return items, nil
}
