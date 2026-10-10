//go:build server

package auth

import (
	"context"

	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// LogoutAction 执行账号退出登录操作。
type LogoutAction struct {
	db *bun.DB
}

// NewLogoutAction 创建账号退出登录操作。
func NewLogoutAction(db *bun.DB) *LogoutAction {
	return &LogoutAction{db: db}
}

// Execute 删除当前登录会话，提交后撤销该会话在各工作区的实时连接。
func (a *LogoutAction) Execute(ctx context.Context, identity *servermodels.AccountIdentity) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*servermodels.AccountSession)(nil)).
			Where("id = ?", identity.Session.ID).
			Where("account_id = ?", identity.Account.ID).
			Exec(ctx); err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.Notification{AudienceKind: realtime.AudienceAccount, AudienceID: identity.Account.ID, Kind: realtime.KindSessionLoggedOut, TokenSessionID: identity.Session.ID})
		var members []servermodels.User
		if err := tx.NewSelect().Model(&members).
			Column("id", "workspace_id").
			Where("account_id = ?", identity.Account.ID).
			Scan(ctx); err != nil {
			return err
		}
		for _, member := range members {
			realtime.Notify(ctx, realtime.UserSessionLoggedOut(member.WorkspaceID, member.ID, identity.Session.ID))
		}
		return nil
	})
}
