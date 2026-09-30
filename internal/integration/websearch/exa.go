package websearch

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchExa 调用 Exa Search API，摘要取与查询最相关的句子。
func searchExa(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"query": request.Query, "numResults": request.Count, "type": "auto", "contents": map[string]any{"highlights": true}}
	if duration := recencyDuration(request.Recency); duration > 0 {
		body["startPublishedDate"] = time.Now().Add(-duration).UTC().Format(time.RFC3339)
	}
	var response struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			PublishedDate string   `json:"publishedDate"`
			Highlights    []string `json:"highlights"`
			Text          string   `json:"text"`
		} `json:"results"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderExa, "https://api.exa.ai/search")
	if err := c.postJSON(ctx, endpoint, http.Header{"X-Api-Key": {config.APIKey}}, body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Results))
	for _, result := range response.Results {
		// 没有相关句子时以正文开头作为摘要。
		snippet := strings.Join(result.Highlights, " … ")
		if snippet == "" {
			snippet = result.Text
		}
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: snippet,
			SiteName: hostname(result.URL), PublishedAt: result.PublishedDate})
	}
	return items, nil
}
