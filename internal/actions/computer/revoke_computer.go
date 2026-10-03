//go:build server

package computer

import (
	"context"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// RevokeComputerAction 撤销当前成员的电脑。
type RevokeComputerAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewRevokeComputerAction 创建电脑撤销操作。
func NewRevokeComputerAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *RevokeComputerAction {
	return &RevokeComputerAction{db: db, enqueuer: enqueuer}
}

// Execute 撤销当前成员名下的电脑并结束其事件流，随后结算派发给它且未结束的调用；电脑记录保留，该安装再次注册前凭据失效。
func (a *RevokeComputerAction) Execute(ctx context.Context, identity *servermodels.Identity, computerID string) error {
	if !common.ValidUUID(computerID) {
		return ErrNotFound
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().
			Model((*servermodels.Computer)(nil)).
			Set("revoked_at = now()").
			Set("updated_at = now()").
			Where("id = ? AND organization_id = ? AND owner_user_id = ?", computerID, identity.Organization.ID, identity.User.ID).
			Where("revoked_at IS NULL").
			Exec(ctx)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		realtime.Notify(ctx, realtime.ComputerRevoked(identity.Organization.ID, computerID))
		return nil
	})
	if err != nil {
		return fmt.Errorf("revoke computer: %w", err)
	}
	slog.Info("电脑已撤销", "organization_id", identity.Organization.ID, "user_id", identity.User.ID, "computer_id", computerID)
	return settleMatchingCalls(ctx, a.db, a.enqueuer, "cmp.id = ? AND cmp.revoked_at IS NOT NULL", computerID)
}
