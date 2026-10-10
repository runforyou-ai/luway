//go:build server

package servicecategory

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// CreateAction 新增企业咨询分类。
type CreateAction struct{ db *bun.DB }

// NewCreateAction 创建咨询分类新增操作。
func NewCreateAction(db *bun.DB) *CreateAction { return &CreateAction{db: db} }

// Execute 校验并新增当前企业的咨询分类，未归档分类达到上限时拒绝。
func (a *CreateAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input = normalizeInput(input)
	var record *Record
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		count, err := tx.NewSelect().Model((*servermodels.ServiceCategory)(nil)).
			Where("sc.workspace_id = ? AND sc.archived_at IS NULL", identity.Workspace.ID).
			Count(ctx)
		if err != nil {
			return err
		}
		if count >= domain.ServiceCategoryMaxCount {
			return ErrLimitReached
		}
		if err := lockTeam(ctx, tx, identity.Workspace.ID, input.TeamID); err != nil {
			return err
		}
		category := &servermodels.ServiceCategory{WorkspaceID: identity.Workspace.ID, Name: input.Name, Description: input.Description, TeamID: input.TeamID}
		_, err = tx.NewInsert().Model(category).
			Column("workspace_id", "name", "description", "team_id").
			Returning("id").
			Exec(ctx)
		if _, ok := pgerr.UniqueViolation(err); ok {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		record, err = loadRecord(ctx, tx, identity.Workspace.ID, category.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create service category: %w", err)
	}
	return record, nil
}
