//go:build server

package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ServiceSessionReturner 在管理操作事务中把失去接待资格的成员负责的开放客服周期退回原队列。
type ServiceSessionReturner interface {
	ReturnServiceSessionsToQueue(ctx context.Context, db bun.IDB, organizationID, identityID, operationID string, sources []domain.ServiceSource) error
}

// UpdateUserAction 修改企业成员账号。
type UpdateUserAction struct {
	db       *bun.DB
	returner ServiceSessionReturner
	enqueuer servertask.TxEnqueuer
}

// NewUpdateUserAction 创建企业成员修改操作。
func NewUpdateUserAction(db *bun.DB, returner ServiceSessionReturner, enqueuer servertask.TxEnqueuer) *UpdateUserAction {
	return &UpdateUserAction{db: db, returner: returner, enqueuer: enqueuer}
}

// Execute 修改工作区成员头像、显示名称、角色、接待开关、最大接待量和所属团队；关闭接待开关时重置其渠道路由并把负责的开放客服周期退回原队列，开启接待时为其补分配。
func (a *UpdateUserAction) Execute(ctx context.Context, identity *servermodels.Identity, userID string, input UpdateInput) (*User, error) {
	// 规范化并校验企业成员字段。
	fields := make(map[string]ValidationCode)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		fields["displayName"] = ValidationDisplayNameRequired
	} else if !domain.IdentityDisplayNameValid(input.DisplayName) {
		fields["displayName"] = ValidationDisplayNameInvalid
	}
	var roleIDValid bool
	input.RoleID, roleIDValid = common.NormalizeUUID(input.RoleID)
	if !roleIDValid {
		fields["roleId"] = ValidationRoleInvalid
	}
	if input.HandlesServiceRequests && input.MaxServiceSessions < 1 {
		fields["maxServiceSessions"] = ValidationMaxServiceSessionsInvalid
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	if !common.ValidUUID(userID) {
		return nil, ErrNotFound
	}
	var output *User
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUserAccounts(ctx, tx, identity, []string{userID}); err != nil {
			return err
		}
		administratorRoleID, err := roleaction.LockAdministratorRole(ctx, tx, identity.Organization.ID)
		if err != nil {
			return err
		}
		if err := validateRoleID(ctx, tx, identity.Organization.ID, input.RoleID); err != nil {
			return err
		}
		input.TeamIDs, err = validateTeamIDs(ctx, tx, identity.Organization.ID, input.TeamIDs)
		if err != nil {
			return err
		}
		// 读取锁内目标账号的最大接待量与所属团队，用于判断可接待的队列会话是否增加。
		var previous struct {
			MaxServiceSessions int      `bun:"max_service_sessions"`
			TeamIDs            []string `bun:"team_ids,array"`
		}
		err = tx.NewSelect().Model((*servermodels.User)(nil)).
			ColumnExpr("u.max_service_sessions").
			ColumnExpr("ARRAY(SELECT tm.team_id::text FROM team_members AS tm WHERE tm.organization_id = u.organization_id AND tm.identity_id = u.identity_id) AS team_ids").
			Where("u.organization_id = ? AND u.id = ?", identity.Organization.ID, userID).
			Scan(ctx, &previous)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// 最大接待量只在开启接待时写入，未开启接待时保留原值。
		accountUpdate := tx.NewUpdate().Model((*servermodels.User)(nil)).
			Set("profile_version = profile_version + CASE WHEN role_id IS DISTINCT FROM ?::uuid THEN 1 ELSE 0 END", input.RoleID).
			Set("role_id = ?", input.RoleID).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", userID)
		if input.HandlesServiceRequests {
			accountUpdate = accountUpdate.Set("max_service_sessions = ?", input.MaxServiceSessions)
		}
		identityID, err := identityaction.UpdateUserAccount(ctx, identity.Organization.ID, accountUpdate)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// 锁定企业身份行，头像替换、退回客服周期与重置渠道路由在锁后执行。
		var current struct {
			HandlesServiceRequests bool    `bun:"handles_service_requests"`
			AvatarFileID           *string `bun:"avatar_file_id"`
		}
		if err := tx.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
			Column("oi.handles_service_requests").
			ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
			Where("oi.organization_id = ? AND oi.id = ?", identity.Organization.ID, identityID).
			For("UPDATE OF oi").
			Scan(ctx, &current); err != nil {
			return err
		}
		// 传入新头像时激活该图片，替换下来的旧头像交给清理任务。
		var nextAvatarFileID *string
		if input.AvatarFileID != "" {
			nextAvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeUserAvatar, input.AvatarFileID, current.AvatarFileID)
			if err != nil {
				return err
			}
			if err := fileaction.RetireLinkedImage(ctx, tx, identity.Organization.ID, current.AvatarFileID, nextAvatarFileID); err != nil {
				return err
			}
		}
		displayChanged, err := identityaction.UpdateUserIdentity(ctx, tx, identity.Organization.ID, identityID, tx.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("avatar_file_id = COALESCE(?, avatar_file_id)", nextAvatarFileID).
			Set("handles_service_requests = ?", input.HandlesServiceRequests).
			Set("updated_at = now()"))
		if err != nil {
			return err
		}
		if current.HandlesServiceRequests && !input.HandlesServiceRequests {
			if err := chatstate.ResetChannelRoutingTarget(ctx, tx, identity.Organization.ID, domain.ChannelRoutingTargetTypeMember, identityID); err != nil {
				return err
			}
			if err := a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Organization.ID, identityID, uuid.NewV7().String(), nil); err != nil {
				return err
			}
		}
		if err := roleaction.EnsureActiveAdministratorRemains(ctx, tx, identity.Organization.ID, administratorRoleID); err != nil {
			return err
		}
		if err := teamaction.ReplaceIdentityTeams(ctx, tx, identity, identityID, input.TeamIDs); err != nil {
			return err
		}
		// 开启接待、调高最大接待量或加入新团队后可接待的队列会话增加，由补分配任务在锁内重新判断。
		joinedTeam := false
		for _, teamID := range input.TeamIDs {
			joinedTeam = joinedTeam || !slices.Contains(previous.TeamIDs, teamID)
		}
		if input.HandlesServiceRequests && (!current.HandlesServiceRequests || input.MaxServiceSessions > previous.MaxServiceSessions || joinedTeam) {
			if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{OrganizationID: identity.Organization.ID, IdentityID: identityID}); err != nil {
				return err
			}
		}
		// 名称或头像实际变化时，在成员资料写入完成后推进展示该成员的会话版本。
		if displayChanged {
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, identityID); err != nil {
				return err
			}
		}
		output, err = loadUser(ctx, tx, identity.Organization.ID, userID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}
	return output, nil
}
