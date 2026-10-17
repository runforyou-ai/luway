//go:build server

package platform

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Settings 定义平台管理员可修改的注册策略与自建工作区额度。
type Settings struct {
	RegistrationPolicy     domain.RegistrationPolicy
	WorkspaceCreationQuota int
}

// settingsFromModel 读取平台行中的平台级策略。
func settingsFromModel(platform *servermodels.Platform) Settings {
	return Settings{
		RegistrationPolicy:     domain.RegistrationPolicy(platform.RegistrationPolicy),
		WorkspaceCreationQuota: platform.WorkspaceCreationQuota,
	}
}

// Licensed 读取平台是否持有未过期授权（避免与 license 包循环依赖）。
func Licensed(ctx context.Context, db bun.IDB) (bool, error) {
	var active bool
	err := db.NewSelect().TableExpr("licenses AS lic").
		ColumnExpr("lic.expires_at > now()").
		Where("lic.deployment_id = (SELECT pf.deployment_id FROM platforms AS pf LIMIT 1)").
		Scan(ctx, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return active, nil
}

// SettingsQuery 读取平台级策略。
type SettingsQuery struct {
	db *bun.DB
}

// NewSettingsQuery 创建平台级策略查询。
func NewSettingsQuery(db *bun.DB) *SettingsQuery {
	return &SettingsQuery{db: db}
}

// Execute 返回当前注册策略与自建工作区额度（未授权时额度对外视为 0）。
func (q *SettingsQuery) Execute(ctx context.Context) (Settings, error) {
	platform, err := Load(ctx, q.db)
	if err != nil {
		return Settings{}, err
	}
	settings := settingsFromModel(platform)
	ok, err := Licensed(ctx, q.db)
	if err != nil {
		return Settings{}, err
	}
	if !ok {
		settings.WorkspaceCreationQuota = 0
	}
	return settings, nil
}

// Update 在事务内确认操作者仍是有效平台管理员并锁定平台行，执行修改后返回最新的平台设置。
func Update(ctx context.Context, db *bun.DB, operator *servermodels.AccountIdentity, update func(context.Context, bun.Tx, *servermodels.Platform) error) (Settings, error) {
	var output Settings
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := LockActiveAdmins(ctx, tx, operator); err != nil {
			return err
		}
		platform, err := Lock(ctx, tx)
		if err != nil {
			return err
		}
		if err := update(ctx, tx, platform); err != nil {
			return err
		}
		output = settingsFromModel(platform)
		return nil
	})
	if err != nil {
		return Settings{}, err
	}
	return output, nil
}
