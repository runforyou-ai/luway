//go:build server

package servicecategory

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ArchiveAction 删除企业咨询分类。
type ArchiveAction struct{ db *bun.DB }

// NewArchiveAction 创建咨询分类删除操作。
func NewArchiveAction(db *bun.DB) *ArchiveAction { return &ArchiveAction{db: db} }

// Execute 归档当前企业的咨询分类：不再提供给 AI 与选择器，历史周期继续显示其名称。
func (a *ArchiveAction) Execute(ctx context.Context, identity *servermodels.Identity, categoryID string) error {
	if !common.ValidUUID(categoryID) {
		return ErrNotFound
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.ServiceCategory)(nil)).
			Set("archived_at = now()").
			Set("updated_at = now()").
			Where("organization_id = ? AND id = ? AND archived_at IS NULL", identity.Organization.ID, categoryID).
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
