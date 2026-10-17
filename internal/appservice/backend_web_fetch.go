package appservice

import "context"

// WebFetchBackend 定义网页读取设置的业务调用。
type WebFetchBackend interface {
	// GetWebFetchSettings 读取当前企业的网页读取设置。
	//appservice:route GET /settings/web-fetch perm=workspace.manage
	GetWebFetchSettings(context.Context, RequestMeta) (WebFetchSettings, error)
	// UpdateWebFetchSettings 修改当前企业的网页读取设置。
	//appservice:route PUT /settings/web-fetch perm=workspace.manage
	UpdateWebFetchSettings(context.Context, RequestMeta, WebFetchSettings) (WebFetchSettings, error)
	// TestWebFetchService 用草稿配置读取一次固定页面，验证读取服务可用。
	//appservice:route POST /settings/web-fetch/test perm=workspace.manage
	TestWebFetchService(context.Context, RequestMeta, WebFetchSettings) error
}
