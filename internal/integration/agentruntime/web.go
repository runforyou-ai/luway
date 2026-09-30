package agentruntime

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	"github.com/runforyou-ai/luway/pkg/webfetch"
)

const (
	// WebSearchToolName 是联网搜索工具名。
	WebSearchToolName = "web_search"
	// WebFetchToolName 是网页读取工具名。
	WebFetchToolName = "web_fetch"
)

// WebSearch 使用企业配置的搜索服务搜索互联网。
type WebSearch func(context.Context, websearch.Request) (websearch.Result, error)

// WebFetch 读取一个网页并返回 Markdown 正文。
type WebFetch func(context.Context, string) (webfetch.Document, error)

type webSearchInput struct {
	Query   string            `json:"query" jsonschema:"required" jsonschema_description:"搜索关键词"`
	Count   int               `json:"count,omitempty" jsonschema_description:"返回的结果条数，默认 5，最多 10"`
	Recency websearch.Recency `json:"recency,omitempty" jsonschema:"enum=day,enum=week,enum=month,enum=year" jsonschema_description:"只返回最近一天、一周、一月或一年内发布的结果，不传表示不限时间"`
}

type webFetchInput struct {
	URL string `json:"url" jsonschema:"required" jsonschema_description:"网页地址，以 http:// 或 https:// 开头"`
}

// newWebSearchTool 使用本次运行的搜索服务创建联网搜索 Tool。
func newWebSearchTool(search WebSearch) (tool.InvokableTool, error) {
	return toolutils.InferTool(
		WebSearchToolName,
		"搜索互联网上的公开信息，返回标题、链接、摘要和发布时间。",
		func(ctx context.Context, input webSearchInput) (websearch.Result, error) {
			return search(ctx, websearch.Request{Query: input.Query, Count: input.Count, Recency: input.Recency})
		},
	)
}

// newWebFetchTool 创建读取网页正文的 Tool。
func newWebFetchTool(fetch WebFetch) (tool.InvokableTool, error) {
	return toolutils.InferTool(
		WebFetchToolName,
		"读取一个公开网页的正文，去掉导航和页脚等页面结构后以 Markdown 返回。",
		func(ctx context.Context, input webFetchInput) (webfetch.Document, error) {
			return fetch(ctx, input.URL)
		},
	)
}
