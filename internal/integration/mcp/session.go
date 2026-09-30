package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// Tool 定义远程工具的调用契约，InputSchema 为该工具的原始 JSON Schema。
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Session 保持一次业务运行期内的 MCP 连接。
type Session struct{ session *sdk.ClientSession }

// Connect 建立 MCP 会话，请求期限由调用方 context 控制，调用方负责关闭会话。
func Connect(ctx context.Context, config Config) (*Session, error) {
	return connectSession(ctx, config, time.Time{})
}

// connectSession 建立 MCP 会话，deadline 非零时会话内全部 HTTP 请求共用该期限。
func connectSession(ctx context.Context, config Config, deadline time.Time) (*Session, error) {
	transport, err := newTransport(config, deadline)
	if err != nil {
		return nil, err
	}
	session, err := connect(ctx, transport)
	if err != nil {
		return nil, err
	}
	return &Session{session: session}, nil
}

// Tools 遍历所有分页读取工具目录及调用所需的输入 schema。
func (s *Session) Tools(ctx context.Context) ([]Tool, error) {
	tools := make([]Tool, 0)
	for item, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return nil, classifyError(connectiontest.StageCapability, err)
		}
		inputSchema, err := json.Marshal(item.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("encode input schema of tool %q: %w", item.Name, err)
		}
		tools = append(tools, Tool{Name: item.Name, Description: item.Description, InputSchema: inputSchema})
	}
	return tools, nil
}

// Call 调用远程工具并返回文本结果；工具自身报告的失败作为错误返回。
func (s *Session) Call(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	result, err := s.session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return "", classifyError(connectiontest.StageCapability, err)
	}
	// 工具结果以文本内容为主，仅在没有文本内容时回退到结构化结果。
	parts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok && text.Text != "" {
			parts = append(parts, text.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" && result.StructuredContent != nil {
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return "", fmt.Errorf("encode structured result of tool %q: %w", name, err)
		}
		text = string(encoded)
	}
	if result.IsError {
		if text == "" {
			return "", errors.New("tool reported an error without any detail")
		}
		return "", errors.New(text)
	}
	return text, nil
}

// Close 关闭 MCP 会话。
func (s *Session) Close() error { return s.session.Close() }
