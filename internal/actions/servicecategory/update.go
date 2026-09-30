//go:build server

package servicecategory

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// UpdateAction 修改企业咨询分类。
type UpdateAction struct{ db *bun.DB }

// NewUpdateAction 创建咨询分类修改操作。
func NewUpdateAction(db *bun.DB) *UpdateAction { return &UpdateAction{db: db} }

// Execute 校验并修改当前企业未归档的咨询分类。
func (a *UpdateAction) Execute(ctx context.Context, identity *servermodels.Identity, categoryID string, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	if !common.ValidUUID(categoryID) {
		return nil, ErrNotFound
	}
	var record *Record
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockTeam(ctx, tx, identity.Organization.ID, input.TeamID); err != nil {
			return err
		}
		result, err := tx.NewUpdate().Model((*servermodels.ServiceCategory)(nil)).
			Set("name = ?", input.Name).
			Set("description = ?", input.Description).
			Set("team_id = ?", input.TeamID).
			Set("updated_at = now()").
			Where("organization_id = ? AND id = ? AND archived_at IS NULL", identity.Organization.ID, categoryID).
			Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrNotFound
		}
		record, err = loadRecord(ctx, tx, identity.Organization.ID, categoryID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update service category: %w", err)
	}
	return record, nil
}
