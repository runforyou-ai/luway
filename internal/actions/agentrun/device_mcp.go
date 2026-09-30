//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// deviceMCPHandshakeTimeout 限制代理设备连接企业 MCP 服务并读取工具目录的时间。
const deviceMCPHandshakeTimeout = 15 * time.Second

// ErrDeviceRunMCPToolNotFound 表示运行未绑定指定的企业 MCP 服务或服务不提供该工具。
var ErrDeviceRunMCPToolNotFound = errors.New("device agent run MCP tool not found")

// DeviceMCPServer 是设备运行可用的一个企业 MCP 服务及其工具目录。
type DeviceMCPServer struct {
	ID    string
	Name  string
	Tools []mcpintegration.Tool
}

// DeviceMCPToolResult 是代理设备调用企业 MCP 工具的结果，Error 非空表示服务连接失败或工具报告失败；连接与协议失败只说明失败类型，不含服务配置。
type DeviceMCPToolResult struct {
	Result string
	Error  string
}

// ListDeviceRunMCPTools 并行连接设备持有运行绑定的企业 MCP 服务并读取工具目录，按服务顺序返回，不可用的服务跳过；连接留给本次运行后续的工具调用复用。
func (a *ExecuteAction) ListDeviceRunMCPTools(ctx context.Context, device RunDevice, runID string) ([]DeviceMCPServer, error) {
	servers, err := a.deviceRunMCPServers(ctx, device, runID)
	if err != nil {
		return nil, err
	}
	catalogs := make([]*DeviceMCPServer, len(servers))
	var group sync.WaitGroup
	for i, server := range servers {
		group.Go(func() {
			handshakeCtx, cancel := context.WithTimeout(ctx, deviceMCPHandshakeTimeout)
			defer cancel()
			connection, done, err := a.deviceMCP.use(handshakeCtx, runID, server)
			if err != nil {
				slog.Warn("企业 MCP 服务不可用，设备运行跳过其工具", "agent_run_id", runID, "mcp_server", server.Name, "error", err)
				return
			}
			tools, err := connection.Tools(handshakeCtx)
			done(mcpintegration.ConnectionLost(err) && handshakeCtx.Err() == nil)
			if err != nil {
				slog.Warn("读取企业 MCP 服务工具目录失败，设备运行跳过其工具", "agent_run_id", runID, "mcp_server", server.Name, "error", err)
				return
			}
			tools = slices.DeleteFunc(tools, func(tool mcpintegration.Tool) bool {
				return server.Tools != nil && !slices.Contains(server.Tools, tool.Name)
			})
			catalogs[i] = &DeviceMCPServer{ID: server.ID, Name: server.Name, Tools: tools}
		})
	}
	group.Wait()
	output := make([]DeviceMCPServer, 0, len(servers))
	for _, catalog := range catalogs {
		if catalog != nil {
			output = append(output, *catalog)
		}
	}
	return output, nil
}

// CallDeviceRunMCPTool 为设备持有的运行调用其绑定的企业 MCP 服务中的工具，复用本次运行已建立的连接，会话不可用时丢弃该连接。
func (a *ExecuteAction) CallDeviceRunMCPTool(ctx context.Context, device RunDevice, runID, serverID, toolName string, arguments json.RawMessage) (DeviceMCPToolResult, error) {
	servers, err := a.deviceRunMCPServers(ctx, device, runID)
	if err != nil {
		return DeviceMCPToolResult{}, err
	}
	index := slices.IndexFunc(servers, func(server agentruntime.MCPServer) bool { return server.ID == serverID })
	if index < 0 || (servers[index].Tools != nil && !slices.Contains(servers[index].Tools, toolName)) {
		return DeviceMCPToolResult{}, ErrDeviceRunMCPToolNotFound
	}
	server := servers[index]
	connection, done, err := a.deviceMCP.use(ctx, runID, server)
	if err != nil {
		if ctx.Err() != nil {
			return DeviceMCPToolResult{}, ctx.Err()
		}
		slog.Warn("连接企业 MCP 服务失败", "agent_run_id", runID, "mcp_server", server.Name, "error", err)
		return DeviceMCPToolResult{Error: deviceMCPFailure("连接", server.Name, err)}, nil
	}
	result, err := connection.Call(ctx, toolName, arguments)
	// 会话已不可用时丢弃连接，请求取消、错误响应与工具自身报告的失败保留连接。
	done(mcpintegration.ConnectionLost(err) && ctx.Err() == nil)
	if err != nil {
		if ctx.Err() != nil {
			return DeviceMCPToolResult{}, ctx.Err()
		}
		// 工具自身报告的失败原样交回，连接与协议失败只给出失败类型。
		if _, _, classified := connectiontest.Details(err); !classified {
			return DeviceMCPToolResult{Error: err.Error()}, nil
		}
		slog.Warn("调用企业 MCP 工具失败", "agent_run_id", runID, "mcp_server", server.Name, "tool_name", toolName, "error", err)
		return DeviceMCPToolResult{Error: deviceMCPFailure("调用", server.Name, err)}, nil
	}
	return DeviceMCPToolResult{Result: result}, nil
}

// deviceMCPFailure 返回交给设备的企业 MCP 服务失败说明，只含服务名称与失败类型，不含服务地址等配置。
func deviceMCPFailure(action, serverName string, err error) string {
	if _, kind, classified := connectiontest.Details(err); classified {
		return fmt.Sprintf("%s MCP 服务 %s 失败（%s）", action, serverName, kind)
	}
	return fmt.Sprintf("%s MCP 服务 %s 失败", action, serverName)
}

// deviceRunMCPServers 读取设备持有有效租约的运行绑定的企业 MCP 服务。
func (a *ExecuteAction) deviceRunMCPServers(ctx context.Context, device RunDevice, runID string) ([]agentruntime.MCPServer, error) {
	run, err := a.requireDeviceLease(ctx, device, runID)
	if err != nil {
		return nil, err
	}
	loaded, err := loadRunMCPServers(ctx, a.db, run)
	if err != nil {
		return nil, fmt.Errorf("load device agent run mcp servers: %w", err)
	}
	return loaded.Servers, nil
}
