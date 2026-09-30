//go:build server

package device

import (
	"context"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RegisterDeviceAction 注册当前用户的本机设备。
type RegisterDeviceAction struct {
	db *bun.DB
}

// NewRegisterDeviceAction 创建设备注册操作。
func NewRegisterDeviceAction(db *bun.DB) *RegisterDeviceAction {
	return &RegisterDeviceAction{db: db}
}

// Execute 按安装标识注册或更新当前用户的本机设备；同一安装重新注册即重新授权，撤销标记随之清除。
func (a *RegisterDeviceAction) Execute(ctx context.Context, identity *servermodels.Identity, input RegisterInput) (*Record, error) {
	input, fields := normalizeRegisterInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	device := servermodels.Device{
		OrganizationID: identity.Organization.ID,
		UserID:         identity.User.ID,
		InstallID:      input.InstallID,
		Name:           input.Name,
		Platform:       input.Platform,
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		_, err := tx.NewInsert().
			Model(&device).
			Column("organization_id", "user_id", "install_id", "name", "platform").
			On("CONFLICT (organization_id, user_id, install_id) DO UPDATE").
			Set("name = EXCLUDED.name").
			Set("platform = EXCLUDED.platform").
			Set("revoked_at = NULL").
			Set("updated_at = now()").
			Returning("*").
			Exec(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("register device: %w", err)
	}
	slog.Info("设备注册成功", "organization_id", identity.Organization.ID, "user_id", identity.User.ID, "device_id", device.ID, "platform", device.Platform)
	output := recordFromModel(device)
	return &output, nil
}
