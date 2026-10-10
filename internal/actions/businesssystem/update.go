//go:build server

package businesssystem

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// UpdateBusinessSystemAction 修改业务系统。
type UpdateBusinessSystemAction struct {
	db   *bun.DB
	test *TestConnectionAction
}

// NewUpdateBusinessSystemAction 创建业务系统修改操作。
func NewUpdateBusinessSystemAction(db *bun.DB, test *TestConnectionAction) *UpdateBusinessSystemAction {
	return &UpdateBusinessSystemAction{db: db, test: test}
}

// Execute 修改当前工作区中的业务系统，并以连接测试取得的工具目录替换已有目录。
func (a *UpdateBusinessSystemAction) Execute(ctx context.Context, identity *servermodels.Identity, businessSystemID string, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	existing, err := loadBusinessSystem(ctx, a.db, identity.Workspace.ID, businessSystemID, false)
	if err != nil {
		return nil, err
	}
	// 传输方式创建后不能修改。
	if existing.Transport != input.Connection.Transport {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"transport": ValidationTransportChanged}}
	}
	// 网络探测在写事务外执行，失败时不保存配置。
	connection, tools, err := a.test.Execute(ctx, input.Connection)
	if err != nil {
		return nil, err
	}
	var system *servermodels.BusinessSystem
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		current, err := loadBusinessSystem(ctx, tx, identity.Workspace.ID, businessSystemID, true)
		if err != nil {
			return err
		}
		if current.Transport != connection.Transport {
			return &ValidationError{Fields: map[string]ValidationCode{"transport": ValidationTransportChanged}}
		}
		// 写入新工具目录并结束进行中的更新；工具目录来源变化时清除工具设置，否则只保留新目录中仍存在的工具设置。
		current.Tools, current.ToolsRefreshID, current.ToolsFailure = tools, nil, ""
		if catalogSource(current.Connection) != catalogSource(connection.Connection) {
			current.ToolSettings = map[string]domain.BusinessToolSetting{}
		} else {
			current.ToolSettings = retainToolSettings(current.ToolSettings, tools)
		}
		current.Name, current.Connection, current.Credential = input.Name, connection.Connection, connection.Credential
		current.HeaderBindings = input.HeaderBindings
		if _, err := tx.NewUpdate().
			Model(current).
			Column("name", "connection", "credential", "header_bindings",
				"tools", "tools_refresh_id", "tools_failure", "tool_settings").
			Set("tools_updated_at = now()").
			Where("workspace_id = ?", identity.Workspace.ID).
			WherePK().
			Returning("*").
			Exec(ctx); err != nil {
			return err
		}
		system = current
		return nil
	})
	// 工作区内名称不区分大小写且保持唯一。
	if pgerr.UniqueViolationOn(err, "business_systems_workspace_name_unique") {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("update business system: %w", err)
	}
	output := recordFromModel(*system)
	return &output, nil
}
