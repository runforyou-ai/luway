package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// captured 记录模拟服务收到的请求。
type captured struct {
	method string
	header http.Header
	query  string
	body   map[string]any
}

// newTestClient 创建把指定服务商指向模拟服务的客户端，模拟服务记录请求并返回固定响应。
func newTestClient(t *testing.T, provider domain.WebSearchProvider, status int, response string) (*Client, *captured) {
	t.Helper()
	seen := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.header, seen.query = r.Method, r.Header.Clone(), r.URL.RawQuery
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			_ = json.Unmarshal(data, &seen.body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return &Client{http: server.Client(), endpoints: map[domain.WebSearchProvider]string{provider: server.URL}}, seen
}

// TestSearchProviders 验证各服务商的请求凭据、时间范围参数与结果字段映射。
func TestSearchProviders(t *testing.T) {
	cases := []struct {
		provider domain.WebSearchProvider
		response string
		check    func(*captured) bool
		want     Item
	}{
		{domain.WebSearchProviderTavily,
			`{"results":[{"title":"T","url":"https://a.example/x","content":"S","published_date":"2026-01-01"}]}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["time_range"] == "week"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "a.example", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderBrave,
			`{"web":{"results":[{"title":"T","url":"https://a.example/x","description":"<strong>S</strong> &amp; more","page_age":"2026-01-01","profile":{"name":"A"}}]}}`,
			func(c *captured) bool {
				return c.method == http.MethodGet && c.header.Get("X-Subscription-Token") == "key" && strings.Contains(c.query, "freshness=pw")
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S & more", SiteName: "A", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderExa,
			`{"results":[{"title":"T","url":"https://a.example/x","publishedDate":"2026-01-01","highlights":["S1","S2"]}]}`,
			func(c *captured) bool {
				return c.header.Get("X-Api-Key") == "key" && c.body["startPublishedDate"] != nil
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S1 … S2", SiteName: "a.example", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderPerplexity,
			`{"results":[{"title":"T","url":"https://a.example/x","snippet":"S","date":"2026-01-01"}]}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["search_recency_filter"] == "week"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "a.example", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderSerper,
			`{"organic":[{"title":"T","link":"https://a.example/x","snippet":"S","date":"Jan 1, 2026"}]}`,
			func(c *captured) bool { return c.header.Get("X-Api-Key") == "key" && c.body["tbs"] == "qdr:w" },
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "a.example", PublishedAt: "Jan 1, 2026"}},
		{domain.WebSearchProviderSerpAPI,
			`{"organic_results":[{"title":"T","link":"https://a.example/x","snippet":"S","source":"A"}]}`,
			func(c *captured) bool {
				return strings.Contains(c.query, "api_key=key") && strings.Contains(c.query, "engine=google") && strings.Contains(c.query, "tbs=qdr%3Aw")
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "A"}},
		{domain.WebSearchProviderJina,
			`{"code":200,"data":[{"title":"T","url":"https://a.example/x","description":"S"}]}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.header.Get("X-Respond-With") == "no-content"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "a.example"}},
		{domain.WebSearchProviderFirecrawl,
			`{"success":true,"data":{"web":[{"title":"T","url":"https://a.example/x","description":"S"}]}}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["tbs"] == "qdr:w"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "a.example"}},
		{domain.WebSearchProviderBocha,
			`{"code":200,"data":{"webPages":{"value":[{"name":"T","url":"https://a.example/x","snippet":"S","siteName":"A","datePublished":"2026-01-01"}]}}}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["freshness"] == "oneWeek"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "A", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderAliyunIQS,
			`{"pageItems":[{"title":"T","link":"https://a.example/x","snippet":"S","hostname":"A","publishedTime":"2026-01-01"}]}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["timeRange"] == "OneWeek"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "A", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderBaidu,
			`{"request_id":"r","references":[{"title":"T","url":"https://a.example/x","content":"S","website":"A","date":"2026-01-01"}]}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["search_recency_filter"] == "week"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "A", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderVolcengine,
			`{"ResponseMetadata":{},"Result":{"WebResults":[{"Title":"T","Url":"https://a.example/x","Snippet":"S","SiteName":"A","PublishTime":"2026-01-01"}]}}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["TimeRange"] == "OneWeek"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "A", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderZhipu,
			`{"search_result":[{"title":"T","link":"https://a.example/x","content":"S","media":"A","publish_date":"2026-01-01"}]}`,
			func(c *captured) bool {
				return c.header.Get("Authorization") == "Bearer key" && c.body["search_recency_filter"] == "oneWeek"
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "A", PublishedAt: "2026-01-01"}},
		{domain.WebSearchProviderSearXNG,
			`{"results":[{"title":"T","url":"https://a.example/x","content":"S","publishedDate":null}]}`,
			func(c *captured) bool {
				return strings.Contains(c.query, "format=json") && strings.Contains(c.query, "time_range=week")
			},
			Item{Title: "T", URL: "https://a.example/x", Snippet: "S", SiteName: "a.example"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			client, seen := newTestClient(t, tc.provider, http.StatusOK, tc.response)
			result, err := client.Search(context.Background(), Config{Provider: tc.provider, APIKey: "key"}, Request{Query: "q", Recency: RecencyWeek})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(result.Items) != 1 || result.Items[0] != tc.want {
				t.Fatalf("items=%+v", result.Items)
			}
			if !tc.check(seen) {
				t.Fatalf("request method=%s query=%s header=%v body=%v", seen.method, seen.query, seen.header, seen.body)
			}
		})
	}
}

