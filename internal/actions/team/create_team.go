//go:build server

package team

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// CreateTeamAction 创建企业团队。
type CreateTeamAction struct{ db *bun.DB }

// NewCreateTeamAction 创建团队新增操作。
func NewCreateTeamAction(db *bun.DB) *CreateTeamAction { return &CreateTeamAction{db: db} }

// Execute 校验并创建当前企业的团队。
func (a *CreateTeamAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*TeamRecord, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	record := &TeamRecord{}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		team := &servermodels.Team{OrganizationID: identity.Organization.ID, Name: input.Name, Description: input.Description, CreatedByUserID: identity.User.ID}
		_, err := tx.NewInsert().Model(team).
			Column("organization_id", "name", "description", "created_by_user_id").
			Returning("id, name, description, created_at, updated_at").
			Exec(ctx)
		if isUniqueViolation(err) {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		*record = TeamRecord{ID: team.ID, Name: team.Name, Description: team.Description, CreatedAt: team.CreatedAt, UpdatedAt: team.UpdatedAt}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create team: %w", err)
	}
	return record, nil
}

// isUniqueViolation 判断 PostgreSQL 错误是否为唯一约束冲突。
func isUniqueViolation(err error) bool {
	_, ok := pgerr.UniqueViolation(err)
	return ok
}
