//go:build server

package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ServiceSessionReturner 在管理操作事务中把失去接待资格的身份负责的开放客服周期退回原队列。
type ServiceSessionReturner interface {
	ReturnServiceSessionsToQueue(ctx context.Context, db bun.IDB, workspaceID, identityID, operationID string, audiences []domain.ServiceAudience) error
}

// UpdateStatusAction 修改 AI 员工状态。
type UpdateStatusAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
	returner ServiceSessionReturner
}

// NewUpdateStatusAction 创建 AI 员工状态修改操作。
func NewUpdateStatusAction(db *bun.DB, enqueuer servertask.TxEnqueuer, returner ServiceSessionReturner) *UpdateStatusAction {
	return &UpdateStatusAction{db: db, enqueuer: enqueuer, returner: returner}
}

// Execute 禁用或恢复 AI 员工账号，禁用时清理渠道分配、把其负责的开放客服周期退回原队列并取消其等待确认或审批的操作。
func (a *UpdateStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, agentID string, status domain.IdentityStatus) (*Agent, error) {
	if status != domain.IdentityStatusActive && status != domain.IdentityStatusInactive {
		return nil, &common.FieldError{Fields: map[string]common.FieldCode{"status": ValidationStatusInvalid}}
	}
	var output *Agent
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
			Where("workspace_id = ?", identity.Workspace.ID).
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
			if _, err := lockAgentIdentity(ctx, tx, identity.Workspace.ID, updatedAgent.IdentityID); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).
				Set("work_status = ?", domain.WorkStatusOffDuty).
				Set("work_status_updated_at = now()").
				Where("workspace_id = ?", identity.Workspace.ID).
				Where("id = ?", updatedAgent.IdentityID).
				Where("type = ?", domain.WorkspaceIdentityTypeAgent).
				Exec(ctx); err != nil {
				return err
			}
			if err := serviceroute.ResetChannelRoutingTarget(ctx, tx, identity.Workspace.ID, domain.ChannelRoutingTargetTypeMember, updatedAgent.IdentityID); err != nil {
				return err
			}
			if err := a.returner.ReturnServiceSessionsToQueue(ctx, tx, identity.Workspace.ID, updatedAgent.IdentityID, uuid.NewV7().String(), nil); err != nil {
				return err
			}
			if err := agentprocess.CancelAgentToolDecisions(ctx, tx, a.enqueuer, identity.Workspace.ID, updatedAgent.IdentityID); err != nil {
				return err
			}
		}
		// 会话列表展示 AI 员工账号状态，状态实际变化时在退回完成后推进展示该 AI 员工的会话版本。
		if updatedAgent.Changed {
			// AI 员工启用或停用会改变可接待身份，通知企业全部网站访客重新读取接待状态。
			realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Workspace.ID, updatedAgent.IdentityID); err != nil {
				return err
			}
		}
		output, err = loadAgent(ctx, tx, identity.Workspace.ID, agentID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update agent status: %w", err)
	}
	return output, nil
}

// lockedAgentIdentity 表示锁定时 AI 员工身份的头像。
type lockedAgentIdentity struct {
	AvatarFileID *string `bun:"avatar_file_id"`
}

// lockAgentIdentity 对 AI 员工身份取 FOR UPDATE，并返回锁定时的头像。
func lockAgentIdentity(ctx context.Context, db bun.IDB, workspaceID, identityID string) (lockedAgentIdentity, error) {
	var locked lockedAgentIdentity
	if err := db.NewSelect().TableExpr("workspace_identities AS oi").
		ColumnExpr("oi.avatar_file_id::text AS avatar_file_id").
		Where("oi.workspace_id = ? AND oi.id = ? AND oi.type = ?", workspaceID, identityID, domain.WorkspaceIdentityTypeAgent).
		For("UPDATE OF oi").
		Scan(ctx, &locked); err != nil {
		return lockedAgentIdentity{}, fmt.Errorf("lock agent identity: %w", err)
	}
	return locked, nil
}
