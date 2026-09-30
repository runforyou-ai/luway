package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// stubLocalMCP 在内存中记录本地 MCP 服务。
type stubLocalMCP struct {
	servers []LocalMCPServer
}

// Add 记录服务并返回固定的工具名称。
func (s *stubLocalMCP) Add(_ context.Context, server LocalMCPServer) ([]string, error) {
	s.servers = append(s.servers, server)
	return []string{"fetch"}, nil
}

// Remove 删除同名服务。
func (s *stubLocalMCP) Remove(_ context.Context, name string) (bool, error) {
	for index, server := range s.servers {
		if server.Name == name {
			s.servers = append(s.servers[:index], s.servers[index+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// Names 返回已记录的服务名称。
func (s *stubLocalMCP) Names(context.Context) ([]string, error) {
	names := make([]string, 0, len(s.servers))
	for _, server := range s.servers {
		names = append(names, server.Name)
	}
	return names, nil
}

// TestLocalMCPToolsManageServers 验证添加与移除工具经执行设备管理本地 MCP 服务，移除工具的说明列出已添加的服务。
func TestLocalMCPToolsManageServers(t *testing.T) {
	ctx := context.Background()
	local := &stubLocalMCP{servers: []LocalMCPServer{{Name: "existing"}}}
	tools, err := newLocalMCPTools(ctx, RunRequest{Assignment: Assignment{Tools: LocalTools()}, LocalMCP: local})
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools=%d err=%v", len(tools), err)
	}
	info, err := tools[1].Info(ctx)
	if err != nil || !strings.Contains(info.Desc, "existing") {
		t.Fatalf("移除工具说明未列出已添加的服务: %v %v", info, err)
	}
	result, err := tools[0].(tool.InvokableTool).InvokableRun(ctx, `{"name":"fetch","command":"uvx","args":["mcp-server-fetch"]}`)
	if err != nil || !strings.Contains(result, "fetch") || len(local.servers) != 2 || local.servers[1].Command != "uvx" {
		t.Fatalf("添加结果不符合预期: %q %v %+v", result, err, local.servers)
	}
	if _, err := tools[1].(tool.InvokableTool).InvokableRun(ctx, `{"name":"missing"}`); err == nil {
		t.Fatal("移除不存在的服务应返回错误")
	}
}

// TestLocalMCPToolGuidanceSeparatesFromWorkspace 验证本地 MCP 管理工具与技能工具在工具说明中各自单独列出。
func TestLocalMCPToolGuidanceSeparatesFromWorkspace(t *testing.T) {
	guidance := toolGuidance(builtinTools{Workspace: LocalTools()})
	lines := strings.Split(guidance, "\n")
	if len(lines) != 4 || strings.Contains(lines[1], addLocalMCPToolName) || strings.Contains(lines[1], installSkillToolName) ||
		!strings.Contains(lines[2], addLocalMCPToolName) || !strings.Contains(lines[3], installSkillToolName) {
		t.Fatalf("工具说明不符合预期:\n%s", guidance)
	}
}

// TestAddLocalMCPToolDescribesManagedToolchain 验证只有执行设备提供托管运行环境时，添加工具才说明启动命令已预装。
func TestAddLocalMCPToolDescribesManagedToolchain(t *testing.T) {
	for _, managed := range []bool{true, false} {
		tools, err := newLocalMCPTools(context.Background(), RunRequest{Assignment: Assignment{Tools: LocalTools()}, LocalMCP: &stubLocalMCP{}, ManagedToolchain: managed})
		if err != nil {
			t.Fatal(err)
		}
		info, err := tools[0].Info(context.Background())
		if err != nil || strings.Contains(info.Desc, "已预装") != managed {
			t.Fatalf("managed=%v 时添加工具说明不符合预期: %v %v", managed, info, err)
		}
	}
}
