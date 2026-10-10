//go:build server

package businesssystem

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadBusinessSystem 读取当前工作区中的业务系统。
func loadBusinessSystem(ctx context.Context, db bun.IDB, workspaceID, businessSystemID string, lock bool) (*servermodels.BusinessSystem, error) {
	system := &servermodels.BusinessSystem{}
	query := db.NewSelect().
		Model(system).
		Where("bs.id = ?", businessSystemID).
		Where("bs.workspace_id = ?", workspaceID)
	if lock {
		query = query.For("UPDATE")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return system, nil
}

// catalogSource 返回工具目录来源的标识：MCP 为服务地址，HTTP 为文档地址，粘贴的文档以同一标识表示。
func catalogSource(connection domain.BusinessSystemConnection) string {
	if connection.MCP != nil {
		return connection.MCP.URL
	}
	return connection.HTTP.SpecURL
}

// retainToolSettings 返回新工具目录中仍存在的工具的设置，并移除已不在参数定义中的参数绑定。
func retainToolSettings(settings map[string]domain.BusinessToolSetting, tools []domain.BusinessTool) map[string]domain.BusinessToolSetting {
	retained := make(map[string]domain.BusinessToolSetting, len(settings))
	for _, item := range tools {
		setting, ok := settings[item.Name]
		if !ok {
			continue
		}
		parameters := item.Parameters()
		setting.ParameterBindings = maps.Clone(setting.ParameterBindings)
		maps.DeleteFunc(setting.ParameterBindings, func(name string, _ domain.ContextValue) bool {
			return !slices.Contains(parameters, name)
		})
		if !setting.Empty() {
			retained[item.Name] = setting
		}
	}
	return retained
}

// recordFromModel 转换业务系统存储模型。
func recordFromModel(input servermodels.BusinessSystem) Record {
	return Record{
		ID: input.ID, Name: input.Name, Transport: input.Transport, Connection: input.Connection,
		Credential: input.Credential, HeaderBindings: input.HeaderBindings,
		Tools: input.Tools, ToolSettings: input.ToolSettings, ToolsUpdatedAt: input.ToolsUpdatedAt,
		ToolsUpdating: input.ToolsRefreshID != nil, ToolsFailure: input.ToolsFailure,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}