// TestSearchLimitsAndTruncates 验证结果按条数截断、跳过无链接结果并限制摘要长度。
func TestSearchLimitsAndTruncates(t *testing.T) {
	long := strings.Repeat("长", maxSnippetRunes+10)
	client, _ := newTestClient(t, domain.WebSearchProviderTavily, http.StatusOK,
		`{"results":[{"title":"no url"},{"title":"A","url":"https://a.example","content":"`+long+`"},{"title":"B","url":"https://b.example"},{"title":"C","url":"https://c.example"}]}`)
	result, err := client.Search(context.Background(), Config{Provider: domain.WebSearchProviderTavily, APIKey: "key"}, Request{Query: "q", Count: 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].Title != "A" || result.Items[1].Title != "B" {
		t.Fatalf("items=%+v", result.Items)
	}
	if got := []rune(result.Items[0].Snippet); len(got) != maxSnippetRunes+1 || got[len(got)-1] != '…' {
		t.Fatalf("snippet length=%d", len(got))
	}
}

// TestSearchFailureKinds 验证认证失败、额度不足与服务商业务错误的分类。
func TestSearchFailureKinds(t *testing.T) {
	cases := []struct {
		name     string
		provider domain.WebSearchProvider
		status   int
		response string
		want     connectiontest.FailureKind
	}{
		{"unauthorized", domain.WebSearchProviderTavily, http.StatusUnauthorized, `{"detail":{"error":"Unauthorized"}}`, connectiontest.FailureUnauthorized},
		{"tavily quota", domain.WebSearchProviderTavily, 432, `{}`, connectiontest.FailureRateLimited},
		{"exa credits", domain.WebSearchProviderExa, http.StatusPaymentRequired, `{}`, connectiontest.FailureRateLimited},
		{"brave invalid token", domain.WebSearchProviderBrave, http.StatusUnprocessableEntity, `{"error":{"code":"SUBSCRIPTION_TOKEN_INVALID"}}`, connectiontest.FailureUnauthorized},
		{"bocha quota", domain.WebSearchProviderBocha, http.StatusForbidden, `{"code":"403","message":"You do not have enough money or package quota"}`, connectiontest.FailureRateLimited},
		{"volcengine auth", domain.WebSearchProviderVolcengine, http.StatusOK, `{"ResponseMetadata":{"Error":{"Code":"10403","Message":"auth"}}}`, connectiontest.FailureUnauthorized},
		{"baidu error", domain.WebSearchProviderBaidu, http.StatusOK, `{"code":216003,"message":"bad"}`, connectiontest.FailureProtocol},
		{"invalid json", domain.WebSearchProviderZhipu, http.StatusOK, `not json`, connectiontest.FailureProtocol},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newTestClient(t, tc.provider, tc.status, tc.response)
			_, err := client.Search(context.Background(), Config{Provider: tc.provider, APIKey: "key"}, Request{Query: "q"})
			if _, kind, ok := connectiontest.Details(err); !ok || kind != tc.want {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

// TestSearchRecencyNotice 验证无法按请求的时间范围筛选时结果带说明，能够筛选时不带说明。
func TestSearchRecencyNotice(t *testing.T) {
	cases := []struct {
		provider domain.WebSearchProvider
		recency  Recency
		response string
		notice   bool
	}{
		{domain.WebSearchProviderJina, RecencyWeek, `{"data":[]}`, true},
		{domain.WebSearchProviderJina, "", `{"data":[]}`, false},
		{domain.WebSearchProviderBaidu, RecencyDay, `{"references":[]}`, true},
		{domain.WebSearchProviderBaidu, RecencyMonth, `{"references":[]}`, false},
		{domain.WebSearchProviderTavily, RecencyDay, `{"results":[]}`, false},
	}
	for _, tc := range cases {
		client, _ := newTestClient(t, tc.provider, http.StatusOK, tc.response)
		result, err := client.Search(context.Background(), Config{Provider: tc.provider, APIKey: "key"}, Request{Query: "q", Recency: tc.recency})
		if err != nil || (result.Notice != "") != tc.notice {
			t.Fatalf("%s %s: result=%+v err=%v", tc.provider, tc.recency, result, err)
		}
	}
}

// TestSearchTransportErrorHidesQuery 验证传输失败的错误不包含查询参数中的凭据。
func TestSearchTransportErrorHidesQuery(t *testing.T) {
	client := &Client{http: &http.Client{}, endpoints: map[domain.WebSearchProvider]string{domain.WebSearchProviderSerpAPI: "http://127.0.0.1:1/search"}}
	_, err := client.Search(context.Background(), Config{Provider: domain.WebSearchProviderSerpAPI, APIKey: "secret-key"}, Request{Query: "q"})
	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "api_key") {
		t.Fatalf("err=%v", err)
	}
}
