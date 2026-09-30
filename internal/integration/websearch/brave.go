package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// braveFreshness 是 Brave 的时间范围取值。
var braveFreshness = map[Recency]string{RecencyDay: "pd", RecencyWeek: "pw", RecencyMonth: "pm", RecencyYear: "py"}

// searchBrave 调用 Brave Search API；凭据无效时 Brave 返回 422 与 SUBSCRIPTION_TOKEN_INVALID，按认证失败处理。
func searchBrave(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	query := url.Values{"q": {request.Query}, "count": {strconv.Itoa(request.Count)}}
	if freshness := braveFreshness[request.Recency]; freshness != "" {
		query.Set("freshness", freshness)
	}
	var response struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				PageAge     string `json:"page_age"`
				Profile     struct {
					Name string `json:"name"`
				} `json:"profile"`
			} `json:"results"`
		} `json:"web"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderBrave, "https://api.search.brave.com/res/v1/web/search")
	err := c.getJSON(ctx, endpoint, query, http.Header{"X-Subscription-Token": {config.APIKey}}, &response)
	if connectionError, ok := errors.AsType[*connectiontest.Error](err); ok && strings.Contains(connectionError.Error(), "SUBSCRIPTION_TOKEN_INVALID") {
		return nil, connectiontest.NewError(connectiontest.StageAuthenticate, connectiontest.FailureUnauthorized, err)
	}
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.Web.Results))
	for _, result := range response.Web.Results {
		items = append(items, Item{Title: result.Title, URL: result.URL, Snippet: result.Description,
			SiteName: result.Profile.Name, PublishedAt: result.PageAge})
	}
	return items, nil
}
