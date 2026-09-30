//go:build server

package mcpserver

import (
	"context"
	"fmt"
	"time"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// CreateMCPServerAction 创建 MCP 服务。
type CreateMCPServerAction struct {
	db   *bun.DB
	test *TestConnectionAction
}

// NewCreateMCPServerAction 创建 MCP 服务操作。
func NewCreateMCPServerAction(db *bun.DB, test *TestConnectionAction) *CreateMCPServerAction {
	return &CreateMCPServerAction{db: db, test: test}
}

// Execute 在当前企业中创建 MCP 服务，并保存连接测试取得的工具目录。
func (a *CreateMCPServerAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	// 网络探测在写事务外执行，失败时不保存配置。
	tools, err := a.test.Execute(ctx, ConnectionInput{URL: input.URL, ServerType: input.ServerType, AuthorizationToken: input.AuthorizationToken})
	if err != nil {
		return nil, err
	}
	var mcpServer servermodels.MCPServer
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		now := time.Now()
		mcpServer = servermodels.MCPServer{
			OrganizationID: identity.Organization.ID, Name: input.Name,
			URL: input.URL, ServerType: input.ServerType, AuthorizationToken: input.AuthorizationToken,
			CustomerScoped: input.CustomerScoped, Tools: tools, ToolsUpdatedAt: &now,
		}
		_, err := tx.NewInsert().
			Model(&mcpServer).
			Column("organization_id", "name", "url", "server_type", "authorization_token", "customer_scoped", "tools", "tools_updated_at").
			Returning("*").
			Exec(ctx)
		return err
	})
	// 企业内名称不区分大小写且保持唯一。
	if pgerr.UniqueViolationOn(err, "mcp_servers_organization_name_unique") {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("create MCP server: %w", err)
	}
	output := recordFromModel(mcpServer)
	return &output, nil
}
