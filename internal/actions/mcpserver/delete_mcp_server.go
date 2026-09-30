//go:build server

package mcpserver

import (
	"context"
	"fmt"
	"log/slog"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteMCPServerAction 删除 MCP 服务。
type DeleteMCPServerAction struct {
	db *bun.DB
}

// NewDeleteMCPServerAction 创建 MCP 服务删除操作。
func NewDeleteMCPServerAction(db *bun.DB) *DeleteMCPServerAction {
	return &DeleteMCPServerAction{db: db}
}

// Execute 删除当前企业中的 MCP 服务。
func (a *DeleteMCPServerAction) Execute(ctx context.Context, identity *servermodels.Identity, mcpServerID string) error {
	var revisedAgents int
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		mcpServer, err := loadMCPServer(ctx, tx, identity.Organization.ID, mcpServerID, true)
		if err != nil {
			return err
		}
		revisedAgents, err = agentaction.RemoveMCPServerFromRevisions(ctx, tx, identity, mcpServer.ID)
		if err != nil {
			return err
		}
		_, err = tx.NewDelete().
			Model(mcpServer).
			Where("organization_id = ?", identity.Organization.ID).
			WherePK().
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete MCP server: %w", err)
	}
	slog.Info("MCP 服务删除成功", "organization_id", identity.Organization.ID, "mcp_server_id", mcpServerID, "revised_agent_count", revisedAgents)
	return nil
}
