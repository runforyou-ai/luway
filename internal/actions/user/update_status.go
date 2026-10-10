//go:build server

package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/actions/seat"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// PersonalAgentRetirer 在停用成员的事务中停用其负责的个人 AI 员工并移出所有群聊。
type PersonalAgentRetirer interface {
	RetirePersonalAgents(ctx context.Context, tx bun.Tx, actor *servermodels.Identity, responsibleUserID string) error
}

// UpdateStatusAction 修改用户账号状态。
type UpdateStatusAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
	seats    seat.Seats
	returner ServiceSessionReturner
	retirer  PersonalAgentRetirer
}

// NewUpdateStatusAction 创建用户账号状态修改操作。
func NewUpdateStatusAction(db *bun.DB, enqueuer servertask.TxEnqueuer, seats seat.Seats, returner ServiceSessionReturner, retirer PersonalAgentRetirer) *UpdateStatusAction {
	return &UpdateStatusAction{db: db, enqueuer: enqueuer, seats: seats, returner: returner, retirer: retirer}
}

// Execute 禁用或恢复用户账号，管理员只有管理员可以禁用或恢复，并在禁用时清理渠道分配、把其负责的开放客服周期退回原队列、停用其负责的个人 AI 员工、取消等待其确认或审批的 AI 员工操作；恢复时校验席位上限，个人 AI 员工保持停用。平台管理员的成员身份不可停用。
func (a *UpdateStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, userID string, status domain.IdentityStatus) (*User, error) {
	if status != domain.IdentityStatusActive && status != domain.IdentityStatusInactive {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"status": ValidationStatusInvalid}}
	}
	var output *User
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUserAccounts(ctx, tx, identity, []string{userID}); err != nil {
			return err
		}
		// 平台管理员须保留成员身份；共享锁定目标账号行，与授予平台管理员串行。
		if status == domain.IdentityStatusInactive {
			var platformAdmin bool
			err := tx.NewSelect().Model((*servermodels.Account)(nil)).
				Column("acc.is_platform_admin").
				Where("acc.id = (SELECT u.account_id FROM users AS u WHERE u.workspace_id = ? AND u.id = ?)", identity.Workspace.ID, userID).
				For("SHARE").
				Scan(ctx, &platformAdmin)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if platformAdmin {
				return ErrPlatformAdmin
			}
		}
		administratorRoleID, err := roleaction.LockAdministratorRole(ctx, tx, identity.Workspace.ID)
		if err != nil {
			return err
		}
		if err := roleaction.RequireAdministratorFor(ctx, tx, identity, []string{userID}, nil); err != nil {
			return err
		}
		var updatedUser struct {
			IdentityID string `bun:"identity_id"`
			Changed    bool   `bun:"changed"`
		}
		err = tx.NewUpdate().Model((*servermodels.User)(nil)).
			Set("status = ?", status).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id = ?", userID).
			Returning("new.identity_id, old.status <> new.status AS changed").
			Scan(ctx, &updatedUser)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == domain.IdentityStatusActive && updatedUser.Changed {
			realtime.Notify(ctx, realtime.Notification{WorkspaceID: identity.Workspace.ID, AudienceKind: realtime.AudienceUser, AudienceID: userID, Kind: realtime.KindMembershipAdded})
			if err := a.seats.Enforce(ctx, tx, identity.Workspace.ID); err != nil {
				return err
			}
		}
		// 账号启用或停用会改变可接待成员，通知企业全部网站访客重新读取接待状态。
		if updatedUser.Changed {
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
		}
		if status == domain.IdentityStatusInactive {
			if _, err := identityaction.UpdateUserIdentity(ctx, tx, identity.Workspace.ID, updatedUser.IdentityID, tx.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).
				Set("work_status = ?", domain.WorkStatusOffDuty).
				Set("work_status_updated_at = now()")); err != nil {
				return err
			}
			if err := serviceroute.ResetChannelRoutingTarget(ctx, tx, identity.Workspace.ID, domain.ChannelRoutingTargetTypeMember, updatedUser.IdentityID); err != nil {
				return err
			}
			if err := a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Workspace.ID, updatedUser.IdentityID, uuid.NewV7().String(), nil); err != nil {
				return err
			}
			if err := a.retirer.RetirePersonalAgents(ctx, tx, identity, userID); err != nil {
				return err
			}
			if err := agentprocess.CancelMemberToolDecisions(ctx, tx, a.enqueuer, identity.Workspace.ID, updatedUser.IdentityID); err != nil {
				return err
			}
			// 提交后通知实时服务关闭该用户的全部实时连接。
			realtime.Notify(ctx, realtime.UserDisabled(identity.Workspace.ID, userID))
		}
		// 会话列表展示对方账号状态，状态实际变化时推进展示该成员的会话版本。
		if updatedUser.Changed {
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, updatedUser.IdentityID); err != nil {
				return err
			}
		}
		if err := roleaction.EnsureActiveAdministratorRemains(ctx, tx, identity.Workspace.ID, administratorRoleID); err != nil {
			return err
		}
		output, err = loadUser(ctx, tx, identity.Workspace.ID, userID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update user status: %w", err)
	}
	return output, nil
}
