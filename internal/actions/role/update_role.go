//go:build server

package role

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateRoleAction 修改角色信息和权限。
type UpdateRoleAction struct {
	db *bun.DB
}

// NewUpdateRoleAction 创建角色修改操作。
func NewUpdateRoleAction(db *bun.DB) *UpdateRoleAction {
	return &UpdateRoleAction{db: db}
}

// Execute 修改当前企业中的角色。
func (a *UpdateRoleAction) Execute(ctx context.Context, identity *servermodels.Identity, roleID string, input Input) (*Record, error) {
	var role *servermodels.Role
	var normalized Input
	var memberCounts map[string]int
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var err error
		role, err = loadRole(ctx, tx, identity.Organization.ID, roleID, true)
		if err != nil {
			return err
		}
		kind := domain.RoleKind(role.Kind)
		if kind == domain.RoleKindAdmin {
			return ErrAdminImmutable
		}
		var fields map[string]ValidationCode
		normalized, fields = normalizeInput(input, kind == domain.RoleKindCustom)
		if len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		if kind == domain.RoleKindCustom {
			role.Name = normalized.Name
			role.Description = normalized.Description
			if _, err := tx.NewUpdate().
				Model(role).
				Set("name = ?", role.Name).
				Set("description = ?", role.Description).
				Set("updated_at = now()").
				Returning("updated_at").
				WherePK().
				Exec(ctx); err != nil {
				return err
			}
		} else {
			if _, err := tx.NewUpdate().Model(role).Set("updated_at = now()").Returning("updated_at").WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		if err := replacePermissions(ctx, tx, identity.Organization.ID, role.ID, normalized.Permissions); err != nil {
			return err
		}
		memberCounts, err = loadMemberCounts(ctx, tx, identity.Organization.ID, []string{role.ID})
		if err != nil {
			return fmt.Errorf("count role members: %w", err)
		}
		return nil
	})
	if isRoleNameConflict(err) {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("update role: %w", err)
	}
	output := recordFromModel(*role, normalized.Permissions, memberCounts[role.ID])
	return &output, nil
}
