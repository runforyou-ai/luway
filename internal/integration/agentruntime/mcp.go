package agentruntime

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/mozillazg/go-pinyin"
	"github.com/runforyou-ai/luway/internal/integration/mcp"
)

const (
	// mcpToolNameMaxLength 是模型可见 MCP 工具名称的最大长度。
	mcpToolNameMaxLength = 64
	// mcpToolDigestLength 是工具名称末尾服务身份摘要的十六进制位数。
	mcpToolDigestLength = 8
)

// mcpHandshakeTimeout 限制建立会话和读取工具目录的时间。
var mcpHandshakeTimeout = 15 * time.Second

// mcpPinyinArgs 输出汉字不带声调的读音。
var mcpPinyinArgs = pinyin.NewArgs()

// MCPSource 表示 MCP 服务的来源。
type MCPSource string

const (
	// MCPSourceOrganization 是管理员为企业添加、按 AI 员工绑定的服务。
	MCPSourceOrganization MCPSource = "organization"
	// MCPSourceLocal 是个人 AI 员工为执行电脑添加的本地服务。
	MCPSourceLocal MCPSource = "local"
)

// MCPConnection 是本次运行内一个 MCP 服务的已建立连接。
type MCPConnection interface {
	// Tools 读取服务的工具目录。
	Tools(ctx context.Context) ([]mcp.Tool, error)
	// Call 调用工具并返回文本结果，工具自身报告的失败作为错误返回。
	Call(ctx context.Context, name string, arguments json.RawMessage) (string, error)
	// Close 关闭连接。
	Close() error
}

// MCPServer 定义本次运行可调用的一个 MCP 服务。
type MCPServer struct {
	Source MCPSource
	ID     string // 服务在来源内的唯一标识：企业服务为服务编号，本地服务为服务名称。
	Name   string
	Config mcp.Config
	Tools  []string // 限定挂载的工具名称，nil 表示挂载目录中的全部工具。
	// HandshakeTimeout 是建立会话和读取工具目录的时限，零值使用默认时限。
	HandshakeTimeout time.Duration
	// Connect 非空时替代按 Config 直连，用于经企业服务端代理调用的服务。
	Connect func(context.Context) (MCPConnection, error)
}

// Open 建立连接，context 控制连接的生命周期。
func (s MCPServer) Open(ctx context.Context) (MCPConnection, error) {
	if s.Connect != nil {
		return s.Connect(ctx)
	}
	session, err := mcp.Connect(ctx, s.Config)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// MCPToolName 生成模型可见的工具名称：mcp__<服务名>__<工具名>_<摘要>。服务名与工具名中的汉字转为拼音、其余字符只保留字母与数字，
// 摘要取自来源、服务标识与原工具名，同名服务或转写后相同的名称由摘要区分；超长时截断正文，始终保留摘要。
func MCPToolName(source MCPSource, serverID, serverName, toolName string) string {
	sum := sha256.Sum256([]byte(string(source) + ":" + serverID + "\x00" + toolName))
	suffix := "_" + hex.EncodeToString(sum[:])[:mcpToolDigestLength]
	body := "mcp__" + cmp.Or(mcpNameSlug(serverName), "server") + "__" + cmp.Or(mcpNameSlug(toolName), "tool")
	body = strings.TrimRight(body[:min(len(body), mcpToolNameMaxLength-len(suffix))], "_")
	return body + suffix
}

// mcpNameSlug 把名称转写为以下划线连接的 ASCII 词：汉字按拼音成词，字母与数字连续成词，其余字符作为分隔。
func mcpNameSlug(name string) string {
	words := make([]string, 0)
	var word strings.Builder
	for _, r := range name {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			word.WriteRune(r)
			continue
		}
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
		// 汉字取首个无声调读音。
		if readings := pinyin.SinglePinyin(r, mcpPinyinArgs); len(readings) > 0 {
			words = append(words, readings[0])
		}
	}
	if word.Len() > 0 {
		words = append(words, word.String())
	}
	return strings.Join(words, "_")
}

