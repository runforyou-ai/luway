//go:build server

package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// ValidateAssignment 校验并锁定成员可以使用的角色。
func ValidateAssignment(ctx context.Context, db bun.IDB, organizationID, roleID string) (*servermodels.Role, error) {
	if !common.ValidUUID(roleID) {
		return nil, ErrAssignmentInvalid
	}
	role := &servermodels.Role{}
	err := db.NewSelect().Model(role).
		Column("id", "kind", "name").
		Where("organization_id = ?", organizationID).
		Where("id = ?", roleID).
		For("KEY SHARE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAssignmentInvalid
	}
	if err != nil {
		return nil, err
	}
	return role, nil
}

// LockAdministratorRole 锁定管理员角色以串行维护有效管理员数量。
func LockAdministratorRole(ctx context.Context, db bun.IDB, organizationID string) (string, error) {
	role := &servermodels.Role{}
	err := db.NewSelect().Model(role).
		Column("id").
		Where("organization_id = ?", organizationID).
		Where("kind = ?", domain.RoleKindAdmin).
		For("UPDATE").
		Scan(ctx)
	if err != nil {
		return "", err
	}
	return role.ID, nil
}

// EnsureActiveAdministratorRemains 校验企业仍有账号正常的真人管理员。
func EnsureActiveAdministratorRemains(ctx context.Context, db bun.IDB, organizationID, administratorRoleID string) error {
	count, err := db.NewSelect().TableExpr("users AS u").
		Where("u.organization_id = ?", organizationID).
		Where("u.role_id = ?", administratorRoleID).
		Where("u.status = ?", domain.IdentityStatusActive).
		Count(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrLastActiveAdministrator
	}
	return nil
}

// UpdateAssignmentsAction 批量调整成员的企业角色。
type UpdateAssignmentsAction struct{ db *bun.DB }

// NewUpdateAssignmentsAction 创建成员角色批量调整操作。
func NewUpdateAssignmentsAction(db *bun.DB) *UpdateAssignmentsAction {
	return &UpdateAssignmentsAction{db: db}
}

// Execute 校验成员和角色后一次性保存全部调整。
func (a *UpdateAssignmentsAction) Execute(ctx context.Context, identity *servermodels.Identity, changes []AssignmentInput) error {
	identityIDs := make([]string, 0, len(changes))
	roleIDs := make([]string, 0, len(changes))
	seenIdentities := make(map[string]struct{}, len(changes))
	seenRoles := make(map[string]struct{}, len(changes))
	for index := range changes {
		change := &changes[index]
		var identityValid, roleValid bool
		change.IdentityID, identityValid = common.NormalizeUUID(change.IdentityID)
		change.RoleID, roleValid = common.NormalizeUUID(change.RoleID)
		if !identityValid || !roleValid {
			return ErrAssignmentInvalid
		}
		if _, exists := seenIdentities[change.IdentityID]; exists {
			return ErrAssignmentInvalid
		}
		seenIdentities[change.IdentityID] = struct{}{}
		identityIDs = append(identityIDs, change.IdentityID)
		if _, exists := seenRoles[change.RoleID]; !exists {
			seenRoles[change.RoleID] = struct{}{}
			roleIDs = append(roleIDs, change.RoleID)
		}
	}

	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 目标身份先解析账号编号，操作者与目标账号一起按编号取锁。
		var userIDs []string
		if len(identityIDs) > 0 {
			if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("id").
				Where("organization_id = ? AND identity_id IN (?)", identity.Organization.ID, bun.In(identityIDs)).
				Scan(ctx, &userIDs); err != nil {
				return err
			}
		}
		if err := identityaction.LockActiveUserAccounts(ctx, tx, identity, userIDs); err != nil {
			return err
		}
		if len(userIDs) != len(identityIDs) {
			return ErrAssignmentInvalid
		}
		if len(changes) == 0 {
			return nil
		}
		administratorRoleID, err := LockAdministratorRole(ctx, tx, identity.Organization.ID)
		if err != nil {
			return err
		}
		var lockedRoleIDs []string
		if err := tx.NewSelect().Model((*servermodels.Role)(nil)).Column("id").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id IN (?)", bun.In(roleIDs)).
			For("KEY SHARE").
			Scan(ctx, &lockedRoleIDs); err != nil {
			return err
		}
		if len(lockedRoleIDs) != len(roleIDs) {
			return ErrAssignmentInvalid
		}
		changeRoleIDs := make([]string, len(changes))
		for index, change := range changes {
			changeRoleIDs[index] = change.RoleID
		}
		if err := identityaction.UpdateUserAccounts(ctx, identity.Organization.ID, tx.NewUpdate().Model((*servermodels.User)(nil)).
			TableExpr("unnest(?::uuid[], ?::uuid[]) AS change(identity_id, role_id)", pgdialect.Array(identityIDs), pgdialect.Array(changeRoleIDs)).
			Set("profile_version = u.profile_version + CASE WHEN u.role_id IS DISTINCT FROM change.role_id THEN 1 ELSE 0 END").
			Set("role_id = change.role_id").
			Set("updated_at = now()").
			Where("u.organization_id = ? AND u.identity_id = change.identity_id", identity.Organization.ID)); err != nil {
			return err
		}
		return EnsureActiveAdministratorRemains(ctx, tx, identity.Organization.ID, administratorRoleID)
	})
	if err != nil {
		return fmt.Errorf("update role assignments: %w", err)
	}
	return nil
}
