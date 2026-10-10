//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// BusinessSystem 表示 PostgreSQL 中的工作区业务系统。
type BusinessSystem struct {
	bun.BaseModel `bun:"table:business_systems,alias:bs"`

	ID             string                                `bun:"id,pk"`
	WorkspaceID    string                                `bun:"workspace_id"`
	Name           string                                `bun:"name"`
	Transport      domain.BusinessSystemTransport        `bun:"transport"`
	Connection     domain.BusinessSystemConnection       `bun:"connection,type:jsonb"`
	Credential     domain.BusinessSystemCredential       `bun:"credential,type:jsonb"`
	HeaderBindings map[string]domain.ContextValue        `bun:"header_bindings,type:jsonb"`
	Tools          []domain.BusinessTool                 `bun:"tools,type:jsonb"`
	ToolsUpdatedAt *time.Time                            `bun:"tools_updated_at"`
	ToolsRefreshID *string                               `bun:"tools_refresh_id"`
	ToolsFailure   string                                `bun:"tools_failure"`
	ToolSettings   map[string]domain.BusinessToolSetting `bun:"tool_settings,type:jsonb"`
	CreatedAt      time.Time                             `bun:"created_at"`
	// UpdatedAt 记录配置保存时间，工具目录使用 ToolsUpdatedAt。
	UpdatedAt time.Time `bun:"updated_at"`
}
