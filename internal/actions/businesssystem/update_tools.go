//go:build server

package businesssystem

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// UpdateToolsAction 获取完整工具目录并收敛更新状态。
type UpdateToolsAction struct {
	db        *bun.DB
	connector *Connector
}

// NewUpdateToolsAction 创建业务系统工具目录更新执行器。
func NewUpdateToolsAction(db *bun.DB, connector *Connector) *UpdateToolsAction {
	return &UpdateToolsAction{db: db, connector: connector}
}

// Execute 读取当前批次配置，在网络请求结束后原子写回目录。
func (a *UpdateToolsAction) Execute(ctx context.Context, input RefreshToolsInput) error {
	record, err := loadBusinessSystem(ctx, a.db, input.WorkspaceID, input.BusinessSystemID, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.ToolsRefreshID == nil || *record.ToolsRefreshID != input.RefreshID {
		return nil
	}
	discovery, err := a.connector.Discover(ctx, ConnectionInput{Transport: record.Transport, Connection: record.Connection, Credential: record.Credential})
	// 文档无效或没有可调用接口按服务未返回可用工具目录处理。
	if _, invalid := errors.AsType[*ValidationError](err); invalid {
		err = connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureProtocol, err)
	}
	if err != nil {
		return err
	}
	return a.finish(ctx, input, discovery.Tools, "")
}

// FinalizeFailure 在任务最终失败时保留已有目录并结束更新状态。
func (a *UpdateToolsAction) FinalizeFailure(ctx context.Context, input RefreshToolsInput, runErr error) error {
	_, kind, classified := connectiontest.Details(runErr)
	if !classified {
		kind = connectiontest.FailureUnavailable
	}
	return a.finish(ctx, input, nil, string(kind))
}

// finish 校验更新批次，拒绝已被保存操作或新的更新批次替代的结果。
func (a *UpdateToolsAction) finish(ctx context.Context, input RefreshToolsInput, tools []domain.BusinessTool, failure string) error {
	changed := false
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		record, err := loadBusinessSystem(ctx, tx, input.WorkspaceID, input.BusinessSystemID, true)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if record.ToolsRefreshID == nil || *record.ToolsRefreshID != input.RefreshID {
			return nil
		}
		record.ToolsRefreshID, record.ToolsFailure = nil, failure
		update := tx.NewUpdate().Model(record).Column("tools_refresh_id", "tools_failure")
		if failure == "" {
			record.Tools = tools
			record.ToolSettings = retainToolSettings(record.ToolSettings, tools)
			update = update.Column("tools", "tool_settings").Set("tools_updated_at = now()")
		}
		if _, err := update.WherePK().Exec(ctx); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err == nil && changed {
		attributes := []any{"workspace_id", input.WorkspaceID, "business_system_id", input.BusinessSystemID, "refresh_id", input.RefreshID}
		if failure == "" {
			slog.InfoContext(ctx, "业务系统工具目录更新完成", append(attributes, "tool_count", len(tools))...)
		} else {
			slog.WarnContext(ctx, "业务系统工具目录更新失败", append(attributes, "kind", failure)...)
		}
	}
	return err
}
