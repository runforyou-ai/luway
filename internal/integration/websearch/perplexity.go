package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// perplexitySnippetTokens 是每条结果从页面中提取的最大 token 数。
const perplexitySnippetTokens = 256

// searchPerplexity 调用 Perplexity Search API。
func searchPerplexity(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"query": request.Query, "max_results": request.Count, "max_tokens_per_page": perplexitySnippetTokens}
	if request.Recency != "" {
		body["search_recency_filter"] = string(request.Recency)
	}
	var response struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Date    string `json:"date"`
		} `json:"results"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderPerplexity, "https://api.perplexity.ai/search")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Results))
	for _, result := range response.Results {
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: result.Snippet,
			SiteName: hostname(result.URL), PublishedAt: result.Date})
	}
	return items, nil
}
