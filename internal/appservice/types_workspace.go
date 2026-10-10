package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// WorkspaceIdentityType 表示工作区身份类型。
type WorkspaceIdentityType = domain.WorkspaceIdentityType

// CurrentWorkspace 定义当前工作区及其通用设置。
type CurrentWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// WorkspaceSettingsInput 定义工作区通用设置修改输入，工作区标识创建后不修改。
type WorkspaceSettingsInput struct {
	Name string `json:"name"`
}
