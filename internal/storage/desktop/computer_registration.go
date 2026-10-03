//go:build !server && !ios && !android

package desktop

import (
	"context"
	"database/sql"
	"errors"
	"time"

	desktopmodels "github.com/runforyou-ai/luway/internal/storage/desktop/models"
	nativemodels "github.com/runforyou-ai/luway/internal/storage/native/models"
	"uuid"
)

const computerInstallIDSettingKey = "computer_install_id"

// ComputerInstallID 读取本机执行器安装标识，尚未生成时创建并保存。
func (s *Store) ComputerInstallID(ctx context.Context) (string, error) {
	setting := &nativemodels.AppSetting{}
	err := s.DB().NewSelect().
		Model(setting).
		Where("key = ?", computerInstallIDSettingKey).
		Scan(ctx)
	if err == nil {
		return setting.Value, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	installID := uuid.NewV7().String()
	created := &nativemodels.AppSetting{
		Key:       computerInstallIDSettingKey,
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
		Where("key = ?", computerInstallIDSettingKey).
		Scan(ctx); err != nil {
		return "", err
	}
	return stored.Value, nil
}

// LoadComputerRegistrations 读取本机在指定服务器上为指定账号在各工作区注册的电脑，按工作区编号索引。
func (s *Store) LoadComputerRegistrations(ctx context.Context, serverURL, accountID string) (map[string]desktopmodels.ComputerRegistration, error) {
	var registrations []desktopmodels.ComputerRegistration
	if err := s.DB().NewSelect().
		Model(&registrations).
		Where("server_url = ?", serverURL).
		Where("account_id = ?", accountID).
		Scan(ctx); err != nil {
		return nil, err
	}
	computers := make(map[string]desktopmodels.ComputerRegistration, len(registrations))
	for _, registration := range registrations {
		computers[registration.OrganizationID] = registration
	}
	return computers, nil
}

// ListComputerRegistrations 读取本机在全部服务器上为全部账号注册的电脑。
func (s *Store) ListComputerRegistrations(ctx context.Context) ([]desktopmodels.ComputerRegistration, error) {
	registrations := make([]desktopmodels.ComputerRegistration, 0)
	if err := s.DB().NewSelect().Model(&registrations).
		OrderExpr("server_url, account_id, organization_id").
		Scan(ctx); err != nil {
		return nil, err
	}
	return registrations, nil
}

// SaveComputerRegistration 保存本机在指定服务器上为指定账号在指定工作区注册的电脑与电脑凭据。
func (s *Store) SaveComputerRegistration(ctx context.Context, registration desktopmodels.ComputerRegistration) error {
	registration.RegisteredAt = time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB().NewInsert().
		Model(&registration).
		Column("server_url", "account_id", "organization_id", "computer_id", "credential", "registered_at").
		On("CONFLICT (server_url, account_id, organization_id) DO UPDATE").
		Set("computer_id = EXCLUDED.computer_id").
		Set("credential = EXCLUDED.credential").
		Set("registered_at = EXCLUDED.registered_at").
		Exec(ctx)
	return err
}

// DeleteComputerRegistration 删除本机在指定服务器上为指定账号在指定工作区的注册结果，电脑编号非空时只删除该电脑的注册。
func (s *Store) DeleteComputerRegistration(ctx context.Context, serverURL, accountID, organizationID, computerID string) error {
	query := s.DB().NewDelete().
		Model((*desktopmodels.ComputerRegistration)(nil)).
		Where("server_url = ?", serverURL).
		Where("account_id = ?", accountID).
		Where("organization_id = ?", organizationID)
	if computerID != "" {
		query = query.Where("computer_id = ?", computerID)
	}
	_, err := query.Exec(ctx)
	return err
}
