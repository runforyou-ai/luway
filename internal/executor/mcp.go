//go:build !server && !ios && !android

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/localmcp"
	"github.com/runforyou-ai/luway/internal/integration/localworkspace"
	"github.com/runforyou-ai/luway/internal/integration/mcp"
	"github.com/runforyou-ai/luway/pkg/outputbuffer"
)

const (
	// mcpHandshakeTimeout 是本地 MCP 服务启动并返回工具目录的时限，首次启动可能需要下载依赖。
	mcpHandshakeTimeout = 2 * time.Minute
	// mcpStderrBytes 是试启动失败时保留的服务错误输出字节数。
	mcpStderrBytes = 4 << 10
)

// mcpServer 是一个本机 MCP 服务的配置、会话与工具目录。
type mcpServer struct {
	config localmcp.Server
	// connect 串行化同一服务的连接与调用前的会话检查。
	connect sync.Mutex
	session *mcp.Session
	tools   []mcp.Tool
}

// mcpPool 保持这台电脑上本机 MCP 服务的长连接，本地进程以会话文件夹根目录为工作目录。
type mcpPool struct {
	dir         string
	environment func() localworkspace.Environment
	ctx         context.Context
	cancel      context.CancelFunc

	mu      sync.Mutex
	loaded  bool
	servers map[string]*mcpServer
}

// newMCPPool 创建本机 MCP 连接池。
func newMCPPool(dir string, environment func() localworkspace.Environment) *mcpPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &mcpPool{dir: dir, environment: environment, ctx: ctx, cancel: cancel, servers: map[string]*mcpServer{}}
}

// synced 判断是否已按配置连接过本机 MCP 服务。
func (p *mcpPool) synced() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loaded
}

// sync 按配置增删本机 MCP 服务，配置变化的服务重新连接，新服务并发连接并读取工具目录，连接失败的服务不列入工具目录。
func (p *mcpPool) sync(configs []localmcp.Server) {
	p.mu.Lock()
	wanted := make(map[string]localmcp.Server, len(configs))
	for _, config := range configs {
		wanted[config.Name] = config
	}
	stale := make([]*mcpServer, 0)
	for name, server := range p.servers {
		if config, ok := wanted[name]; !ok || !reflect.DeepEqual(config, server.config) {
			stale = append(stale, server)
			delete(p.servers, name)
		}
	}
	fresh := make([]*mcpServer, 0)
	for name, config := range wanted {
		if p.servers[name] == nil {
			server := &mcpServer{config: config}
			p.servers[name] = server
			fresh = append(fresh, server)
		}
	}
	p.loaded = true
	p.mu.Unlock()
	for _, server := range stale {
		server.close()
	}
	var group sync.WaitGroup
	for _, server := range fresh {
		group.Go(func() {
			server.connect.Lock()
			defer server.connect.Unlock()
			if err := server.open(p.ctx, p.environment(), p.dir); err != nil {
				slog.Warn("连接本地 MCP 服务失败", "mcp_server", server.config.Name, "error", err)
			}
		})
	}
	group.Wait()
}

