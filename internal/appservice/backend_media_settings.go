package appservice

import "context"

// MediaSettingsBackend 定义工作区媒体理解设置的业务调用。
type MediaSettingsBackend interface {
	// GetMediaUnderstandingSettings 读取当前企业的媒体理解设置。
	//appservice:route GET /settings/media-understanding perm=workspace.manage
	GetMediaUnderstandingSettings(context.Context, RequestMeta) (MediaUnderstandingSettings, error)
	// UpdateMediaUnderstandingSettings 修改当前企业的媒体理解设置。
	//appservice:route PUT /settings/media-understanding perm=workspace.manage
	UpdateMediaUnderstandingSettings(context.Context, RequestMeta, MediaUnderstandingSettings) (MediaUnderstandingSettings, error)
}
