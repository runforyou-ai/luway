//go:build server

package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
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
		Where("inv.organization_id = ?", identity.Organization.ID).
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

// Execute 把当前工作区中待接受的邀请置为撤销，邀请链接随之失效。
func (a *RevokeAction) Execute(ctx context.Context, identity *servermodels.Identity, invitationID string) error {
	if !common.ValidUUID(invitationID) {
		return ErrNotFound
	}
	return a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		return revokePending(ctx, tx, identity.Organization.ID, invitationID)
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

// Execute 撤销原邀请并创建内容相同、有效期重新计算的新邀请。
func (a *RegenerateAction) Execute(ctx context.Context, identity *servermodels.Identity, invitationID string) (Created, error) {
	if !common.ValidUUID(invitationID) {
		return Created{}, ErrNotFound
	}
	var newID, value string
	err := a.create.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		previous := &servermodels.OrganizationInvitation{}
		err := tx.NewSelect().Model(previous).
			Where("inv.organization_id = ?", identity.Organization.ID).
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
		if err := revokePending(ctx, tx, identity.Organization.ID, invitationID); err != nil {
			return err
		}
		newID, value, err = insertInvitation(ctx, tx, identity, previous.InvitedEmail, previous.DisplayName, previous.RoleID)
		return err
	})
	if err != nil {
		return Created{}, fmt.Errorf("regenerate invitation: %w", err)
	}
	return a.create.created(ctx, identity, newID, value)
}

// revokePending 在调用方事务内撤销待接受的邀请，邀请不存在或已处理时返回 ErrNotFound。
func revokePending(ctx context.Context, tx bun.Tx, organizationID, invitationID string) error {
	result, err := tx.NewUpdate().Model((*servermodels.OrganizationInvitation)(nil)).
		Set("status = ?", domain.InvitationStatusRevoked).
		Set("updated_at = now()").
		Where("organization_id = ?", organizationID).
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
