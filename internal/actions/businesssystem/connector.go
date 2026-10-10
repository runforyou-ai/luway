//go:build server

package businesssystem

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	"github.com/runforyou-ai/luway/internal/integration/openapi"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// OpenAPIClient 读取 OpenAPI 文档并调用文档中的接口。
type OpenAPIClient interface {
	openapi.Discoverer
	Call(context.Context, openapi.Request) (string, error)
}

// mcpConnectTimeout 限制建立 MCP 会话的时间。
const mcpConnectTimeout = 15 * time.Second

// errCallerClosed 表示调用会话已关闭。
var errCallerClosed = errors.New("business system caller closed")

// Connector 是业务系统的统一执行入口：按传输方式读取工具目录，并为运行或审批后的执行建立调用会话。
type Connector struct {
	mcp        mcpintegration.Discoverer
	connectMCP func(context.Context, mcpintegration.Config) (mcpSession, error)
	openapi    OpenAPIClient
	// connectTimeout 是建立 MCP 会话的时限。
	connectTimeout time.Duration
}

// mcpSession 是一个已建立的 MCP 会话。
type mcpSession interface {
	Call(ctx context.Context, name string, arguments json.RawMessage) (string, error)
	Close() error
}

// NewConnector 创建按传输方式使用 MCP 与 OpenAPI 客户端的业务系统执行入口。
func NewConnector(mcp mcpintegration.Discoverer, openAPI OpenAPIClient) *Connector {
	return &Connector{
		mcp: mcp, openapi: openAPI, connectTimeout: mcpConnectTimeout,
		connectMCP: func(ctx context.Context, config mcpintegration.Config) (mcpSession, error) {
			return mcpintegration.Connect(ctx, config)
		},
	}
}

// NewDefaultConnector 创建使用默认 MCP 与 OpenAPI 客户端的业务系统执行入口。
func NewDefaultConnector() *Connector {
	return NewConnector(mcpintegration.NewClient(), openapi.NewClient())
}

// Discovery 是一次工具目录读取结果：HTTP 业务系统未填写接口根地址时取文档声明的服务地址。
type Discovery struct {
	Connection domain.BusinessSystemConnection
	Tools      []domain.BusinessTool
}

// Discover 按传输方式读取工具目录；HTTP 业务系统的文档无效或没有可调用接口时返回字段校验错误，接口根地址无法确定时同样返回字段校验错误。
func (c *Connector) Discover(ctx context.Context, input ConnectionInput) (Discovery, error) {
	headers := credentialHeaders(input.Credential)
	if input.Transport == domain.BusinessSystemTransportMCP {
		tools, err := c.mcp.Discover(ctx, mcpintegration.Config{URL: input.Connection.MCP.URL, ServerType: input.Connection.MCP.ServerType, Headers: headers})
		return Discovery{Connection: input.Connection, Tools: tools}, err
	}
	connection := *input.Connection.HTTP
	document, err := c.openapi.Discover(ctx, openapi.Source{SpecURL: connection.SpecURL, Spec: connection.Spec})
	switch {
	case errors.Is(err, openapi.ErrSpecInvalid):
		return Discovery{}, &ValidationError{Fields: map[string]ValidationCode{specField(connection): ValidationSpecInvalid}}
	case errors.Is(err, openapi.ErrSpecEmpty):
		return Discovery{}, &ValidationError{Fields: map[string]ValidationCode{specField(connection): ValidationSpecEmpty}}
	case err != nil:
		return Discovery{}, err
	}
	if connection.BaseURL == "" {
		connection.BaseURL = document.ServerURL
	}
	if connection.BaseURL == "" {
		return Discovery{}, &ValidationError{Fields: map[string]ValidationCode{"baseUrl": ValidationBaseURLRequired}}
	}
	return Discovery{Connection: domain.BusinessSystemConnection{HTTP: &connection}, Tools: document.Tools}, nil
}

// specField 返回文档来源对应的表单字段。
func specField(connection domain.HTTPConnection) string {
	if connection.SpecURL != "" {
		return "specUrl"
	}
	return "spec"
}

// Open 为业务系统建立调用会话，headers 是附加到每个请求的凭据与绑定请求头；MCP 会话在首次调用时连接，连接中断后的下一次调用重新连接。
// ctx 控制会话的生命周期，调用方结束使用后关闭会话。
func (c *Connector) Open(ctx context.Context, system servermodels.BusinessSystem, headers map[string]string) agentcontract.BusinessCaller {
	sessionCtx, cancel := context.WithCancel(ctx)
	return &caller{connector: c, system: system, headers: headers, ctx: sessionCtx, cancel: cancel}
}

// caller 是一个业务系统在一次运行或一次执行内的调用会话，可供并行的工具调用共用。
type caller struct {
	connector *Connector
	system    servermodels.BusinessSystem
	headers   map[string]string
	ctx       context.Context
	cancel    context.CancelFunc

	mu         sync.Mutex
	session    *sharedSession
	connecting *connectAttempt
	closed     bool
}

