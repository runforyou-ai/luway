//go:build server

package mcpserver

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetMCPServerQuery 查询当前企业中的 MCP 服务。
type GetMCPServerQuery struct {
	db *bun.DB
}

// NewGetMCPServerQuery 创建 MCP 服务详情查询。
func NewGetMCPServerQuery(db *bun.DB) *GetMCPServerQuery {
	return &GetMCPServerQuery{db: db}
}

// Execute 返回当前企业中的 MCP 服务详情。
func (q *GetMCPServerQuery) Execute(ctx context.Context, identity *servermodels.Identity, mcpServerID string) (*Record, error) {
	mcpServer, err := loadMCPServer(ctx, q.db, identity.Organization.ID, mcpServerID, false)
	if err != nil {
		return nil, fmt.Errorf("get MCP server: %w", err)
	}
	output := recordFromModel(*mcpServer)
	return &output, nil
}
