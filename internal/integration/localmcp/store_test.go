package localmcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStorePutListRemove 验证添加、按名称列出、替换与删除服务，并按通用 mcpServers 格式写入配置文件。
func TestStorePutListRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app", "mcp.json")
	changes := 0
	store := NewStore(path, func() { changes++ })
	if servers, err := store.List(); err != nil || len(servers) != 0 {
		t.Fatalf("配置文件不存在时应为空: %v %v", servers, err)
	}
	for _, server := range []Server{
		{Name: "fetch", Command: "uvx", Args: []string{"mcp-server-fetch"}},
		{Name: "files", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"}},
		{Name: "fetch", Command: "uvx", Args: []string{"mcp-server-fetch", "--ignore-robots-txt"}},
	} {
		if err := store.Put(server); err != nil {
			t.Fatal(err)
		}
	}
	servers, err := store.List()
	if err != nil || len(servers) != 2 || servers[0].Name != "fetch" || len(servers[0].Args) != 2 || servers[1].Name != "files" {
		t.Fatalf("服务列表不符合预期: %+v %v", servers, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"mcpServers"`) {
		t.Fatalf("配置文件格式不符合预期: %s %v", data, err)
	}
	if removed, err := store.Remove("fetch"); err != nil || !removed {
		t.Fatalf("删除失败: %v %v", removed, err)
	}
	if removed, _ := store.Remove("fetch"); removed {
		t.Fatal("不存在的服务不应报告已删除")
	}
	if changes != 4 {
		t.Fatalf("变更通知次数 %d", changes)
	}
}

// TestServerValidate 验证服务名称与启动命令的校验。
func TestServerValidate(t *testing.T) {
	for _, server := range []Server{
		{Name: "", Command: "uvx"}, {Name: "has space", Command: "uvx"}, {Name: "ok", Command: " "},
		{Name: "remote", Type: TypeHTTP}, {Name: "remote", Type: TypeSSE, URL: "ftp://example.com"},
		{Name: "remote", Type: TypeHTTP, URL: "https://example.com/mcp", Command: "uvx"}, {Name: "remote", Type: "ws", URL: "https://example.com"},
	} {
		if server.Validate() == nil {
			t.Fatalf("应拒绝 %+v", server)
		}
	}
	for _, server := range []Server{
		{Name: "local", Command: "uvx"}, {Name: "remote", Type: TypeHTTP, URL: "http://127.0.0.1:3000/mcp"}, {Name: "events", Type: TypeSSE, URL: "https://example.com/sse"},
	} {
		if err := server.Validate(); err != nil {
			t.Fatalf("应接受 %+v: %v", server, err)
		}
	}
}
