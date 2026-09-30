//go:build !server && !ios && !android

package devicehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/mcp"
)

// organizationMCPConnections 经企业服务端列出运行绑定的企业 MCP 服务，返回经服务端代理调用的连接配置；列出失败时本次运行不加载企业服务。
func organizationMCPConnections(ctx context.Context, client appservice.DeviceRunBackend, meta appservice.RequestMeta, runID string) []agentruntime.MCPServer {
	list, err := client.ListDeviceRunMCPTools(ctx, meta, runID)
	if err != nil {
		slog.Warn("列出企业 MCP 服务失败，本次运行不加载企业服务", "agent_run_id", runID, "error", err)
		return nil
	}
	servers := make([]agentruntime.MCPServer, 0, len(list.Servers))
	for _, server := range list.Servers {
		connection := &organizationMCPConnection{client: client, meta: meta, runID: runID, serverID: server.ID, tools: make([]mcp.Tool, 0, len(server.Tools))}
		for _, tool := range server.Tools {
			connection.tools = append(connection.tools, mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		}
		servers = append(servers, agentruntime.MCPServer{
			Source: agentruntime.MCPSourceOrganization, ID: server.ID, Name: server.Name,
			Connect: func(context.Context) (agentruntime.MCPConnection, error) { return connection, nil },
		})
	}
	return servers
}

// organizationMCPConnection 经企业服务端代理调用一个企业 MCP 服务，工具目录在运行开始时一次读取。
type organizationMCPConnection struct {
	client   appservice.DeviceRunBackend
	meta     appservice.RequestMeta
	runID    string
	serverID string
	tools    []mcp.Tool
}

// Tools 返回运行开始时读取的工具目录。
func (c *organizationMCPConnection) Tools(context.Context) ([]mcp.Tool, error) { return c.tools, nil }

// Call 经企业服务端调用工具，服务连接失败与工具报告的失败作为错误返回。
func (c *organizationMCPConnection) Call(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	output, err := c.client.CallDeviceRunMCPTool(ctx, c.meta, c.runID, appservice.DeviceRunMCPToolCallInput{
		ServerID: c.serverID, ToolName: name, Arguments: arguments,
	})
	if err != nil {
		return "", err
	}
	if output.Error != "" {
		return "", errors.New(output.Error)
	}
	return output.Result, nil
}

// Close 没有需要释放的连接。
func (c *organizationMCPConnection) Close() error { return nil }
