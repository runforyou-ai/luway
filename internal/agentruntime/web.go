package agentruntime

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/tools/web"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/runforyou-ai/support/arr"
)

const (
	// WebSearchToolName 是联网搜索工具名。
	WebSearchToolName = web.SearchToolName
	// WebFetchToolName 是网页读取工具名。
	WebFetchToolName = web.FetchToolName
)

// WebSearch 使用企业配置的搜索服务搜索互联网。
type WebSearch func(context.Context, websearch.Request) (websearch.Result, error)

// WebFetch 读取一个网页并返回 Markdown 正文。
type WebFetch func(context.Context, string) (webfetch.Document, error)

// newWebSearchTool 使用本次运行的搜索服务创建联网搜索工具，search 为空时调用返回不可用。
func newWebSearchTool(search WebSearch) tool.InvokableTool {
	var searcher web.Searcher
	if search != nil {
		searcher = web.SearcherFunc(func(ctx context.Context, request web.SearchRequest) (web.SearchResult, error) {
			result, err := search(ctx, websearch.Request{Query: request.Query, Count: request.Count, Recency: websearch.Recency(request.Recency)})
			if err != nil {
				return web.SearchResult{}, err
			}
			return web.SearchResult{Notice: result.Notice, Items: arr.Map(result.Items, func(item websearch.Item) web.SearchItem {
				return web.SearchItem{Title: item.Title, URL: item.URL, Snippet: item.Snippet, SiteName: item.SiteName, PublishedAt: item.PublishedAt}
			})}, nil
		})
	}
	return web.NewSearchTool(searcher, web.Options{Language: llm.Chinese})
}

// newWebFetchTool 创建读取网页正文的工具，fetch 为空时调用返回不可用。
func newWebFetchTool(fetch WebFetch) tool.InvokableTool {
	var fetcher web.Fetcher
	if fetch != nil {
		fetcher = web.FetcherFunc(func(ctx context.Context, address string) (web.Document, error) {
			document, err := fetch(ctx, address)
			return web.Document{URL: document.URL, Title: document.Title, Content: document.Content}, err
		})
	}
	return web.NewFetchTool(fetcher, web.Options{Language: llm.Chinese})
}
