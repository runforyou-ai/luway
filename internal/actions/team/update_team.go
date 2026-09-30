//go:build server

package team

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateTeamAction 修改企业团队。
type UpdateTeamAction struct{ db *bun.DB }

// NewUpdateTeamAction 创建团队修改操作。
func NewUpdateTeamAction(db *bun.DB) *UpdateTeamAction { return &UpdateTeamAction{db: db} }

// Execute 校验并修改当前企业的团队。
func (a *UpdateTeamAction) Execute(ctx context.Context, identity *servermodels.Identity, teamID string, input Input) (*TeamRecord, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	if !common.ValidUUID(teamID) {
		return nil, ErrNotFound
	}
	var record *TeamRecord
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var nameChanged bool
		err := tx.NewUpdate().Model((*servermodels.Team)(nil)).
			Set("name = ?", input.Name).
			Set("description = ?", input.Description).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", teamID).
			Returning("old.name IS DISTINCT FROM new.name").
			Scan(ctx, &nameChanged)
		if isUniqueViolation(err) {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if nameChanged {
			if err := chatstate.TouchTeamConversations(ctx, tx, identity.Organization.ID, teamID); err != nil {
				return err
			}
		}
		record, err = loadTeam(ctx, tx, identity.Organization.ID, teamID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update team: %w", err)
	}
	return record, nil
}
