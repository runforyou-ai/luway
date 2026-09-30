//go:build !server && !ios && !android

package desktop

import (
	"context"
	"database/sql"
	"errors"
	"time"

	desktopmodels "github.com/runforyou-ai/cervi/internal/storage/desktop/models"
	nativemodels "github.com/runforyou-ai/cervi/internal/storage/native/models"
	"uuid"
)

const deviceInstallIDSettingKey = "device_install_id"

// DeviceInstallID 读取本机安装标识，尚未生成时创建并保存。
func (s *Store) DeviceInstallID(ctx context.Context) (string, error) {
	setting := &nativemodels.AppSetting{}
	err := s.DB().NewSelect().
		Model(setting).
		Where("key = ?", deviceInstallIDSettingKey).
		Scan(ctx)
	if err == nil {
		return setting.Value, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	installID := uuid.NewV7().String()
	created := &nativemodels.AppSetting{
		Key:       deviceInstallIDSettingKey,
		Value:     installID,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	// 并发写入时保留先落库的标识，本机安装标识始终唯一。
	if _, err := s.DB().NewInsert().
		Model(created).
		Column("key", "value", "updated_at").
		On("CONFLICT (key) DO NOTHING").
		Exec(ctx); err != nil {
		return "", err
	}
	stored := &nativemodels.AppSetting{}
	if err := s.DB().NewSelect().
		Model(stored).
		Where("key = ?", deviceInstallIDSettingKey).
		Scan(ctx); err != nil {
		return "", err
	}
	return stored.Value, nil
}

// LoadDeviceRegistrations 读取本机在指定服务器上为指定账号在各工作区注册的设备编号，按工作区编号索引。
func (s *Store) LoadDeviceRegistrations(ctx context.Context, serverURL, accountID string) (map[string]string, error) {
	var registrations []desktopmodels.DeviceRegistration
	if err := s.DB().NewSelect().
		Model(&registrations).
		Where("server_url = ?", serverURL).
		Where("account_id = ?", accountID).
		Scan(ctx); err != nil {
		return nil, err
	}
	devices := make(map[string]string, len(registrations))
	for _, registration := range registrations {
		devices[registration.OrganizationID] = registration.DeviceID
	}
	return devices, nil
}

// SaveDeviceRegistration 保存本机在指定服务器上为指定账号在指定工作区注册的设备编号。
func (s *Store) SaveDeviceRegistration(ctx context.Context, serverURL, accountID, organizationID, deviceID string) error {
	registration := &desktopmodels.DeviceRegistration{
		ServerURL:      serverURL,
		AccountID:      accountID,
		OrganizationID: organizationID,
		DeviceID:       deviceID,
		RegisteredAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	_, err := s.DB().NewInsert().
		Model(registration).
		Column("server_url", "account_id", "organization_id", "device_id", "registered_at").
		On("CONFLICT (server_url, account_id, organization_id) DO UPDATE").
		Set("device_id = EXCLUDED.device_id").
		Set("registered_at = EXCLUDED.registered_at").
		Exec(ctx)
	return err
}

// DeleteDeviceRegistration 删除本机在指定服务器上为指定账号在指定工作区的注册结果。
func (s *Store) DeleteDeviceRegistration(ctx context.Context, serverURL, accountID, organizationID string) error {
	_, err := s.DB().NewDelete().
		Model((*desktopmodels.DeviceRegistration)(nil)).
		Where("server_url = ?", serverURL).
		Where("account_id = ?", accountID).
		Where("organization_id = ?", organizationID).
		Exec(ctx)
	return err
}
