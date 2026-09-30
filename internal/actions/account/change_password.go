//go:build server

package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

// ChangePasswordAction 修改当前账号的登录密码。
type ChangePasswordAction struct {
	db *bun.DB
}

// ChangePasswordInput 定义修改密码所需字段。
type ChangePasswordInput struct {
	CurrentPassword string
	NewPassword     string
}

// NewChangePasswordAction 创建密码修改操作。
func NewChangePasswordAction(db *bun.DB) *ChangePasswordAction {
	return &ChangePasswordAction{db: db}
}

// Execute 在事务内核验当前密码、保存新密码，删除该账号除当前会话外的全部登录会话，提交后通知 Gateway 关闭这些会话在各工作区的实时连接。
func (a *ChangePasswordAction) Execute(ctx context.Context, identity *servermodels.AccountIdentity, input ChangePasswordInput) error {
	if code, ok := passwordCode(input.NewPassword); ok {
		return &ValidationError{Fields: map[string]ValidationCode{"newPassword": code}}
	}
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		account := &servermodels.Account{}
		err := tx.NewSelect().Model(account).
			Column("password_hash", "status").
			Where("acc.id = ?", identity.Account.ID).
			For("UPDATE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && account.Status != string(domain.AccountStatusActive)) {
			return identityaction.ErrInvalid
		}
		if err != nil {
			return fmt.Errorf("read current password: %w", err)
		}
		if !commonpassword.Matches(account.PasswordHash, input.CurrentPassword) {
			return &ValidationError{Fields: map[string]ValidationCode{"currentPassword": ValidationCurrentPasswordIncorrect}}
		}
		passwordHash, err := commonpassword.Hash(input.NewPassword)
		if err != nil {
			return fmt.Errorf("hash new password: %w", err)
		}
		if _, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("password_hash = ?", passwordHash).
			Set("updated_at = now()").
			Where("id = ?", identity.Account.ID).
			Exec(ctx); err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		var revokedSessionIDs []string
		if err := tx.NewDelete().Model((*servermodels.AccountSession)(nil)).
			Where("account_id = ?", identity.Account.ID).
			Where("id <> ?", identity.Session.ID).
			Returning("id").
			Scan(ctx, &revokedSessionIDs); err != nil {
			return fmt.Errorf("revoke other sessions: %w", err)
		}
		if len(revokedSessionIDs) == 0 {
			return nil
		}
		var members []servermodels.User
		if err := tx.NewSelect().Model(&members).
			Column("id", "organization_id").
			Where("account_id = ?", identity.Account.ID).
			Scan(ctx); err != nil {
			return fmt.Errorf("load account members: %w", err)
		}
		for _, sessionID := range revokedSessionIDs {
			for _, member := range members {
				realtime.Notify(ctx, realtime.UserSessionLoggedOut(member.OrganizationID, member.ID, sessionID))
			}
		}
		return nil
	})
}
