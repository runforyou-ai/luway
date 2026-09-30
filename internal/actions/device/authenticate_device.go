//go:build server

package device

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// AuthenticateDeviceAction 校验请求携带的设备属于当前用户且未撤销，并记录设备最近在线时间。
type AuthenticateDeviceAction struct {
	db *bun.DB
}

// NewAuthenticateDeviceAction 创建设备认证操作。
func NewAuthenticateDeviceAction(db *bun.DB) *AuthenticateDeviceAction {
	return &AuthenticateDeviceAction{db: db}
}

// Execute 返回当前用户名下未撤销的指定设备，并按 30 秒粒度刷新最近在线时间。
func (a *AuthenticateDeviceAction) Execute(ctx context.Context, identity *servermodels.Identity, deviceID string) (*Record, error) {
	if !common.ValidUUID(deviceID) {
		return nil, ErrNotFound
	}
	device := servermodels.Device{}
	err := a.db.NewSelect().Model(&device).
		Where("d.organization_id = ? AND d.user_id = ? AND d.id = ?", identity.Organization.ID, identity.User.ID, deviceID).
		Where("d.revoked_at IS NULL").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load request device: %w", err)
	}
	if _, err := a.db.NewUpdate().Model((*servermodels.Device)(nil)).
		Set("last_seen_at = now()").
		Where("id = ?", device.ID).
		Where("last_seen_at IS NULL OR last_seen_at < now() - interval '30 seconds'").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("touch device last seen: %w", err)
	}
	output := recordFromModel(device)
	return &output, nil
}
