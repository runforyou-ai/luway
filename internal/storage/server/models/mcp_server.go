//go:build server

package models

import (
	"github.com/runforyou-ai/cervi/internal/domain"
	"time"

	"github.com/uptrace/bun"
)

// MCPServer 表示 PostgreSQL 中的企业 MCP 服务。
type MCPServer struct {
	bun.BaseModel `bun:"table:mcp_servers,alias:ms"`

	ID                 string                           `bun:"id,pk"`
	OrganizationID     string                           `bun:"organization_id"`
	Name               string                           `bun:"name"`
	URL                string                           `bun:"url"`
	ServerType         domain.MCPServerType             `bun:"server_type"`
	AuthorizationToken string                           `bun:"authorization_token"`
	Tools              []domain.MCPTool                 `bun:"tools,type:jsonb"`
	ToolsUpdatedAt     *time.Time                       `bun:"tools_updated_at"`
	ToolsRefreshID     *string                          `bun:"tools_refresh_id"`
	ToolsFailure       string                           `bun:"tools_failure"`
	ToolPurposes       map[string]domain.MCPToolPurpose `bun:"tool_purposes,type:jsonb"`
	CustomerScoped     bool                             `bun:"customer_scoped"`
	CreatedAt          time.Time                        `bun:"created_at"`
	// UpdatedAt 记录配置保存时间，工具目录使用 ToolsUpdatedAt。
	UpdatedAt time.Time `bun:"updated_at"`
}
