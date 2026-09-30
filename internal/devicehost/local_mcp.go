//go:build !server && !ios && !android

package devicehost

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/localmcp"
	"github.com/runforyou-ai/cervi/internal/integration/localworkspace"
	"github.com/runforyou-ai/cervi/internal/integration/mcp"
	"github.com/runforyou-ai/cervi/pkg/outputbuffer"
)

const (
	// localMCPHandshakeTimeout 是本地 MCP 服务启动并返回工具目录的时限，首次启动可能需要下载依赖。
	localMCPHandshakeTimeout = 2 * time.Minute
	// localMCPStderrBytes 是试启动失败时保留的服务错误输出字节数。
	localMCPStderrBytes = 4 << 10
)

// localMCPServer 创建本地 MCP 服务的连接配置：SSE 与 Streamable HTTP 服务按地址与请求头连接；
// 本地进程在建立会话时于运行环境中启动，服务配置的环境变量叠加在运行环境之上，服务及其子进程在同一进程树中，会话关闭时一并终止。
// 服务的标准输出是协议通道，服务及其子进程调用的 npm 不输出安装摘要与提示；标准错误写入 stderr（为空时丢弃）。
func localMCPServer(server localmcp.Server, environment localworkspace.Environment, dir string, stderr io.Writer) agentruntime.MCPServer {
	connection := agentruntime.MCPServer{Source: agentruntime.MCPSourceLocal, ID: server.Name, Name: server.Name}
	switch server.Transport() {
	case localmcp.TypeSSE:
		connection.Config = mcp.Config{URL: server.URL, ServerType: domain.MCPServerTypeSSE, Headers: server.Headers}
		return connection
	case localmcp.TypeHTTP:
		connection.Config = mcp.Config{URL: server.URL, ServerType: domain.MCPServerTypeStreamableHTTP, Headers: server.Headers}
		return connection
	}
	variables := append(slices.Clone(environment.Variables), "NPM_CONFIG_LOGLEVEL=silent", "NPM_CONFIG_FUND=false", "NPM_CONFIG_UPDATE_NOTIFIER=false")
	for name, value := range server.Env {
		variables = append(variables, name+"="+value)
	}
	environment.Variables = variables
	start := func(ctx context.Context) (io.ReadCloser, io.WriteCloser, error) {
		process, err := localworkspace.StartProcess(ctx, environment, dir, stderr, server.Command, server.Args...)
		if err != nil {
			return nil, nil, err
		}
		return process.Stdout, process.Stdin, nil
	}
	connection.Config = mcp.Config{Start: start}
	connection.HandshakeTimeout = localMCPHandshakeTimeout
	return connection
}

// localMCPConnections 读取本地 MCP 配置并返回本次运行的连接配置，配置无法读取时不加载本地 MCP 服务。
func localMCPConnections(store *localmcp.Store, environment localworkspace.Environment, dir string) []agentruntime.MCPServer {
	servers, err := store.List()
	if err != nil {
		slog.Warn("读取本地 MCP 配置失败，本次运行不加载本地 MCP 服务", "error", err)
		return nil
	}
	connections := make([]agentruntime.MCPServer, 0, len(servers))
	for _, server := range servers {
		connections = append(connections, localMCPServer(server, environment, dir, nil))
	}
	return connections
}

// localMCPManager 在本次运行的运行环境中试启动并保存本地 MCP 服务。
type localMCPManager struct {
	store       *localmcp.Store
	environment localworkspace.Environment
	dir         string
}

// Add 试启动服务并读取工具目录，成功后保存配置并返回服务提供的工具名称；失败时返回原因与服务的错误输出。
func (m *localMCPManager) Add(ctx context.Context, input agentruntime.LocalMCPServer) ([]string, error) {
	server := localmcp.Server{Name: input.Name, Type: input.Type, Command: input.Command, Args: input.Args, Env: input.Env, URL: input.URL, Headers: input.Headers}
	if err := server.Validate(); err != nil {
		return nil, err
	}
	stderr := outputbuffer.New(0, localMCPStderrBytes)
	connection := localMCPServer(server, m.environment, m.dir, stderr)
	ctx, cancel := context.WithTimeout(ctx, localMCPHandshakeTimeout)
	defer cancel()
	session, err := connection.Open(ctx)
	if err != nil {
		return nil, startFailure(err, stderr)
	}
	tools, err := session.Tools(ctx)
	closeErr := session.Close()
	if err != nil {
		return nil, startFailure(err, stderr)
	}
	if closeErr != nil {
		slog.Warn("关闭试启动的本地 MCP 服务失败", "mcp_server", server.Name, "error", closeErr)
	}
	if err := m.store.Put(server); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names, nil
}

// Remove 删除服务配置，返回服务是否存在。
func (m *localMCPManager) Remove(_ context.Context, name string) (bool, error) {
	return m.store.Remove(name)
}

// Names 按名称顺序返回已添加的服务。
func (m *localMCPManager) Names(context.Context) ([]string, error) {
	servers, err := m.store.List()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Name)
	}
	return names, nil
}

// startFailure 返回试启动失败的原因，附带服务最后的错误输出。
func startFailure(err error, stderr *outputbuffer.Buffer) error {
	if output := strings.TrimSpace(stderr.String()); output != "" {
		return fmt.Errorf("服务启动失败：%w\n服务输出：\n%s", err, output)
	}
	return fmt.Errorf("服务启动失败：%w", err)
}
