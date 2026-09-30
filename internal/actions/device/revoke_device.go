//go:build server

package device

import (
	"context"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RevokeDeviceAction 撤销当前用户的设备。
type RevokeDeviceAction struct {
	db *bun.DB
}

// NewRevokeDeviceAction 创建设备撤销操作。
func NewRevokeDeviceAction(db *bun.DB) *RevokeDeviceAction {
	return &RevokeDeviceAction{db: db}
}

// Execute 撤销当前用户名下的设备；设备记录保留，该安装再次注册前不再受信任。
func (a *RevokeDeviceAction) Execute(ctx context.Context, identity *servermodels.Identity, deviceID string) error {
	if !common.ValidUUID(deviceID) {
		return ErrNotFound
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		result, err := tx.NewUpdate().
			Model((*servermodels.Device)(nil)).
			Set("revoked_at = now()").
			Set("updated_at = now()").
			Where("id = ?", deviceID).
			Where("organization_id = ?", identity.Organization.ID).
			Where("user_id = ?", identity.User.ID).
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
		return nil
	})
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	slog.Info("设备已撤销", "organization_id", identity.Organization.ID, "user_id", identity.User.ID, "device_id", deviceID)
	return nil
}
