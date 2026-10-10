//go:build server

package user

import (
	"context"
	"database/sql"
	"errors"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadCurrentIdentity 重新读取当前成员、工作区身份和所属账号，保留原登录会话与所属角色。
func loadCurrentIdentity(ctx context.Context, db bun.IDB, current *servermodels.Identity) (*servermodels.Identity, error) {
	identity := &servermodels.Identity{Workspace: current.Workspace, Role: current.Role, Session: current.Session}
	err := db.NewSelect().TableExpr("users AS u").
		ColumnExpr(identityaction.MemberColumns).
		ColumnExpr(identityaction.AccountColumns).
		Join("JOIN workspace_identities AS oi ON oi.id = u.identity_id AND oi.workspace_id = u.workspace_id AND oi.type = ?", domain.WorkspaceIdentityTypeUser).
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Where("u.workspace_id = ?", current.Workspace.ID).
		Where("u.id = ?", current.User.ID).
		Scan(ctx, append(identityaction.MemberTargets(&identity.User, &identity.WorkspaceIdentity), identityaction.AccountTargets(&identity.Account)...)...)
	return identity, err
}

// loadUser 读取工作区成员、账号邮箱、角色和所属团队。
func loadUser(ctx context.Context, db bun.IDB, workspaceID, userID string) (*User, error) {
	user := &User{}
	err := db.NewSelect().TableExpr("users AS u").
		ColumnExpr("u.id::text AS id, u.identity_id::text AS identity_id").
		ColumnExpr("acc.email, u.status, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, oi.handles_service_requests, u.max_service_sessions, oi.work_status, oi.created_at").
		ColumnExpr("r.id::text AS role_id, r.kind AS role_kind, r.name AS role_name").
		Join("JOIN workspace_identities AS oi ON oi.id = u.identity_id AND oi.workspace_id = u.workspace_id AND oi.type = ?", domain.WorkspaceIdentityTypeUser).
		Join("JOIN roles AS r ON r.id = u.role_id AND r.workspace_id = u.workspace_id").
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Where("u.id = ?", userID).
		Where("u.workspace_id = ?", workspaceID).
		Scan(ctx, user)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	user.Teams, err = teamaction.LoadIdentityTeams(ctx, db, workspaceID, user.IdentityID)
	return user, err
}

// validateRoleID 校验并锁定当前企业的角色。
func validateRoleID(ctx context.Context, db bun.IDB, workspaceID, roleID string) error {
	_, err := roleaction.ValidateAssignment(ctx, db, workspaceID, roleID)
	if errors.Is(err, roleaction.ErrAssignmentInvalid) {
		return &ValidationError{Fields: map[string]ValidationCode{"roleId": ValidationRoleInvalid}}
	}
	return err
}

// validateTeamIDs 规范化团队编号并锁定同企业的全部目标团队，团队不存在时返回字段错误。
func validateTeamIDs(ctx context.Context, db bun.IDB, workspaceID string, teamIDs []string) ([]string, error) {
	ids, _ := common.NormalizeUUIDs(teamIDs)
	_, err := teamaction.LockTeams(ctx, db, workspaceID, ids)
	if errors.Is(err, teamaction.ErrNotFound) {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"teamIds": ValidationTeamInvalid}}
	}
	if err != nil {
		return nil, err
	}
	return ids, nil
}
