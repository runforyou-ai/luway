//go:build server

package contact

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteContactAction 将联系人移入回收站。
type DeleteContactAction struct {
	db *bun.DB
}

// NewDeleteContactAction 创建联系人删除操作。
func NewDeleteContactAction(db *bun.DB) *DeleteContactAction {
	return &DeleteContactAction{db: db}
}

// Execute 软删除当前企业的联系人。
func (a *DeleteContactAction) Execute(ctx context.Context, identity *servermodels.Identity, contactID string) error {
	if !common.ValidUUID(contactID) {
		return ErrNotFound
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().
			Table("contacts").
			Set("deleted_at = now()").
			Set("updated_at = now()").
			Where("id = ?", contactID).
			Where("organization_id = ?", identity.Organization.ID).
			Where("deleted_at IS NULL").
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
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete contact: %w", err)
	}
	return nil
}