// connectAttempt 是一次进行中的 MCP 连接，done 关闭后 err 为连接失败原因。
type connectAttempt struct {
	done chan struct{}
	err  error
}

// sharedSession 是并行调用共用的 MCP 会话，inFlight 计数正在进行的调用，会话在全部调用结束后才关闭。
type sharedSession struct {
	mcpSession
	inFlight sync.WaitGroup
}

// retire 等待会话上正在进行的调用结束后关闭会话。
func (s *sharedSession) retire() error {
	s.inFlight.Wait()
	return s.Close()
}

// Call 按工具目录中的原工具名调用工具：HTTP 业务系统按工具对应的接口发出请求，MCP 业务系统经共用会话调用；
// 连接中断时后续调用改用新连接，中断的会话在其上的调用全部结束后关闭。
func (c *caller) Call(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	if c.system.Transport == domain.BusinessSystemTransportHTTP {
		index := slices.IndexFunc(c.system.Tools, func(tool domain.BusinessTool) bool { return tool.Name == name && tool.HTTP != nil })
		if index < 0 {
			return "", ErrToolNotFound
		}
		return c.connector.openapi.Call(ctx, openapi.Request{
			BaseURL: c.system.Connection.HTTP.BaseURL, Headers: c.headers, Operation: *c.system.Tools[index].HTTP, Arguments: arguments,
		})
	}
	session, err := c.acquire(ctx)
	if err != nil {
		return "", err
	}
	result, err := session.Call(ctx, name, arguments)
	session.inFlight.Done()
	if err != nil && mcpintegration.ConnectionLost(err) {
		slog.WarnContext(logscope.WithWorkspace(ctx, c.system.WorkspaceID), "业务系统 MCP 连接中断", "business_system_id", c.system.ID, "error", err)
		c.mu.Lock()
		if c.session == session {
			c.session = nil
			go func() { _ = session.retire() }()
		}
		c.mu.Unlock()
	}
	return result, err
}

// acquire 返回已登记一次进行中调用的 MCP 会话；尚未建立时共用同一次连接，等待随 ctx 取消立即返回。
func (c *caller) acquire(ctx context.Context) (*sharedSession, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, errCallerClosed
		}
		if c.session != nil {
			c.session.inFlight.Add(1)
			session := c.session
			c.mu.Unlock()
			return session, nil
		}
		attempt := c.connecting
		if attempt == nil {
			attempt = &connectAttempt{done: make(chan struct{})}
			c.connecting = attempt
			go c.connect(attempt)
		}
		c.mu.Unlock()
		select {
		case <-attempt.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if attempt.err != nil {
			return nil, attempt.err
		}
	}
}

// connect 在会话生命周期内建立 MCP 连接，超过连接时限时以 ErrConnectTimeout 结束本次连接，迟到的连接随即关闭。
func (c *caller) connect(attempt *connectAttempt) {
	timer := time.AfterFunc(c.connector.connectTimeout, func() { c.settle(attempt, nil, ErrConnectTimeout) })
	connection := c.system.Connection.MCP
	session, err := c.connector.connectMCP(c.ctx, mcpintegration.Config{URL: connection.URL, ServerType: connection.ServerType, Headers: c.headers})
	timer.Stop()
	c.settle(attempt, session, err)
}

// settle 结束一次连接并唤醒等待者：首次结束时登记连接结果，连接已结束或调用会话已关闭时关闭新建的会话。
func (c *caller) settle(attempt *connectAttempt, session mcpSession, err error) {
	c.mu.Lock()
	if c.connecting != attempt {
		c.mu.Unlock()
		if session != nil {
			_ = session.Close()
		}
		return
	}
	c.connecting = nil
	discard := session != nil && (err != nil || c.closed)
	switch {
	case err != nil:
		attempt.err = err
	case c.closed:
		attempt.err = errCallerClosed
	default:
		c.session = &sharedSession{mcpSession: session}
	}
	close(attempt.done)
	c.mu.Unlock()
	if discard {
		_ = session.Close()
	}
}

// Close 在正在进行的调用结束后关闭当前 MCP 会话，并结束会话生命周期；进行中的连接完成后随即关闭。
func (c *caller) Close() error {
	c.mu.Lock()
	c.closed = true
	session := c.session
	c.session = nil
	c.mu.Unlock()
	defer c.cancel()
	if session == nil {
		return nil
	}
	return session.retire()
}

// credentialHeaders 返回凭据附加到每个请求的请求头，不认证时为空。
func credentialHeaders(credential domain.BusinessSystemCredential) map[string]string {
	headers := make(map[string]string)
	if name, value := credential.Header(); name != "" {
		headers[name] = value
	}
	return headers
}
