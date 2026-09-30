//go:build server

package servicecategory

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// CreateAction 新增企业咨询分类。
type CreateAction struct{ db *bun.DB }

// NewCreateAction 创建咨询分类新增操作。
func NewCreateAction(db *bun.DB) *CreateAction { return &CreateAction{db: db} }

// Execute 校验并新增当前企业的咨询分类，未归档分类达到上限时拒绝。
func (a *CreateAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	var record *Record
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		count, err := tx.NewSelect().Model((*servermodels.ServiceCategory)(nil)).
			Where("sc.organization_id = ? AND sc.archived_at IS NULL", identity.Organization.ID).
			Count(ctx)
		if err != nil {
			return err
		}
		if count >= domain.ServiceCategoryMaxCount {
			return ErrLimitReached
		}
		if err := lockTeam(ctx, tx, identity.Organization.ID, input.TeamID); err != nil {
			return err
		}
		category := &servermodels.ServiceCategory{OrganizationID: identity.Organization.ID, Name: input.Name, Description: input.Description, TeamID: input.TeamID}
		_, err = tx.NewInsert().Model(category).
			Column("organization_id", "name", "description", "team_id").
			Returning("id").
			Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		record, err = loadRecord(ctx, tx, identity.Organization.ID, category.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create service category: %w", err)
	}
	return record, nil
}
