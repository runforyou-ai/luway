//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ServiceSessionReturner 在管理操作事务中把失去接待资格的身份负责的开放客服周期退回原队列，并在提交后中断被取消的模型调用。
type ServiceSessionReturner interface {
	ReturnServiceSessionsToQueue(ctx context.Context, db bun.IDB, organizationID, identityID, operationID string, sources []domain.ServiceSource) ([]string, error)
	CancelRunContexts([]string)
}

// UpdateStatusAction 修改 AI 员工状态。
type UpdateStatusAction struct {
	db       *bun.DB
	returner ServiceSessionReturner
}

// NewUpdateStatusAction 创建 AI 员工状态修改操作。
func NewUpdateStatusAction(db *bun.DB, returner ServiceSessionReturner) *UpdateStatusAction {
	return &UpdateStatusAction{db: db, returner: returner}
}

// Execute 禁用或恢复 AI 员工账号，禁用时清理渠道分配并把其负责的开放客服周期退回原队列。
func (a *UpdateStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, status domain.IdentityStatus) (*Agent, error) {
	if !common.ValidUUID(agentID) {
		return nil, ErrNotFound
	}
	if status != domain.IdentityStatusActive && status != domain.IdentityStatusInactive {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"status": ValidationStatusInvalid}}
	}
	var output *Agent
	var cancelledRunIDs []string
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var updatedAgent struct {
			IdentityID string `bun:"identity_id"`
			Changed    bool   `bun:"changed"`
		}
		err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
			Set("status = ?", status).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", agentID).
			Where(serviceAgentCondition).
			Returning("new.identity_id, old.status <> new.status AS changed").
			Scan(ctx, &updatedAgent)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == domain.IdentityStatusInactive {
			// 锁序为 AI 员工记录、企业身份、渠道、会话，与编辑 AI 员工一致；身份锁与以该身份为目标的渠道编辑、入站路由和转交串行。
			if _, err := lockAgentIdentity(ctx, tx, identity.Organization.ID, updatedAgent.IdentityID); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
				Set("work_status = ?", domain.WorkStatusOffDuty).
				Set("work_status_updated_at = now()").
				Set("updated_at = now()").
				Where("organization_id = ?", identity.Organization.ID).
				Where("id = ?", updatedAgent.IdentityID).
				Where("type = ?", domain.OrganizationIdentityTypeAgent).
				Exec(ctx); err != nil {
				return err
			}
			if err := chatstate.ResetChannelRoutingTarget(ctx, tx, identity.Organization.ID, domain.ChannelRoutingTargetTypeMember, updatedAgent.IdentityID); err != nil {
				return err
			}
			cancelledRunIDs, err = a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Organization.ID, updatedAgent.IdentityID, uuid.NewV7().String(), nil)
			if err != nil {
				return err
			}
		}
		// 会话列表展示 AI 员工账号状态，状态实际变化时在退回完成后推进展示该 AI 员工的会话版本。
		if updatedAgent.Changed {
			// AI 员工启用或停用会改变可接待身份，通知企业全部网站访客重新读取接待状态。
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Organization.ID))
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, updatedAgent.IdentityID); err != nil {
				return err
			}
		}
		output, err = loadAgent(ctx, tx, identity.Organization.ID, agentID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update agent status: %w", err)
	}
	a.returner.CancelRunContexts(cancelledRunIDs)
	return output, nil
}

// lockedAgentIdentity 表示锁定时 AI 员工身份的头像。
type lockedAgentIdentity struct {
	AvatarFileID *string `bun:"avatar_file_id"`
}

// lockAgentIdentity 对 AI 员工身份取 FOR UPDATE，并返回锁定时的头像。
func lockAgentIdentity(ctx context.Context, db bun.IDB, organizationID, identityID string) (lockedAgentIdentity, error) {
	var locked lockedAgentIdentity
	if err := db.NewSelect().TableExpr("organization_identities AS oi").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		Where("oi.organization_id = ? AND oi.id = ? AND oi.type = ?", organizationID, identityID, domain.OrganizationIdentityTypeAgent).
		For("UPDATE OF oi").
		Scan(ctx, &locked); err != nil {
		return lockedAgentIdentity{}, fmt.Errorf("lock agent identity: %w", err)
	}
	return locked, nil
}
