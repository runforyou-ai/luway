//go:build server

package businesssystem

import (
	"context"
	"fmt"
	"slices"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateToolSettingAction 保存业务系统中一个工具的管理员设置。
type UpdateToolSettingAction struct{ db *bun.DB }

// NewUpdateToolSettingAction 创建工具设置保存操作。
func NewUpdateToolSettingAction(db *bun.DB) *UpdateToolSettingAction {
	return &UpdateToolSettingAction{db: db}
}

// Execute 以输入替换当前工具目录中一个工具的设置，与默认一致的设置不保存，不触发连接测试与目录更新。
func (a *UpdateToolSettingAction) Execute(ctx context.Context, identity *servermodels.Identity, businessSystemID string, input ToolSettingInput) (*Record, error) {
	var system *servermodels.BusinessSystem
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		current, err := loadBusinessSystem(ctx, tx, identity.Workspace.ID, businessSystemID, true)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(current.Tools, func(item domain.BusinessTool) bool { return item.Name == input.ToolName })
		if index < 0 {
			return ErrToolNotFound
		}
		if fields := validateToolSetting(current.Tools[index], input.Setting); len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		if current.ToolSettings == nil {
			current.ToolSettings = make(map[string]domain.BusinessToolSetting)
		}
		if input.Setting.Empty() {
			delete(current.ToolSettings, input.ToolName)
		} else {
			current.ToolSettings[input.ToolName] = input.Setting
		}
		if _, err := tx.NewUpdate().Model(current).Column("tool_settings").
			WherePK().Returning("*").Exec(ctx); err != nil {
			return err
		}
		system = current
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update business tool setting: %w", err)
	}
	output := recordFromModel(*system)
	return &output, nil
}
