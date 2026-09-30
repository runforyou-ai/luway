//go:build server

package mcpserver

import (
	"context"
	"fmt"
	"time"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// UpdateMCPServerAction 修改 MCP 服务。
type UpdateMCPServerAction struct {
	db   *bun.DB
	test *TestConnectionAction
}

// NewUpdateMCPServerAction 创建 MCP 服务修改操作。
func NewUpdateMCPServerAction(db *bun.DB, test *TestConnectionAction) *UpdateMCPServerAction {
	return &UpdateMCPServerAction{db: db, test: test}
}

// Execute 修改当前企业中的 MCP 服务，并以连接测试取得的工具目录替换已有目录。
func (a *UpdateMCPServerAction) Execute(ctx context.Context, identity *servermodels.Identity, mcpServerID string, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	if _, err := loadMCPServer(ctx, a.db, identity.Organization.ID, mcpServerID, false); err != nil {
		return nil, err
	}
	// 网络探测在写事务外执行，失败时不保存配置。
	tools, err := a.test.Execute(ctx, ConnectionInput{URL: input.URL, ServerType: input.ServerType, AuthorizationToken: input.AuthorizationToken})
	if err != nil {
		return nil, err
	}
	var mcpServer *servermodels.MCPServer
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		current, err := loadMCPServer(ctx, tx, identity.Organization.ID, mcpServerID, true)
		if err != nil {
			return err
		}
		// 写入新工具目录并结束进行中的更新；地址变化时清除工具用途，否则只保留新目录中仍存在的工具用途。
		now := time.Now()
		current.Tools, current.ToolsUpdatedAt, current.ToolsRefreshID, current.ToolsFailure = tools, &now, nil, ""
		if current.URL != input.URL {
			current.ToolPurposes = map[string]domain.MCPToolPurpose{}
		} else {
			current.ToolPurposes = retainToolPurposes(current.ToolPurposes, tools)
		}
		// 助理不接待客户，服务改为按客户查询时从助理的配置中移除。
		if input.CustomerScoped && !current.CustomerScoped {
			if _, err := agentaction.RemoveMCPServerFromAssistants(ctx, tx, identity, current.ID); err != nil {
				return err
			}
		}
		current.Name = input.Name
		current.URL = input.URL
		current.ServerType = input.ServerType
		current.AuthorizationToken = input.AuthorizationToken
		current.CustomerScoped = input.CustomerScoped
		if _, err := tx.NewUpdate().
			Model(current).
			Column("name", "url", "server_type", "authorization_token", "customer_scoped", "tools", "tools_updated_at", "tools_refresh_id", "tools_failure", "tool_purposes").
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			WherePK().
			Returning("*").
			Exec(ctx); err != nil {
			return err
		}
		mcpServer = current
		return nil
	})
	// 企业内名称不区分大小写且保持唯一。
	if pgerr.UniqueViolationOn(err, "mcp_servers_organization_name_unique") {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("update MCP server: %w", err)
	}
	output := recordFromModel(*mcpServer)
	return &output, nil
}
