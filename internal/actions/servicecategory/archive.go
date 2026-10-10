//go:build server

package servicecategory

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ArchiveAction 归档企业咨询分类。
type ArchiveAction struct{ db *bun.DB }

// NewArchiveAction 创建咨询分类归档操作。
func NewArchiveAction(db *bun.DB) *ArchiveAction { return &ArchiveAction{db: db} }

// Execute 归档当前企业的咨询分类：归档的分类不提供给 AI 与选择器，历史周期继续显示其名称。
func (a *ArchiveAction) Execute(ctx context.Context, identity *servermodels.Identity, categoryID string) error {
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.ServiceCategory)(nil)).
			Set("archived_at = now()").
			Where("workspace_id = ? AND id = ? AND archived_at IS NULL", identity.Workspace.ID, categoryID).
			Exec(ctx)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("archive service category: %w", err)
	}
	return nil
}
