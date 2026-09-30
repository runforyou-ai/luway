package websearch

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// aliyunTimeRange 是阿里云 IQS 的时间范围取值。
var aliyunTimeRange = map[Recency]string{RecencyDay: "OneDay", RecencyWeek: "OneWeek", RecencyMonth: "OneMonth", RecencyYear: "OneYear"}

// searchAliyunIQS 调用阿里云信息查询服务的统一搜索接口，使用标准版引擎。
func searchAliyunIQS(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"query": request.Query, "engineType": "Generic", "timeRange": "NoLimit"}
	if timeRange := aliyunTimeRange[request.Recency]; timeRange != "" {
		body["timeRange"] = timeRange
	}
	var response struct {
		PageItems []struct {
			Title         string `json:"title"`
			Link          string `json:"link"`
			Snippet       string `json:"snippet"`
			Hostname      string `json:"hostname"`
			PublishedTime string `json:"publishedTime"`
		} `json:"pageItems"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderAliyunIQS, "https://cloud-iqs.aliyuncs.com/search/unified")
	if err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.PageItems))
	for _, result := range response.PageItems {
		items = append(items, Item{Title: result.Title, URL: result.Link, Snippet: result.Snippet,
			SiteName: result.Hostname, PublishedAt: result.PublishedTime})
	}
	return items, nil
}
