//go:build server

package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// PersonalAgentRetirer 在停用成员的事务中停用其负责的个人 AI 员工并移出所有群聊。
type PersonalAgentRetirer interface {
	RetirePersonalAgents(ctx context.Context, tx bun.Tx, actor *servermodels.Identity, responsibleUserID string) ([]string, error)
	CancelRunContexts([]string)
}

// UpdateStatusAction 修改用户账号状态。
type UpdateStatusAction struct {
	db       *bun.DB
	returner ServiceSessionReturner
	retirer  PersonalAgentRetirer
}

// NewUpdateStatusAction 创建用户账号状态修改操作。
func NewUpdateStatusAction(db *bun.DB, returner ServiceSessionReturner, retirer PersonalAgentRetirer) *UpdateStatusAction {
	return &UpdateStatusAction{db: db, returner: returner, retirer: retirer}
}

// Execute 禁用或恢复用户账号，并在禁用时清理渠道分配、把其负责的开放客服周期退回原队列、停用其负责的个人 AI 员工；恢复时个人 AI 员工保持停用。平台管理员的成员身份不可停用。
func (a *UpdateStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, userID string, status domain.IdentityStatus) (*User, error) {
	if !common.ValidUUID(userID) {
		return nil, ErrNotFound
	}
	if status != domain.IdentityStatusActive && status != domain.IdentityStatusInactive {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"status": ValidationStatusInvalid}}
	}
	var output *User
	var cancelledRunIDs, retiredRunIDs []string
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUserAccounts(ctx, tx, identity, []string{userID}); err != nil {
			return err
		}
		// 平台管理员须保留成员身份；共享锁定目标账号行，与授予平台管理员串行。
		if status == domain.IdentityStatusInactive {
			var platformAdmin bool
			err := tx.NewSelect().Model((*servermodels.Account)(nil)).
				Column("acc.is_platform_admin").
				Where("acc.id = (SELECT u.account_id FROM users AS u WHERE u.organization_id = ? AND u.id = ?)", identity.Organization.ID, userID).
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
		administratorRoleID, err := roleaction.LockAdministratorRole(ctx, tx, identity.Organization.ID)
		if err != nil {
			return err
		}
		var updatedUser struct {
			IdentityID string `bun:"identity_id"`
			Changed    bool   `bun:"changed"`
		}
		err = tx.NewUpdate().Model((*servermodels.User)(nil)).
			Set("status = ?", status).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", userID).
			Returning("new.identity_id, old.status <> new.status AS changed").
			Scan(ctx, &updatedUser)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// 账号启用或停用会改变可接待成员，通知企业全部网站访客重新读取接待状态。
		if updatedUser.Changed {
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Organization.ID))
		}
		if status == domain.IdentityStatusInactive {
			if _, err := identityaction.UpdateUserIdentity(ctx, tx, identity.Organization.ID, updatedUser.IdentityID, tx.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
				Set("work_status = ?", domain.WorkStatusOffDuty).
				Set("work_status_updated_at = now()").
				Set("updated_at = now()")); err != nil {
				return err
			}
			if err := chatstate.ResetChannelRoutingTarget(ctx, tx, identity.Organization.ID, domain.ChannelRoutingTargetTypeMember, updatedUser.IdentityID); err != nil {
				return err
			}
			cancelledRunIDs, err = a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Organization.ID, updatedUser.IdentityID, uuid.NewV7().String(), nil)
			if err != nil {
				return err
			}
			if retiredRunIDs, err = a.retirer.RetirePersonalAgents(ctx, tx, identity, userID); err != nil {
				return err
			}
			// 提交后通知 Gateway 关闭该用户的全部实时连接。
			realtime.Notify(ctx, realtime.UserDisabled(identity.Organization.ID, userID))
		}
		// 会话列表展示对方账号状态，状态实际变化时推进展示该成员的会话版本。
		if updatedUser.Changed {
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, updatedUser.IdentityID); err != nil {
				return err
			}
		}
		if err := roleaction.EnsureActiveAdministratorRemains(ctx, tx, identity.Organization.ID, administratorRoleID); err != nil {
			return err
		}
		output, err = loadUser(ctx, tx, identity.Organization.ID, userID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update user status: %w", err)
	}
	a.returner.CancelRunContexts(cancelledRunIDs)
	a.retirer.CancelRunContexts(retiredRunIDs)
	return output, nil
}
