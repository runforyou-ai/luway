//go:build server

package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// ValidateAssignment 校验并锁定成员可以使用的角色。
func ValidateAssignment(ctx context.Context, db bun.IDB, workspaceID, roleID string) (*servermodels.Role, error) {
	if !str.IsUUID(roleID) {
		return nil, ErrAssignmentInvalid
	}
	role := &servermodels.Role{}
	err := db.NewSelect().Model(role).
		Column("id", "kind", "name").
		Where("workspace_id = ?", workspaceID).
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

// RequireAdministratorFor 校验涉及管理员时操作者是管理员：这些成员或角色中含管理员时，按操作者在写事务中的最新角色判断，不是管理员时返回 ErrAdministratorOnly；调用方须已锁定操作者的用户行。
func RequireAdministratorFor(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, userIDs, roleIDs []string) error {
	if len(userIDs) == 0 {
		userIDs = []string{}
	}
	if len(roleIDs) == 0 {
		roleIDs = []string{}
	}
	var involved, administrator bool
	if err := tx.NewSelect().
		ColumnExpr("EXISTS (SELECT 1 FROM users AS u JOIN roles AS r ON r.id = u.role_id WHERE u.workspace_id = ? AND u.id = ANY(?::uuid[]) AND r.kind = ?) OR EXISTS (SELECT 1 FROM roles AS r WHERE r.workspace_id = ? AND r.id = ANY(?::uuid[]) AND r.kind = ?)",
			identity.Workspace.ID, pgdialect.Array(userIDs), domain.RoleKindAdmin, identity.Workspace.ID, pgdialect.Array(roleIDs), domain.RoleKindAdmin).
		ColumnExpr("EXISTS (SELECT 1 FROM users AS u JOIN roles AS r ON r.id = u.role_id WHERE u.workspace_id = ? AND u.id = ? AND r.kind = ?)",
			identity.Workspace.ID, identity.User.ID, domain.RoleKindAdmin).
		Scan(ctx, &involved, &administrator); err != nil {
		return err
	}
	if involved && !administrator {
		return ErrAdministratorOnly
	}
	return nil
}

// LockAdministratorRole 锁定管理员角色以串行维护有效管理员数量。
func LockAdministratorRole(ctx context.Context, db bun.IDB, workspaceID string) (string, error) {
	role := &servermodels.Role{}
	err := db.NewSelect().Model(role).
		Column("id").
		Where("workspace_id = ?", workspaceID).
		Where("kind = ?", domain.RoleKindAdmin).
		For("UPDATE").
		Scan(ctx)
	if err != nil {
		return "", err
	}
	return role.ID, nil
}

// EnsureActiveAdministratorRemains 校验企业仍有账号正常的真人管理员。
func EnsureActiveAdministratorRemains(ctx context.Context, db bun.IDB, workspaceID, administratorRoleID string) error {
	count, err := db.NewSelect().TableExpr("users AS u").
		Where("u.workspace_id = ?", workspaceID).
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

// Execute 校验成员和角色后一次性保存全部调整，调整前后的角色含管理员时只有管理员可以操作。
func (a *UpdateAssignmentsAction) Execute(ctx context.Context, identity *servermodels.Identity, changes []AssignmentInput) error {
	identityIDs := make([]string, 0, len(changes))
	roleIDs := make([]string, 0, len(changes))
	var seenIdentities, seenRoles set.Set[string]
	for index := range changes {
		change := &changes[index]
		var identityValid, roleValid bool
		change.IdentityID, identityValid = str.NormalizeUUID(change.IdentityID)
		change.RoleID, roleValid = str.NormalizeUUID(change.RoleID)
		if !identityValid || !roleValid {
			return ErrAssignmentInvalid
		}
		if !seenIdentities.Add(change.IdentityID) {
			return ErrAssignmentInvalid
		}
		identityIDs = append(identityIDs, change.IdentityID)
		if seenRoles.Add(change.RoleID) {
			roleIDs = append(roleIDs, change.RoleID)
		}
	}

	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		// 目标身份先解析账号编号，操作者与目标账号一起按编号取锁。
		var userIDs []string
		if len(identityIDs) > 0 {
			if err := tx.NewSelect().Model((*servermodels.User)(nil)).Column("id").
				Where("workspace_id = ? AND identity_id IN (?)", identity.Workspace.ID, bun.List(identityIDs)).
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
		administratorRoleID, err := LockAdministratorRole(ctx, tx, identity.Workspace.ID)
		if err != nil {
			return err
		}
		var lockedRoleIDs []string
		if err := tx.NewSelect().Model((*servermodels.Role)(nil)).Column("id").
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id IN (?)", bun.List(roleIDs)).
			For("KEY SHARE").
			Scan(ctx, &lockedRoleIDs); err != nil {
			return err
		}
		if len(lockedRoleIDs) != len(roleIDs) {
			return ErrAssignmentInvalid
		}
		// 调整前后的角色含管理员时只有管理员可以操作。
		if err := RequireAdministratorFor(ctx, tx, identity, userIDs, roleIDs); err != nil {
			return err
		}
		changeRoleIDs := arr.Map(changes, func(change AssignmentInput) string { return change.RoleID })
		if err := identityaction.UpdateUserAccounts(ctx, identity.Workspace.ID, tx.NewUpdate().Model((*servermodels.User)(nil)).
			TableExpr("unnest(?::uuid[], ?::uuid[]) AS change(identity_id, role_id)", pgdialect.Array(identityIDs), pgdialect.Array(changeRoleIDs)).
			Set("profile_version = u.profile_version + CASE WHEN u.role_id IS DISTINCT FROM change.role_id THEN 1 ELSE 0 END").
			Set("role_id = change.role_id").
			Where("u.workspace_id = ? AND u.identity_id = change.identity_id", identity.Workspace.ID)); err != nil {
			return err
		}
		return EnsureActiveAdministratorRemains(ctx, tx, identity.Workspace.ID, administratorRoleID)
	})
	if err != nil {
		return fmt.Errorf("update role assignments: %w", err)
	}
	return nil
}
