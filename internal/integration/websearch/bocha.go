package websearch

import (
	"context"
	"errors"
	"strings"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// bochaFreshness 是博查的时间范围取值。
var bochaFreshness = map[Recency]string{RecencyDay: "oneDay", RecencyWeek: "oneWeek", RecencyMonth: "oneMonth", RecencyYear: "oneYear"}

// searchBocha 调用博查 Web Search API；余额或套餐额度不足时博查返回 403，按额度超限处理。
func searchBocha(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	body := map[string]any{"query": request.Query, "count": request.Count}
	if freshness := bochaFreshness[request.Recency]; freshness != "" {
		body["freshness"] = freshness
	}
	var response struct {
		Data struct {
			WebPages struct {
				Value []struct {
					Name          string `json:"name"`
					URL           string `json:"url"`
					Snippet       string `json:"snippet"`
					SiteName      string `json:"siteName"`
					DatePublished string `json:"datePublished"`
				} `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderBocha, "https://api.bochaai.com/v1/web-search")
	err := c.postJSON(ctx, endpoint, bearer(config.APIKey), body, &response)
	if connectionError, ok := errors.AsType[*connectiontest.Error](err); ok && strings.Contains(connectionError.Error(), "quota") {
		return nil, connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureRateLimited, err)
	}
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Data.WebPages.Value))
	for _, result := range response.Data.WebPages.Value {
		items = append(items, Item{Title: result.Name, URL: result.URL, Snippet: result.Snippet,
			SiteName: result.SiteName, PublishedAt: result.DatePublished})
	}
	return items, nil
}
