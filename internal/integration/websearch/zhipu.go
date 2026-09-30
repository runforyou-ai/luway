package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// zhipuRecency 是智谱 Web Search 的时间范围取值。
var zhipuRecency = map[Recency]string{RecencyDay: "oneDay", RecencyWeek: "oneWeek", RecencyMonth: "oneMonth", RecencyYear: "oneYear"}

// searchZhipu 调用智谱 Web Search API 的基础版引擎。
func searchZhipu(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"search_query": request.Query, "search_engine": "search_std", "search_intent": false, "count": request.Count}
	if recency := zhipuRecency[request.Recency]; recency != "" {
		body["search_recency_filter"] = recency
	}
	var response struct {
		SearchResult []struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			Content     string `json:"content"`
			Media       string `json:"media"`
			PublishDate string `json:"publish_date"`
		} `json:"search_result"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderZhipu, "https://open.bigmodel.cn/api/paas/v4/web_search")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.SearchResult))
	for _, result := range response.SearchResult {
		items = append(items, Item{Title: result.Title, URL: result.Link, Snippet: result.Content,
			SiteName: result.Media, PublishedAt: result.PublishDate})
	}
	return items, nil
}
