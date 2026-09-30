//go:build server

package mcpserver

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListMCPServersQuery 查询当前企业的 MCP 服务。
type ListMCPServersQuery struct {
	db *bun.DB
}

// NewListMCPServersQuery 创建 MCP 服务列表查询。
func NewListMCPServersQuery(db *bun.DB) *ListMCPServersQuery {
	return &ListMCPServersQuery{db: db}
}

// Execute 返回当前企业的 MCP 服务列表。
func (q *ListMCPServersQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Record, error) {
	records := make([]servermodels.MCPServer, 0)
	if err := q.db.NewSelect().
		Model(&records).
		Where("ms.organization_id = ?", identity.Organization.ID).
		Order("ms.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list MCP servers: %w", err)
	}
	output := make([]Record, 0, len(records))
	for _, record := range records {
		output = append(output, recordFromModel(record))
	}
	return output, nil
}
