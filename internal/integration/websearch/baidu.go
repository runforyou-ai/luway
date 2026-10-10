package websearch

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// baiduRecency 是百度千帆 AI 搜索的时间范围取值，最小粒度为一周，放宽时由 recencyNotice 说明。
var baiduRecency = map[Recency]string{RecencyDay: "week", RecencyWeek: "week", RecencyMonth: "month", RecencyYear: "year"}

// searchBaidu 调用百度千帆 AI 搜索的网页搜索接口；业务错误在响应体的 code 与 message 中返回。
func searchBaidu(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{
		"messages":             []map[string]string{{"role": "user", "content": request.Query}},
		"search_source":        "baidu_search_v2",
		"resource_type_filter": []map[string]any{{"type": "web", "top_k": request.Count}},
	}
	if recency := baiduRecency[request.Recency]; recency != "" {
		body["search_recency_filter"] = recency
	}
	var response struct {
		Code       any    `json:"code"`
		Message    string `json:"message"`
		References []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
			Content string `json:"content"`
			Website string `json:"website"`
			Date    string `json:"date"`
		} `json:"references"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderBaidu, "https://qianfan.baidubce.com/v2/ai_search/web_search")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	if response.Code != nil && response.References == nil {
		return nil, errorAs(connectiontest.FailureProtocol, "baidu web search error %v: %s", response.Code, response.Message)
	}
	items := make([]Item, 0, len(response.References))
	for _, result := range response.References {
		// 摘要为空时以正文开头作为摘要。
		snippet := result.Snippet
		if snippet == "" {
			snippet = result.Content
		}
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: snippet,
			SiteName: result.Website, PublishedAt: result.Date})
	}
	return items, nil
}
