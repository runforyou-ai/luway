package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// newSessionTestServer 启动带回声、报错和结构化结果工具的测试 MCP 服务。
func newSessionTestServer(t *testing.T) Config {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name: "echo", Description: "回声",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}},
	}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var arguments struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo:" + arguments.Text}}}, nil
	})
	server.AddTool(&sdk.Tool{Name: "broken", Description: "失败", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "库存服务不可用"}}}, nil
		})
	server.AddTool(&sdk.Tool{Name: "structured", Description: "结构化", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{StructuredContent: map[string]any{"count": 2}}, nil
		})
	endpoint := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	t.Cleanup(endpoint.Close)
	return Config{URL: endpoint.URL, ServerType: domain.MCPServerTypeStreamableHTTP}
}

// TestSessionToolsAndCall 验证目录读取保留输入 schema，并区分成功、工具报错与协议错误。
func TestSessionToolsAndCall(t *testing.T) {
	ctx := context.Background()
	session, err := Connect(ctx, newSessionTestServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	tools, err := session.Tools(ctx)
	if err != nil || len(tools) != 3 {
		t.Fatalf("tools = %+v, err = %v", tools, err)
	}
	echo := tools[1]
	if echo.Name != "echo" || echo.Description != "回声" || !strings.Contains(string(echo.InputSchema), `"required":["text"]`) {
		t.Fatalf("echo tool = %+v", echo)
	}

	result, err := session.Call(ctx, "echo", json.RawMessage(`{"text":"你好"}`))
	if err != nil || result != "echo:你好" {
		t.Fatalf("call result = %q, err = %v", result, err)
	}

	structured, err := session.Call(ctx, "structured", json.RawMessage(`{}`))
	if err != nil || structured != `{"count":2}` {
		t.Fatalf("structured result = %q, err = %v", structured, err)
	}

	if _, err := session.Call(ctx, "broken", json.RawMessage(`{}`)); err == nil || err.Error() != "库存服务不可用" {
		t.Fatalf("tool failure = %v", err)
	}

	_, err = session.Call(ctx, "missing", json.RawMessage(`{}`))
	if _, kind, ok := connectiontest.Details(err); !ok || kind != connectiontest.FailureProtocol {
		t.Fatalf("unknown tool classification: %v", err)
	}
}

// TestConnectUnavailable 验证远端不可达时返回分类后的连接错误。
func TestConnectUnavailable(t *testing.T) {
	_, err := Connect(context.Background(), Config{URL: "http://127.0.0.1:1/mcp", ServerType: domain.MCPServerTypeStreamableHTTP})
	if _, _, ok := connectiontest.Details(err); !ok {
		t.Fatalf("connect error = %v", err)
	}
}

// TestSessionHeaders 验证会话的每个 HTTP 请求都附加配置的请求头。
func TestSessionHeaders(t *testing.T) {
	ctx := context.Background()
	config := newSessionTestServer(t)
	var requests, missing atomic.Int32
	target := config.URL
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get(CustomerIDHeader) != "user-1" || r.Header.Get(CustomerEmailHeader) != "ada@example.com" {
			missing.Add(1)
		}
		request, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		request.Header = r.Header.Clone()
		response, err := http.DefaultTransport.RoundTrip(request)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			w.Header()[name] = values
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(proxy.Close)
	config.URL = proxy.URL
	config.Headers = map[string]string{CustomerIDHeader: "user-1", CustomerEmailHeader: "ada@example.com"}
	session, err := Connect(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if result, err := session.Call(ctx, "echo", json.RawMessage(`{"text":"订单"}`)); err != nil || result != "echo:订单" {
		t.Fatalf("call result = %q, err = %v", result, err)
	}
	if requests.Load() < 2 || missing.Load() != 0 {
		t.Fatalf("requests = %d, missing headers = %d", requests.Load(), missing.Load())
	}
}
