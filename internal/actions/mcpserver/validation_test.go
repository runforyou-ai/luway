//go:build server

package mcpserver

import (
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestNormalizeInput 验证 MCP 服务地址、传输类型和名称校验。
func TestNormalizeInput(t *testing.T) {
	for _, serverType := range []domain.MCPServerType{domain.MCPServerTypeSSE, domain.MCPServerTypeStreamableHTTP} {
		input, fields := normalizeInput(Input{Name: " Docs ", URL: " http://localhost:8080/mcp ", ServerType: serverType})
		if len(fields) != 0 || input.Name != "Docs" || input.URL != "http://localhost:8080/mcp" || input.ServerType != serverType {
			t.Fatalf("input = %+v, fields = %+v", input, fields)
		}
	}
	for _, address := range []string{"example.com", "ftp://example.com", "https://user:pass@example.com", "https:///mcp"} {
		_, fields := normalizeInput(Input{Name: "Docs", URL: address, ServerType: domain.MCPServerTypeSSE})
		if fields["url"] != ValidationURLInvalid {
			t.Fatalf("address = %q, fields = %+v", address, fields)
		}
	}
	for _, serverType := range []domain.MCPServerType{"", "stdio", "http"} {
		_, fields := normalizeInput(Input{Name: "Docs", URL: "https://example.com/mcp", ServerType: serverType})
		if fields["serverType"] != ValidationServerTypeInvalid {
			t.Fatalf("type = %q, fields = %+v", serverType, fields)
		}
	}
	_, fields := normalizeInput(Input{Name: " ", ServerType: domain.MCPServerTypeSSE})
	if fields["name"] != ValidationNameRequired || fields["url"] != ValidationURLRequired {
		t.Fatalf("required fields = %+v", fields)
	}
}
