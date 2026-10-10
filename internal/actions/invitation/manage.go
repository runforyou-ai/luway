//go:build server

package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListQuery 读取当前工作区尚未接受的邀请。
type ListQuery struct {
	db *bun.DB
}

// NewListQuery 创建邀请列表查询。
func NewListQuery(db *bun.DB) *ListQuery {
	return &ListQuery{db: db}
}

// Execute 按创建时间倒序返回待接受的邀请，已过期的邀请状态为 expired。
func (q *ListQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Invitation, error) {
	invitations := make([]Invitation, 0)
	if err := selectInvitations(q.db).
		Where("inv.workspace_id = ?", identity.Workspace.ID).
		Where("inv.status = ?", domain.InvitationStatusPending).
		OrderExpr("inv.created_at DESC, inv.id DESC").
		Scan(ctx, &invitations); err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	return invitations, nil
}

// RevokeAction 撤销待接受的邀请。
type RevokeAction struct {
	db *bun.DB
}

// NewRevokeAction 创建撤销邀请操作。
func NewRevokeAction(db *bun.DB) *RevokeAction {
	return &RevokeAction{db: db}
}

// Execute 把当前工作区中待接受的邀请置为撤销，邀请链接随之失效；管理员角色的邀请只有管理员可以撤销。
func (a *RevokeAction) Execute(ctx context.Context, identity *servermodels.Identity, invitationID string) error {
	return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var roleID string
		err := tx.NewSelect().Model((*servermodels.WorkspaceInvitation)(nil)).
			ColumnExpr("inv.role_id::text").
			Where("inv.workspace_id = ? AND inv.id = ? AND inv.status = ?", identity.Workspace.ID, invitationID, domain.InvitationStatusPending).
			For("UPDATE").
			Scan(ctx, &roleID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := roleaction.RequireAdministratorFor(ctx, tx, identity, nil, []string{roleID}); err != nil {
			return err
		}
		return revokePending(ctx, tx, identity.Workspace.ID, invitationID)
	})
}

// RegenerateAction 以原邀请的邮箱、显示名称和角色重新生成邀请链接。
type RegenerateAction struct {
	create *CreateAction
}

// NewRegenerateAction 创建重新生成邀请链接操作。
func NewRegenerateAction(create *CreateAction) *RegenerateAction {
	return &RegenerateAction{create: create}
}

// Execute 撤销原邀请并创建内容相同、有效期重新计算的新邀请，管理员角色的邀请只有管理员可以重新生成；配置了邮件发送时在同一事务内为新邀请投递邀请邮件任务。
func (a *RegenerateAction) Execute(ctx context.Context, identity *servermodels.Identity, invitationID string) (Created, error) {
	var newID, value string
	emailQueued := a.create.emailEnabled()
	err := serverstorage.RunInTx(ctx, a.create.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		previous := &servermodels.WorkspaceInvitation{}
		err := tx.NewSelect().Model(previous).
			Where("inv.workspace_id = ?", identity.Workspace.ID).
			Where("inv.id = ?", invitationID).
			Where("inv.status = ?", domain.InvitationStatusPending).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := roleaction.RequireAdministratorFor(ctx, tx, identity, nil, []string{previous.RoleID}); err != nil {
			return err
		}
		if err := revokePending(ctx, tx, identity.Workspace.ID, invitationID); err != nil {
			return err
		}
		newID, value, err = insertInvitation(ctx, tx, identity, previous.InvitedEmail, previous.DisplayName, previous.RoleID)
		if err != nil || !emailQueued {
			return err
		}
		return a.create.enqueueEmail(ctx, tx, identity, newID)
	})
	if err != nil {
		return Created{}, fmt.Errorf("regenerate invitation: %w", err)
	}
	return a.create.created(ctx, identity, newID, value, emailQueued)
}

// revokePending 在调用方事务内撤销待接受的邀请，邀请不存在或已处理时返回 ErrNotFound。
func revokePending(ctx context.Context, tx bun.Tx, workspaceID, invitationID string) error {
	result, err := tx.NewUpdate().Model((*servermodels.WorkspaceInvitation)(nil)).
		Set("status = ?", domain.InvitationStatusRevoked).
		Where("workspace_id = ?", workspaceID).
		Where("id = ?", invitationID).
		Where("status = ?", domain.InvitationStatusPending).
		Exec(ctx)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrNotFound
	}
	return nil
}