// openMCPTools 并发连接本次运行绑定的 MCP 服务并按服务顺序注册其工具，返回释放全部连接的函数。
// 服务不可用、目录读取失败、工具参数定义无法解析或工具名称与已注册工具重复时跳过，本次运行在缺少这部分工具的情况下继续。
func openMCPTools(ctx context.Context, runID string, servers []MCPServer, registered map[string]struct{}) ([]*mcpTool, func()) {
	releases := make([]func(), 0, len(servers))
	releaseConnections := func() {
		for _, release := range releases {
			release()
		}
	}
	// 并发握手全部服务，结果按服务顺序登记，工具顺序与重名跳过规则保持稳定。
	type opened struct {
		connection MCPConnection
		catalog    []mcp.Tool
		release    func()
		err        error
	}
	results := make([]opened, len(servers))
	var wait sync.WaitGroup
	for index, server := range servers {
		wait.Go(func() {
			connection, catalog, release, err := openMCPServer(ctx, server)
			results[index] = opened{connection: connection, catalog: catalog, release: release, err: err}
		})
	}
	wait.Wait()
	tools := make([]*mcpTool, 0)
	for index, server := range servers {
		connection, catalog, release, err := results[index].connection, results[index].catalog, results[index].release, results[index].err
		if err != nil {
			slog.Warn("MCP 服务不可用，本次运行跳过其工具",
				"agent_run_id", runID, "mcp_source", server.Source, "mcp_server", server.Name, "error", err)
			continue
		}
		accepted := 0
		for _, item := range catalog {
			if server.Tools != nil && !slices.Contains(server.Tools, item.Name) {
				continue
			}
			info, err := mcpToolInfo(server, item)
			if err != nil {
				slog.Warn("MCP 工具参数定义无法解析，跳过该工具",
					"agent_run_id", runID, "mcp_server", server.Name, "tool_name", item.Name, "error", err)
				continue
			}
			if _, exists := registered[info.Name]; exists {
				slog.Warn("MCP 工具名称与已注册工具重复，跳过该工具",
					"agent_run_id", runID, "mcp_server", server.Name, "tool_name", item.Name)
				continue
			}
			registered[info.Name] = struct{}{}
			accepted++
			tools = append(tools, &mcpTool{connection: connection, server: server.Name, name: item.Name, info: info})
		}
		slog.Info("MCP 服务工具目录已读取", "agent_run_id", runID, "mcp_source", server.Source, "mcp_server", server.Name,
			"tool_count", len(catalog), "registered_tool_count", accepted)
		if accepted == 0 {
			release()
			continue
		}
		releases = append(releases, release)
	}
	return tools, releaseConnections
}

type mcpHandshake struct {
	connection MCPConnection
	tools      []mcp.Tool
	err        error
}

// openMCPServer 在握手超时内建立连接并读取工具目录，返回释放连接的函数。
// 超时由独立计时器控制，连接 context 在整个 Run 内保持有效，供 SSE 维持挂起的事件流请求。
func openMCPServer(ctx context.Context, server MCPServer) (MCPConnection, []mcp.Tool, func(), error) {
	sessionCtx, cancelSession := context.WithCancel(ctx)
	done := make(chan mcpHandshake, 1)
	go func() {
		connection, err := server.Open(sessionCtx)
		if err != nil {
			done <- mcpHandshake{err: err}
			return
		}
		tools, err := connection.Tools(sessionCtx)
		if err != nil {
			connection.Close()
			done <- mcpHandshake{err: err}
			return
		}
		done <- mcpHandshake{connection: connection, tools: tools}
	}()
	timer := time.NewTimer(cmp.Or(server.HandshakeTimeout, mcpHandshakeTimeout))
	defer timer.Stop()
	select {
	case result := <-done:
		if result.err != nil {
			cancelSession()
			return nil, nil, nil, result.err
		}
		// 已 Detach 的连接只能由 Close 释放，关闭后再结束连接 context。
		return result.connection, result.tools, func() {
			if err := result.connection.Close(); err != nil {
				slog.Warn("关闭 MCP 连接失败", "mcp_server", server.Name, "error", err)
			}
			cancelSession()
		}, nil
	case <-timer.C:
		cancelSession()
		// 关闭迟到返回的连接。
		go func() {
			if result := <-done; result.connection != nil {
				result.connection.Close()
			}
		}()
		return nil, nil, nil, errors.New("connect and read tool catalog timed out")
	}
}

// mcpToolInfo 把工具目录项转换为模型可见的工具定义，描述开头注明服务来源与名称。
func mcpToolInfo(server MCPServer, item mcp.Tool) (*schema.ToolInfo, error) {
	parameters := &jsonschema.Schema{Type: "object"}
	if len(item.InputSchema) > 0 {
		if err := json.Unmarshal(item.InputSchema, parameters); err != nil {
			return nil, err
		}
	}
	origin := "企业服务"
	if server.Source == MCPSourceLocal {
		origin = "这台电脑"
	}
	return &schema.ToolInfo{
		Name:        MCPToolName(server.Source, server.ID, server.Name, item.Name),
		Desc:        strings.TrimSpace(fmt.Sprintf("［%s · %s］%s", origin, server.Name, item.Description)),
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parameters),
	}, nil
}

// mcpTool 把 MCP 工具暴露为 Eino 可调用工具，调用时使用服务目录中的原工具名。
type mcpTool struct {
	connection MCPConnection
	server     string
	name       string
	info       *schema.ToolInfo
}

// Info 返回模型可见的名称、描述和参数定义。
func (t *mcpTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 调用 MCP 工具并返回文本结果。
func (t *mcpTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	arguments := json.RawMessage(argumentsInJSON)
	if !json.Valid(arguments) {
		return "", errors.New("参数不是合法 JSON，请重新提交。")
	}
	result, err := t.connection.Call(ctx, t.name, arguments)
	if err != nil {
		return "", fmt.Errorf("call tool %q on MCP server %q: %w", t.name, t.server, err)
	}
	return result, nil
}
