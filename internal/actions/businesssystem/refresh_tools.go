//go:build server

package businesssystem

import (
	"context"
	"fmt"
	"log/slog"
	"uuid"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// RefreshToolsActionName 是业务系统工具目录更新任务的动作名称。
const RefreshToolsActionName = "business_system.refresh_tools"

// RefreshToolsInput 标识一次业务系统工具目录更新，不携带连接凭据。
type RefreshToolsInput struct {
	WorkspaceID      string `json:"workspaceId"`
	BusinessSystemID string `json:"businessSystemId"`
	RefreshID        string `json:"refreshId"`
}

// ToolsScheduler 在业务事务内投递工具更新任务。
type ToolsScheduler struct{ enqueuer servertask.TxEnqueuer }

// NewToolsScheduler 创建工具目录更新调度器。
func NewToolsScheduler(enqueuer servertask.TxEnqueuer) *ToolsScheduler {
	return &ToolsScheduler{enqueuer: enqueuer}
}

// EnqueueIn 替换当前更新批次，并登记在事务提交后投递的任务。
func (s *ToolsScheduler) EnqueueIn(ctx context.Context, tx bun.IDB, record *servermodels.BusinessSystem) error {
	refreshID := uuid.NewV7().String()
	input := RefreshToolsInput{WorkspaceID: record.WorkspaceID, BusinessSystemID: record.ID, RefreshID: refreshID}
	if _, err := s.enqueuer.EnqueueIn(ctx, RefreshToolsActionName, input, servertask.EnqueueOptions{WorkspaceID: record.WorkspaceID, MaxAttempts: 1}); err != nil {
		return err
	}
	record.ToolsRefreshID = &refreshID
	record.ToolsFailure = ""
	_, err := tx.NewUpdate().Model(record).Column("tools_refresh_id", "tools_failure").WherePK().Exec(ctx)
	return err
}

// RefreshToolsAction 为当前工作区的全部业务系统提交工具目录更新任务。
type RefreshToolsAction struct {
	db        *bun.DB
	scheduler *ToolsScheduler
}

// NewRefreshToolsAction 创建工具批量更新操作。
func NewRefreshToolsAction(db *bun.DB, scheduler *ToolsScheduler) *RefreshToolsAction {
	return &RefreshToolsAction{db: db, scheduler: scheduler}
}

// Execute 锁定当前工作区的业务系统，为没有排队或执行中更新的业务系统投递工具目录更新任务。
func (a *RefreshToolsAction) Execute(ctx context.Context, identity *servermodels.Identity) error {
	count := 0
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		records := make([]servermodels.BusinessSystem, 0)
		if err := tx.NewSelect().Model(&records).Where("bs.workspace_id = ?", identity.Workspace.ID).Order("bs.id ASC").For("UPDATE").Scan(ctx); err != nil {
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
		return fmt.Errorf("enqueue business system tools refresh: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "业务系统工具目录更新已提交", "business_system_count", count)
	return nil
}
