//go:build server

package contact

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RestoreContactAction 从回收站恢复联系人。
type RestoreContactAction struct {
	db *bun.DB
}

// NewRestoreContactAction 创建联系人恢复操作。
func NewRestoreContactAction(db *bun.DB) *RestoreContactAction {
	return &RestoreContactAction{db: db}
}

// Execute 恢复当前企业中已软删除的联系人。
func (a *RestoreContactAction) Execute(ctx context.Context, identity *servermodels.Identity, contactID string) (*ContactDetail, error) {
	var detail *ContactDetail
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().
			Table("contacts").
			Set("deleted_at = NULL").
			Where("id = ?", contactID).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("deleted_at IS NOT NULL").
			Exec(ctx)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrNotFound
		}
		loaded, err := loadContactDetail(ctx, tx, identity.Workspace.ID, contactID)
		if err != nil {
			return err
		}
		detail = loaded
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("restore contact: %w", err)
	}
	return detail, nil
}
