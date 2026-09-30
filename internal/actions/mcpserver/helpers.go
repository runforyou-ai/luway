//go:build server

package mcpserver

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadMCPServer 读取当前企业中的 MCP 服务。
func loadMCPServer(ctx context.Context, db bun.IDB, organizationID, mcpServerID string, lock bool) (*servermodels.MCPServer, error) {
	if !common.ValidUUID(mcpServerID) {
		return nil, ErrNotFound
	}
	mcpServer := &servermodels.MCPServer{}
	query := db.NewSelect().
		Model(mcpServer).
		Where("ms.id = ?", mcpServerID).
		Where("ms.organization_id = ?", organizationID)
	if lock {
		query = query.For("UPDATE")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return mcpServer, nil
}

// retainToolPurposes 返回新工具目录中仍存在的工具的用途标记。
func retainToolPurposes(purposes map[string]domain.MCPToolPurpose, tools []domain.MCPTool) map[string]domain.MCPToolPurpose {
	retained := make(map[string]domain.MCPToolPurpose, len(purposes))
	for _, item := range tools {
		if purpose, ok := purposes[item.Name]; ok {
			retained[item.Name] = purpose
		}
	}
	return retained
}

// recordFromModel 转换 MCP 服务存储模型。
func recordFromModel(input servermodels.MCPServer) Record {
	return Record{
		ID: input.ID, Name: input.Name, URL: input.URL, ServerType: input.ServerType, AuthorizationToken: input.AuthorizationToken,
		Tools: input.Tools, ToolPurposes: input.ToolPurposes, CustomerScoped: input.CustomerScoped, ToolsUpdatedAt: input.ToolsUpdatedAt, ToolsUpdating: input.ToolsRefreshID != nil, ToolsFailure: input.ToolsFailure,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}
