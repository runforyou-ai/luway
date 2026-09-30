//go:build server

package user

import (
	"context"
	"database/sql"
	"errors"

	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadCurrentIdentity 重新读取当前成员、工作区身份和所属账号，保留原登录会话。
func loadCurrentIdentity(ctx context.Context, db bun.IDB, current *servermodels.Identity) (*servermodels.Identity, error) {
	identity := &servermodels.Identity{Organization: current.Organization, Session: current.Session}
	err := db.NewSelect().TableExpr("users AS u").
		ColumnExpr("u.id::text, u.identity_id::text, u.organization_id::text, u.account_id::text, u.status, u.translation_language, u.message_notifications_enabled, u.role_id::text").
		ColumnExpr("oi.id::text, oi.organization_id::text, oi.type, oi.display_name, oi.avatar_file_id::text, oi.handles_service_requests, oi.work_status").
		ColumnExpr("acc.id::text, acc.email, acc.email_verified_at, acc.display_name, acc.locale, acc.time_zone, acc.status, acc.is_deployment_admin").
		Join("JOIN organization_identities AS oi ON oi.id = u.identity_id AND oi.organization_id = u.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeUser).
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Where("u.organization_id = ?", current.Organization.ID).
		Where("u.id = ?", current.User.ID).
		Scan(ctx,
			&identity.User.ID, &identity.User.IdentityID, &identity.User.OrganizationID, &identity.User.AccountID, &identity.User.Status,
			&identity.User.TranslationLanguage, &identity.User.MessageNotificationsEnabled, &identity.User.RoleID,
			&identity.OrganizationIdentity.ID, &identity.OrganizationIdentity.OrganizationID, &identity.OrganizationIdentity.Type,
			&identity.OrganizationIdentity.DisplayName, &identity.OrganizationIdentity.AvatarFileID,
			&identity.OrganizationIdentity.HandlesServiceRequests, &identity.OrganizationIdentity.WorkStatus,
			&identity.Account.ID, &identity.Account.Email, &identity.Account.EmailVerifiedAt, &identity.Account.DisplayName,
			&identity.Account.Locale, &identity.Account.TimeZone, &identity.Account.Status, &identity.Account.IsDeploymentAdmin,
		)
	return identity, err
}

// loadUser 读取工作区成员、账号邮箱、角色和所属团队。
func loadUser(ctx context.Context, db bun.IDB, organizationID, userID string) (*User, error) {
	if !common.ValidUUID(userID) {
		return nil, ErrNotFound
	}
	user := &User{}
	err := db.NewSelect().TableExpr("users AS u").
		ColumnExpr("u.id::text AS id, u.identity_id::text AS identity_id").
		ColumnExpr("acc.email, u.status, oi.display_name, oi.avatar_file_id::text AS avatar_file_id, oi.handles_service_requests, u.max_service_sessions, oi.work_status, oi.created_at").
		ColumnExpr("r.id::text AS role_id, r.kind AS role_kind, r.name AS role_name").
		Join("JOIN organization_identities AS oi ON oi.id = u.identity_id AND oi.organization_id = u.organization_id AND oi.type = ?", domain.OrganizationIdentityTypeUser).
		Join("JOIN roles AS r ON r.id = u.role_id AND r.organization_id = u.organization_id").
		Join("JOIN accounts AS acc ON acc.id = u.account_id").
		Where("u.id = ?", userID).
		Where("u.organization_id = ?", organizationID).
		Scan(ctx, user)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	user.Teams, err = teamaction.LoadIdentityTeams(ctx, db, organizationID, user.IdentityID)
	return user, err
}

// validateRoleID 校验并锁定当前企业的角色。
func validateRoleID(ctx context.Context, db bun.IDB, organizationID, roleID string) error {
	_, err := roleaction.ValidateAssignment(ctx, db, organizationID, roleID)
	if errors.Is(err, roleaction.ErrAssignmentInvalid) {
		return &ValidationError{Fields: map[string]ValidationCode{"roleId": ValidationRoleInvalid}}
	}
	return err
}

// validateTeamIDs 规范化团队编号并锁定同企业的全部目标团队。
func validateTeamIDs(ctx context.Context, db bun.IDB, organizationID string, teamIDs []string) ([]string, error) {
	ids, valid := common.NormalizeUUIDs(teamIDs)
	if !valid {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"teamIds": ValidationTeamInvalid}}
	}
	_, err := teamaction.LockTeams(ctx, db, organizationID, ids)
	if errors.Is(err, teamaction.ErrNotFound) {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"teamIds": ValidationTeamInvalid}}
	}
	if err != nil {
		return nil, err
	}
	return ids, nil
}
