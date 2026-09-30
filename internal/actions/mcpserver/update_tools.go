//go:build server

package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// UpdateToolsAction 获取完整工具目录并收敛更新状态。
type UpdateToolsAction struct {
	db     *bun.DB
	client mcpintegration.Discoverer
}

// NewUpdateToolsAction 创建 MCP 工具更新执行器。
func NewUpdateToolsAction(db *bun.DB, client mcpintegration.Discoverer) *UpdateToolsAction {
	return &UpdateToolsAction{db: db, client: client}
}

// Execute 读取当前批次配置，在网络请求结束后原子写回目录。
func (a *UpdateToolsAction) Execute(ctx context.Context, input RefreshToolsInput) error {
	record, err := loadMCPServer(ctx, a.db, input.OrganizationID, input.MCPServerID, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.ToolsRefreshID == nil || *record.ToolsRefreshID != input.RefreshID {
		return nil
	}
	tools, err := a.client.Discover(ctx, mcpintegration.Config{URL: record.URL, ServerType: record.ServerType, AuthorizationToken: record.AuthorizationToken})
	if err != nil {
		return err
	}
	return a.finish(ctx, input, tools, "")
}

// FinalizeFailure 在任务最终失败时保留已有目录并结束更新状态。
func (a *UpdateToolsAction) FinalizeFailure(ctx context.Context, input RefreshToolsInput, runErr error) error {
	_, kind, classified := connectiontest.Details(runErr)
	if !classified {
		kind = connectiontest.FailureUnavailable
	}
	return a.finish(ctx, input, nil, string(kind))
}

// finish 校验更新批次和任务租约，拒绝已被保存操作或新 Worker 替代的结果。
func (a *UpdateToolsAction) finish(ctx context.Context, input RefreshToolsInput, tools []domain.MCPTool, failure string) error {
	changed := false
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		record, err := loadMCPServer(ctx, tx, input.OrganizationID, input.MCPServerID, true)
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
		columns := []string{"tools_refresh_id", "tools_failure"}
		if failure == "" {
			now := time.Now()
			record.Tools, record.ToolsUpdatedAt = tools, &now
			record.ToolPurposes = retainToolPurposes(record.ToolPurposes, tools)
			columns = append(columns, "tools", "tools_updated_at", "tool_purposes")
		}
		if _, err := tx.NewUpdate().Model(record).Column(columns...).Set("updated_at = now()").WherePK().Exec(ctx); err != nil {
			return err
		}
		changed = true
		return servertask.LockExecution(ctx, tx)
	})
	if err == nil && changed {
		attributes := []any{"organization_id", input.OrganizationID, "mcp_server_id", input.MCPServerID, "refresh_id", input.RefreshID}
		if failure == "" {
			slog.Info("MCP 工具更新完成", append(attributes, "tool_count", len(tools))...)
		} else {
			slog.Warn("MCP 工具更新失败", append(attributes, "kind", failure)...)
		}
	}
	return err
}
