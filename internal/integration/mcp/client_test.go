package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// TestDiscoverTransports 验证两种传输的认证、初始化、分页和空目录。
func TestDiscoverTransports(t *testing.T) {
	for _, serverType := range []domain.MCPServerType{domain.MCPServerTypeSSE, domain.MCPServerTypeStreamableHTTP} {
		t.Run(string(serverType), func(t *testing.T) {
			server := sdk.NewServer(&sdk.Implementation{Name: "test", Version: "1"}, &sdk.ServerOptions{PageSize: 1})
			for i := range 3 {
				server.AddTool(&sdk.Tool{Name: fmt.Sprintf("tool_%d", i), Description: fmt.Sprintf("工具 %d", i), InputSchema: map[string]any{"type": "object"}}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
					t.Error("discovery must not invoke tools")
					return &sdk.CallToolResult{}, nil
				})
			}
			var handler http.Handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
			if serverType == domain.MCPServerTypeSSE {
				handler = sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)
			}
			var requests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer test-token" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer endpoint.Close()
			config := Config{URL: endpoint.URL, ServerType: serverType, AuthorizationToken: "test-token"}
			tools, err := NewClient().Discover(context.Background(), config)
			if err != nil || len(tools) != 3 || tools[2].Description != "工具 2" {
				t.Fatalf("tools = %+v, err = %v", tools, err)
			}
			if requests.Load() < 4 {
				t.Fatal("tools were not paginated")
			}
			server.RemoveTools("tool_0", "tool_1", "tool_2")
			tools, err = NewClient().Discover(context.Background(), config)
			if err != nil || tools == nil || len(tools) != 0 {
				t.Fatalf("empty tools = %+v, err = %v", tools, err)
			}
			config.AuthorizationToken = "wrong"
			_, err = NewClient().Discover(context.Background(), config)
			_, kind, _ := connectiontest.Details(err)
			if kind != connectiontest.FailureUnauthorized {
				t.Fatalf("authentication classification: %v", err)
			}
		})
	}
}

// TestDiscoverTimeout 验证远端无响应时及时结束探测。
func TestDiscoverTimeout(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer endpoint.Close()
	client := &Client{runner: connectiontest.NewRunner(50 * time.Millisecond)}
	_, err := client.Discover(context.Background(), Config{URL: endpoint.URL, ServerType: domain.MCPServerTypeStreamableHTTP})
	_, kind, _ := connectiontest.Details(err)
	if kind != connectiontest.FailureTimeout {
		t.Fatalf("timeout classification: %v", err)
	}
}

// TestConnectionLost 验证只有传输层失败判定为会话不可用，错误响应与工具失败保留会话。
func TestConnectionLost(t *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
		want bool
	}{
		{"没有错误", nil, false},
		{"工具自身报告的失败", fmt.Errorf("order not found"), false},
		{"JSON-RPC 错误响应", classifyError(connectiontest.StageCapability, &jsonrpc.Error{Code: -32602, Message: "invalid params"}), false},
		{"传输层失败", connectiontest.NewError(connectiontest.StageCapability, connectiontest.FailureNetwork, fmt.Errorf("connection reset")), true},
		{"会话已失效", connectiontest.HTTPStatusError(http.StatusNotFound), true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := ConnectionLost(scenario.err); got != scenario.want {
				t.Fatalf("ConnectionLost=%v want %v", got, scenario.want)
			}
		})
	}
}
