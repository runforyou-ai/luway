//go:build !server

package apiproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/clientsession"
)

// TestCallDeviceRunMCPToolFollowsContext 验证企业 MCP 工具调用不受普通接口的请求时限与响应大小上限约束，普通接口仍受时限约束。
func TestCallDeviceRunMCPToolFollowsContext(t *testing.T) {
	large := strings.Repeat("订", 1<<20)
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(200 * time.Millisecond)
		switch request.URL.Path {
		case "/api/agent-runs/run-1/mcp/tools/call":
			var input appservice.DeviceRunMCPToolCallInput
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.ServerID != "server-1" || input.ToolName != "get_order" ||
				request.Header.Get(appservice.DeviceHeader) != "device-1" || request.Header.Get("Authorization") != "Bearer mcp-token" {
				t.Errorf("input=%+v err=%v header=%v", input, err, request.Header)
			}
			_ = json.NewEncoder(writer).Encode(appservice.DeviceRunMCPToolCallResult{Result: large})
		default:
			_ = json.NewEncoder(writer).Encode(appservice.DeviceRunMCPToolList{})
		}
	}))
	defer remote.Close()
	backend, err := newTestBackend(&memoryStore{serverURL: remote.URL, credentialSet: true, credential: clientsession.Credential{ServerURL: remote.URL, Token: "mcp-token", ExpiresAt: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	backend.connection.currentState().client.Timeout = 50 * time.Millisecond
	meta := appservice.RequestMeta{DeviceID: "device-1"}
	if _, err := backend.ListDeviceRunMCPTools(context.Background(), meta, "run-1"); err == nil {
		t.Fatal("ordinary request ignored the client timeout")
	}
	output, err := backend.CallDeviceRunMCPTool(context.Background(), meta, "run-1", appservice.DeviceRunMCPToolCallInput{
		ServerID: "server-1", ToolName: "get_order", Arguments: json.RawMessage(`{}`),
	})
	if err != nil || output.Result != large {
		t.Fatalf("result bytes=%d err=%v", len(output.Result), err)
	}
}
