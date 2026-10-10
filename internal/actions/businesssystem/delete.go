//go:build server

package businesssystem

import (
	"context"
	"fmt"
	"log/slog"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteBusinessSystemAction 删除业务系统。
type DeleteBusinessSystemAction struct {
	db *bun.DB
}

// NewDeleteBusinessSystemAction 创建业务系统删除操作。
func NewDeleteBusinessSystemAction(db *bun.DB) *DeleteBusinessSystemAction {
	return &DeleteBusinessSystemAction{db: db}
}

// Execute 删除当前工作区中的业务系统，并为授权了它的 AI 员工创建移除该授权的新配置版本。
func (a *DeleteBusinessSystemAction) Execute(ctx context.Context, identity *servermodels.Identity, businessSystemID string) error {
	var revisedAgents int
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		system, err := loadBusinessSystem(ctx, tx, identity.Workspace.ID, businessSystemID, true)
		if err != nil {
			return err
		}
		revisedAgents, err = agentaction.RemoveBusinessSystemFromRevisions(ctx, tx, identity, system.ID)
		if err != nil {
			return err
		}
		_, err = tx.NewDelete().
			Model(system).
			Where("workspace_id = ?", identity.Workspace.ID).
			WherePK().
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete business system: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "业务系统删除成功", "business_system_id", businessSystemID, "revised_agent_count", revisedAgents)
	return nil
}
