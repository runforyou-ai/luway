//go:build server

package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"uuid"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

const RefreshToolsActionName = "mcp.refresh_tools"

// RefreshToolsInput 标识一次 MCP 工具更新，不携带连接凭据。
type RefreshToolsInput struct {
	OrganizationID string `json:"organizationId"`
	MCPServerID    string `json:"mcpServerId"`
	RefreshID      string `json:"refreshId"`
}

// ToolsScheduler 在业务事务内投递工具更新任务。
type ToolsScheduler struct{ enqueuer servertask.TxEnqueuer }

// NewToolsScheduler 创建 MCP 工具更新调度器。
func NewToolsScheduler(enqueuer servertask.TxEnqueuer) *ToolsScheduler {
	return &ToolsScheduler{enqueuer: enqueuer}
}

// EnqueueIn 替换当前更新批次，并在同一事务中可靠投递任务。
func (s *ToolsScheduler) EnqueueIn(ctx context.Context, tx bun.IDB, record *servermodels.MCPServer) error {
	refreshID := uuid.NewV7().String()
	input := RefreshToolsInput{OrganizationID: record.OrganizationID, MCPServerID: record.ID, RefreshID: refreshID}
	if _, err := s.enqueuer.EnqueueIn(ctx, tx, RefreshToolsActionName, input, servertask.EnqueueOptions{MaxAttempts: 1}); err != nil {
		return err
	}
	record.ToolsRefreshID = &refreshID
	record.ToolsFailure = ""
	_, err := tx.NewUpdate().Model(record).Column("tools_refresh_id", "tools_failure").WherePK().Exec(ctx)
	return err
}

// RefreshToolsAction 为当前企业的全部 MCP 服务提交更新任务。
type RefreshToolsAction struct {
	db        *bun.DB
	scheduler *ToolsScheduler
}

// NewRefreshToolsAction 创建工具批量更新操作。
func NewRefreshToolsAction(db *bun.DB, scheduler *ToolsScheduler) *RefreshToolsAction {
	return &RefreshToolsAction{db: db, scheduler: scheduler}
}

// Execute 锁定服务并跳过已经排队或执行中的更新。
func (a *RefreshToolsAction) Execute(ctx context.Context, identity *servermodels.Identity) error {
	count := 0
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		records := make([]servermodels.MCPServer, 0)
		if err := tx.NewSelect().Model(&records).Where("ms.organization_id = ?", identity.Organization.ID).Order("ms.id ASC").For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		for i := range records {
			if records[i].ToolsRefreshID != nil {
				continue
			}
			if err := a.scheduler.EnqueueIn(ctx, tx, &records[i]); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("enqueue MCP tools refresh: %w", err)
	}
	slog.Info("MCP 工具更新已提交", "organization_id", identity.Organization.ID, "server_count", count)
	return nil
}
