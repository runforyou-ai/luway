//go:build server

package mcpserver

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
)

// TestConnectionAction 测试 MCP 草稿配置的工具发现能力。
type TestConnectionAction struct{ client mcpintegration.Discoverer }

// NewTestConnectionAction 创建 MCP 连接测试操作。
func NewTestConnectionAction(client mcpintegration.Discoverer) *TestConnectionAction {
	return &TestConnectionAction{client: client}
}

// Execute 校验配置并执行只读连接测试，返回服务提供的完整工具目录。
func (a *TestConnectionAction) Execute(ctx context.Context, input ConnectionInput) ([]domain.MCPTool, error) {
	input, fields := normalizeConnectionInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	return a.client.Discover(ctx, mcpintegration.Config{URL: input.URL, ServerType: input.ServerType, AuthorizationToken: input.AuthorizationToken})
}
