package appservice

import "context"

// WebSearchBackend 定义联网搜索设置的业务调用。
type WebSearchBackend interface {
	// GetWebSearchSettings 读取当前企业的联网搜索设置。
	//appservice:route GET /settings/web-search perm=workspace.manage
	GetWebSearchSettings(context.Context, RequestMeta) (WebSearchSettings, error)
	// UpdateWebSearchSettings 修改当前企业的联网搜索设置。
	//appservice:route PUT /settings/web-search perm=workspace.manage
	UpdateWebSearchSettings(context.Context, RequestMeta, WebSearchSettings) (WebSearchSettings, error)
	// TestWebSearchService 用草稿配置执行一次搜索，验证搜索服务可用。
	//appservice:route POST /settings/web-search/test perm=workspace.manage
	TestWebSearchService(context.Context, RequestMeta, WebSearchService) error
}
