//go:build server

package role

import (
	"context"
	"fmt"
	"slices"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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

// Execute 修改当前企业中的角色；该角色的成员与操作者一起先于角色行锁定，权限实际变化时推进成员的身份资料版本并通知其重读身份。
func (a *UpdateRoleAction) Execute(ctx context.Context, identity *servermodels.Identity, roleID string, input Input) (*Record, error) {
	var role *servermodels.Role
	var normalized Input
	var memberCounts map[string]int
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		var memberIDs []string
		if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("id").
			Where("workspace_id = ? AND role_id = ?", identity.Workspace.ID, roleID).
			Scan(ctx, &memberIDs); err != nil {
			return err
		}
		if err := identityaction.LockActiveUserAccounts(ctx, tx, identity, memberIDs); err != nil {
			return err
		}
		var err error
		role, err = loadRole(ctx, tx, identity.Workspace.ID, roleID, true)
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
		previous, err := loadPermissions(ctx, tx, identity.Workspace.ID, []string{role.ID})
		if err != nil {
			return err
		}
		if err := replacePermissions(ctx, tx, identity.Workspace.ID, role.ID, normalized.Permissions); err != nil {
			return err
		}
		// 权限实际变化时推进该角色成员的身份资料版本。
		if len(memberIDs) > 0 && !slices.Equal(domain.EffectiveRolePermissions(kind, previous[role.ID]), normalized.Permissions) {
			if err := identityaction.UpdateUserAccounts(ctx, identity.Workspace.ID, tx.NewUpdate().Model((*servermodels.User)(nil)).
				Set("profile_version = u.profile_version + 1").
				Where("u.workspace_id = ? AND u.id IN (?)", identity.Workspace.ID, bun.List(memberIDs))); err != nil {
				return err
			}
		}
		memberCounts, err = loadMemberCounts(ctx, tx, identity.Workspace.ID, []string{role.ID})
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
	return new(recordFromModel(*role, normalized.Permissions, memberCounts[role.ID])), nil
}
