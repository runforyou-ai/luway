//go:build server

// Package businesssystem 实现工作区业务系统的查询、管理与运行时工具挂载。
package businesssystem

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// Input 定义业务系统可编辑字段。
type Input struct {
	Name           string
	Connection     ConnectionInput
	HeaderBindings map[string]domain.ContextValue
}

// ConnectionInput 定义业务系统的传输方式、对应的连接配置与凭据。
type ConnectionInput struct {
	Transport  domain.BusinessSystemTransport
	Connection domain.BusinessSystemConnection
	Credential domain.BusinessSystemCredential
}

// ToolSettingInput 定义业务系统中一个工具的管理员设置。
type ToolSettingInput struct {
	ToolName string
	Setting  domain.BusinessToolSetting
}

// Record 定义业务系统记录。
type Record struct {
	ID             string
	Name           string
	Transport      domain.BusinessSystemTransport
	Connection     domain.BusinessSystemConnection
	Credential     domain.BusinessSystemCredential
	HeaderBindings map[string]domain.ContextValue
	Tools          []domain.BusinessTool
	ToolSettings   map[string]domain.BusinessToolSetting
	ToolsUpdatedAt *time.Time
	ToolsUpdating  bool
	ToolsFailure   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