// catalog 按名称顺序返回已连接服务的工具目录。
func (p *mcpPool) catalog() []domain.ComputerMCPServer {
	p.mu.Lock()
	servers := slices.Collect(maps.Values(p.servers))
	p.mu.Unlock()
	catalog := make([]domain.ComputerMCPServer, 0, len(servers))
	for _, server := range servers {
		server.connect.Lock()
		tools := slices.Clone(server.tools)
		connected := server.session != nil
		server.connect.Unlock()
		if !connected {
			continue
		}
		entry := domain.ComputerMCPServer{Name: server.config.Name, Tools: make([]domain.ComputerMCPTool, 0, len(tools))}
		for _, tool := range tools {
			entry.Tools = append(entry.Tools, domain.ComputerMCPTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		}
		catalog = append(catalog, entry)
	}
	slices.SortFunc(catalog, func(left, right domain.ComputerMCPServer) int { return strings.Compare(left.Name, right.Name) })
	return catalog
}

// call 调用本机 MCP 服务的工具，会话已断开时重新连接后调用一次。
func (p *mcpPool) call(ctx context.Context, name, tool string, arguments json.RawMessage) (string, error) {
	p.mu.Lock()
	server := p.servers[name]
	p.mu.Unlock()
	if server == nil {
		return "", fmt.Errorf("这台电脑上没有名为 %s 的本地 MCP 服务", name)
	}
	server.connect.Lock()
	if server.session == nil {
		if err := server.open(p.ctx, p.environment(), p.dir); err != nil {
			server.connect.Unlock()
			return "", fmt.Errorf("无法启动本地 MCP 服务 %s：%w", name, err)
		}
	}
	session := server.session
	server.connect.Unlock()
	result, err := session.Call(ctx, tool, arguments)
	if err != nil && mcp.ConnectionLost(err) {
		// 会话已不可用时丢弃，下次调用重新连接。
		server.connect.Lock()
		if server.session == session {
			server.session = nil
			_ = session.Close()
		}
		server.connect.Unlock()
	}
	if err != nil {
		return "", fmt.Errorf("调用 %s 的工具 %s 失败：%w", name, tool, err)
	}
	return result, nil
}

// close 关闭全部会话。
func (p *mcpPool) close() {
	p.mu.Lock()
	servers := slices.Collect(maps.Values(p.servers))
	p.servers = map[string]*mcpServer{}
	p.mu.Unlock()
	for _, server := range servers {
		server.close()
	}
	p.cancel()
}

// open 建立会话并读取工具目录，调用方持有 connect。
func (s *mcpServer) open(ctx context.Context, environment localworkspace.Environment, dir string) error {
	session, tools, err := openMCPServer(ctx, s.config, environment, dir, nil)
	if err != nil {
		return err
	}
	s.session, s.tools = session, tools
	return nil
}

// close 关闭会话。
func (s *mcpServer) close() {
	s.connect.Lock()
	defer s.connect.Unlock()
	if s.session != nil {
		_ = s.session.Close()
		s.session = nil
	}
}

// ProbeMCPServer 试启动本地 MCP 服务并读取工具目录后关闭，失败时返回原因与服务最后的错误输出。
func ProbeMCPServer(ctx context.Context, server localmcp.Server, environment localworkspace.Environment, dir string) error {
	if err := server.Validate(); err != nil {
		return err
	}
	stderr := outputbuffer.New(0, mcpStderrBytes)
	session, _, err := openMCPServer(ctx, server, environment, dir, stderr)
	if err != nil {
		if output := strings.TrimSpace(stderr.String()); output != "" {
			return fmt.Errorf("服务启动失败：%w\n服务输出：\n%s", err, output)
		}
		return fmt.Errorf("服务启动失败：%w", err)
	}
	if err := session.Close(); err != nil {
		slog.Warn("关闭试启动的本地 MCP 服务失败", "mcp_server", server.Name, "error", err)
	}
	return nil
}

// openMCPServer 在时限内连接本地 MCP 服务并读取工具目录：SSE 与 Streamable HTTP 服务按地址与请求头连接；
// 本地进程在运行环境中启动，服务配置的环境变量叠加在运行环境之上，服务及其子进程在 ctx 结束或会话关闭时一并终止，标准错误写入 stderr（为空时丢弃）。
func openMCPServer(ctx context.Context, server localmcp.Server, environment localworkspace.Environment, dir string, stderr io.Writer) (*mcp.Session, []mcp.Tool, error) {
	config := mcp.Config{}
	switch server.Transport() {
	case localmcp.TypeSSE:
		config = mcp.Config{URL: server.URL, ServerType: domain.MCPServerTypeSSE, Headers: server.Headers}
	case localmcp.TypeHTTP:
		config = mcp.Config{URL: server.URL, ServerType: domain.MCPServerTypeStreamableHTTP, Headers: server.Headers}
	default:
		// 服务及其子进程调用的 npm 不输出安装摘要与提示，标准输出只作协议通道。
		variables := append(slices.Clone(environment.Variables), "NPM_CONFIG_LOGLEVEL=silent", "NPM_CONFIG_FUND=false", "NPM_CONFIG_UPDATE_NOTIFIER=false")
		for name, value := range server.Env {
			variables = append(variables, name+"="+value)
		}
		environment.Variables = variables
		config.Start = func(ctx context.Context) (io.ReadCloser, io.WriteCloser, error) {
			process, err := localworkspace.StartProcess(ctx, environment, dir, stderr, server.Command, server.Args...)
			if err != nil {
				return nil, nil, err
			}
			return process.Stdout, process.Stdin, nil
		}
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, mcpHandshakeTimeout)
	defer cancel()
	type handshake struct {
		session *mcp.Session
		tools   []mcp.Tool
		err     error
	}
	done := make(chan handshake, 1)
	go func() {
		// 会话以 ctx 为生命周期，握手只受时限约束。
		session, err := mcp.Connect(ctx, config)
		if err != nil {
			done <- handshake{err: err}
			return
		}
		tools, err := session.Tools(handshakeCtx)
		if err != nil {
			_ = session.Close()
			done <- handshake{err: err}
			return
		}
		done <- handshake{session: session, tools: tools}
	}()
	select {
	case result := <-done:
		return result.session, result.tools, result.err
	case <-handshakeCtx.Done():
		// 关闭迟到返回的会话。
		go func() {
			if result := <-done; result.session != nil {
				_ = result.session.Close()
			}
		}()
		return nil, nil, errors.New("连接并读取工具目录超时")
	}
}
