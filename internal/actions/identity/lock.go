//go:build server

// Package identity 提供 Action 层共用的成员查询列、有效成员条件、资料版本写入和当前用户写入守卫。
package identity

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrInvalid 表示当前用户与企业关联已经失效。
var ErrInvalid = errors.New("identity association is invalid")

// LockActiveUser 校验当前身份并锁定有效用户账号，供写事务复用。
func LockActiveUser(ctx context.Context, tx bun.Tx, identity *servermodels.Identity) error {
	return LockActiveUserAccounts(ctx, tx, identity, nil)
}

// LockActiveUserAccounts 按账号编号锁定操作者及关联账号，并校验操作者身份与有效状态。
func LockActiveUserAccounts(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, relatedUserIDs []string) error {
	if identity == nil ||
		!str.IsUUID(identity.Workspace.ID) ||
		!str.IsUUID(identity.WorkspaceIdentity.ID) ||
		!str.IsUUID(identity.User.ID) ||
		!str.IsUUID(identity.User.IdentityID) ||
		identity.WorkspaceIdentity.ID != identity.User.IdentityID ||
		identity.WorkspaceIdentity.WorkspaceID != identity.Workspace.ID ||
		identity.WorkspaceIdentity.Type != string(domain.WorkspaceIdentityTypeUser) ||
		identity.User.WorkspaceID != identity.Workspace.ID {
		return ErrInvalid
	}
	userIDs := append([]string{identity.User.ID}, relatedUserIDs...)
	var users []servermodels.User
	err := tx.NewSelect().
		Model(&users).
		Column("id", "identity_id", "status").
		Where("id IN (?)", bun.List(userIDs)).
		Where("workspace_id = ?", identity.Workspace.ID).
		OrderExpr("id ASC").
		For("NO KEY UPDATE").
		Scan(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.ID == identity.User.ID && user.IdentityID == identity.User.IdentityID && user.Status == string(domain.IdentityStatusActive) {
			return nil
		}
	}
	return ErrInvalid
}

// ErrServiceHandlingRequired 表示当前身份未开启处理服务请求。
var ErrServiceHandlingRequired = errors.New("service handling identity required")

// LockActiveServiceHandlingUser 锁定当前真人身份的有效账号，并校验其已开启处理服务请求；未开启时返回 ErrServiceHandlingRequired。
func LockActiveServiceHandlingUser(ctx context.Context, tx bun.Tx, identity *servermodels.Identity) error {
	if err := LockActiveUser(ctx, tx, identity); err != nil {
		return err
	}
	var handlesServiceRequests bool
	if err := tx.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).
		Column("oi.handles_service_requests").
		Where("oi.workspace_id = ? AND oi.id = ?", identity.Workspace.ID, identity.WorkspaceIdentity.ID).
		Scan(ctx, &handlesServiceRequests); err != nil {
		return err
	}
	if !handlesServiceRequests {
		return ErrServiceHandlingRequired
	}
	return nil
}
