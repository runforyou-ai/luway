package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// searchSerpAPI 调用 SerpApi 的 Google 搜索引擎，凭据放在查询参数中。
func searchSerpAPI(ctx context.Context, c *Client, config Config, request Request) ([]Item, error) {
	query := url.Values{"engine": {"google"}, "q": {request.Query}, "num": {strconv.Itoa(request.Count)}, "api_key": {config.APIKey}}
	if filter := googleTimeFilter(request.Recency); filter != "" {
		query.Set("tbs", filter)
	}
	var response struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
			Source  string `json:"source"`
			Date    string `json:"date"`
		} `json:"organic_results"`
	}
	endpoint := c.endpoint(domain.WebSearchProviderSerpAPI, "https://serpapi.com/search")
	if err := c.getJSON(ctx, endpoint, query, http.Header{}, &response); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(response.OrganicResults))
	for _, result := range response.OrganicResults {
		items = append(items, Item{Title: result.Title, URL: result.Link, Snippet: result.Snippet,
			SiteName: result.Source, PublishedAt: result.Date})
	}
	return items, nil
}
