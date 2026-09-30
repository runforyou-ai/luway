//go:build server

// Package mcpserver 实现企业 MCP 服务的查询与管理。
package mcpserver

import (
	"github.com/runforyou-ai/cervi/internal/domain"
	"time"
)

// Input 定义 MCP 服务可编辑字段。
type Input struct {
	Name               string
	URL                string
	ServerType         domain.MCPServerType
	AuthorizationToken string
	CustomerScoped     bool
}

// ConnectionInput 定义待测试的连接配置。
type ConnectionInput struct {
	URL                string
	ServerType         domain.MCPServerType
	AuthorizationToken string
}

// Record 定义 MCP 服务记录。
type Record struct {
	ID                 string
	Name               string
	URL                string
	ServerType         domain.MCPServerType
	AuthorizationToken string
	Tools              []domain.MCPTool
	ToolPurposes       map[string]domain.MCPToolPurpose
	CustomerScoped     bool
	ToolsUpdatedAt     *time.Time
	ToolsUpdating      bool
	ToolsFailure       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
