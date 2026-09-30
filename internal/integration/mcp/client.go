// Package mcp 实现远程与本地 MCP 服务的连接、工具发现与工具调用。
package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runforyou-ai/cervi/internal/common/brand"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

const (
	// CustomerIDHeader 携带按客户查询的服务所服务的客户在企业系统中的用户编号。
	CustomerIDHeader = "X-Customer-Id"
	// CustomerEmailHeader 携带该客户的邮箱，只作参考。
	CustomerEmailHeader = "X-Customer-Email"
)

// Config 定义 MCP 连接配置：Start 非空时启动本地服务并经其标准输入输出通信，否则连接远程服务，Headers 附加到会话的每个 HTTP 请求。
type Config struct {
	URL                string
	ServerType         domain.MCPServerType
	AuthorizationToken string
	Headers            map[string]string
	// Start 在建立会话时启动本地服务，返回其标准输出与标准输入；关闭标准输入时服务及其子进程全部结束。
	Start func(context.Context) (io.ReadCloser, io.WriteCloser, error)
}

// Discoverer 读取 MCP 服务的完整工具目录。
type Discoverer interface {
	Discover(context.Context, Config) ([]domain.MCPTool, error)
}

// Client 通过官方 SDK 探测并读取工具目录。
type Client struct{ runner *connectiontest.Runner }

// NewClient 创建具有统一超时的 MCP 客户端。
func NewClient() *Client { return &Client{runner: connectiontest.NewRunner(10 * time.Second)} }

// Discover 完成初始化并遍历所有工具分页，结束后关闭会话。
func (c *Client) Discover(ctx context.Context, config Config) ([]domain.MCPTool, error) {
	tools := make([]domain.MCPTool, 0)
	err := c.runner.Run(ctx, connectiontest.Target{
		Category: string(domain.ConnectionProbeMCPServer), Adapter: string(config.ServerType), Location: string(domain.ConnectionProbeServer),
	}, connectiontest.ProbeFunc(func(ctx context.Context) error {
		deadline, _ := ctx.Deadline()
		session, err := connectSession(ctx, config, deadline)
		if err != nil {
			return err
		}
		defer session.Close()
		catalog, err := session.Tools(ctx)
		if err != nil {
			return err
		}
		for _, item := range catalog {
			tools = append(tools, domain.MCPTool{Name: item.Name, Description: item.Description})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(tools, func(a, b domain.MCPTool) int { return strings.Compare(a.Name, b.Name) })
	return tools, nil
}

// newTransport 为本地服务创建标准输入输出传输，为远程服务按服务类型创建带认证的传输，deadline 为零值时请求期限跟随调用方 context。
func newTransport(config Config, deadline time.Time) (sdk.Transport, error) {
	if config.Start != nil {
		return stdioTransport{start: config.Start}, nil
	}
	client := connectiontest.NewHTTPClient()
	client.Transport = &authenticatedTransport{token: config.AuthorizationToken, headers: config.Headers, deadline: deadline}
	switch config.ServerType {
	case domain.MCPServerTypeSSE:
		return &sdk.SSEClientTransport{Endpoint: config.URL, HTTPClient: client}, nil
	case domain.MCPServerTypeStreamableHTTP:
		return &sdk.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}, nil
	}
	return nil, connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureInvalidConfig, nil)
}

// stdioTransport 在建立会话时启动本地服务，经其标准输入输出通信，会话关闭时关闭两端。
type stdioTransport struct {
	start func(context.Context) (io.ReadCloser, io.WriteCloser, error)
}

// Connect 启动本地服务并建立标准输入输出连接。
func (t stdioTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	output, input, err := t.start(ctx)
	if err != nil {
		return nil, connectiontest.NewError(connectiontest.StageConnect, connectiontest.FailureUnavailable, err)
	}
	return (&sdk.IOTransport{Reader: output, Writer: input}).Connect(ctx)
}

// connect 完成 MCP 初始化握手。
func connect(ctx context.Context, transport sdk.Transport) (*sdk.ClientSession, error) {
	session, err := sdk.NewClient(&sdk.Implementation{Name: brand.Current().DisplayName(), Version: "1.0.0"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return nil, classifyError(connectiontest.StageConnect, err)
	}
	return session, nil
}

// authenticatedTransport 为 MCP 的每个 HTTP 请求附加认证与配置的请求头并分类状态错误。
type authenticatedTransport struct {
	token    string
	headers  map[string]string
	deadline time.Time
}

// RoundTrip 发送带认证的请求并归一化传输错误。
func (t *authenticatedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := request.Context(), context.CancelFunc(func() {})
	if !t.deadline.IsZero() {
		// 将初始化、分页与关闭会话的 HTTP 请求限制在同一次探测期限内。
		ctx, cancel = context.WithDeadline(ctx, t.deadline)
	}
	request = request.Clone(ctx)
	if t.token != "" {
		request.Header.Set("Authorization", "Bearer "+t.token)
	}
	for name, value := range t.headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		cancel()
		return nil, connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	}
	if response.StatusCode >= 300 {
		response.Body.Close()
		cancel()
		return nil, connectiontest.HTTPStatusError(response.StatusCode)
	}
	response.Body = &responseBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

// ConnectionLost 判断调用错误是否表明会话已不可继续使用：传输层失败或服务端拒绝会话时成立，服务端返回的 JSON-RPC 错误响应和工具自身报告的失败不成立。
func ConnectionLost(err error) bool {
	if _, _, classified := connectiontest.Details(err); !classified {
		return false
	}
	var rpcError *jsonrpc.Error
	return !errors.As(err, &rpcError)
}

// classifyError 区分 MCP 协议错误和网络错误。
func classifyError(stage connectiontest.Stage, err error) error {
	if _, _, ok := connectiontest.Details(err); ok {
		return err
	}
	var rpcError *jsonrpc.Error
	if errors.As(err, &rpcError) {
		return connectiontest.NewError(stage, connectiontest.FailureProtocol, err)
	}
	return connectiontest.ClassifyTransportError(stage, err)
}

// responseBody 在流式响应关闭后释放请求期限。
type responseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close 关闭响应并释放上下文资源。
func (b *responseBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}
