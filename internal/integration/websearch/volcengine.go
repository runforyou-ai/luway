package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// volcengineTimeRange 是火山引擎联网搜索的时间范围取值。
var volcengineTimeRange = map[Recency]string{RecencyDay: "OneDay", RecencyWeek: "OneWeek", RecencyMonth: "OneMonth", RecencyYear: "OneYear"}

// searchVolcengine 调用火山引擎联网搜索 API Key 入口；业务错误在 ResponseMetadata.Error 中返回。
func searchVolcengine(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"Query": request.Query, "SearchType": "web", "Count": request.Count}
	if timeRange := volcengineTimeRange[request.Recency]; timeRange != "" {
		body["TimeRange"] = timeRange
	}
	var response struct {
		ResponseMetadata struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Result struct {
			WebResults []struct {
				Title       string `json:"Title"`
				URL         string `json:"Url"`
				Snippet     string `json:"Snippet"`
				SiteName    string `json:"SiteName"`
				PublishTime string `json:"PublishTime"`
			} `json:"WebResults"`
		} `json:"Result"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderVolcengine, "https://open.feedcoopapi.com/search_api/web_search")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	if failure := response.ResponseMetadata.Error; failure != nil && failure.Code != "" {
		// 认证失败与余额、配额不足使用对应的失败分类。
		kind := connectiontest.FailureProtocol
		switch failure.Code {
		case "10403", "invalid_api_key":
			kind = connectiontest.FailureUnauthorized
		case "10406", "10412", "429":
			kind = connectiontest.FailureRateLimited
		}
		return nil, errorAs(kind, "volcengine web search error %s: %s", failure.Code, failure.Message)
	}
	items := make([]Item, 0, len(response.Result.WebResults))
	for _, result := range response.Result.WebResults {
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: result.Snippet,
			SiteName: result.SiteName, PublishedAt: result.PublishTime})
	}
	return items, nil
}
