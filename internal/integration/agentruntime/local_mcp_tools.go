package agentruntime

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	// addLocalMCPToolName 是添加本地 MCP 服务的工具名称。
	addLocalMCPToolName = "add_local_mcp_server"
	// removeLocalMCPToolName 是移除本地 MCP 服务的工具名称。
	removeLocalMCPToolName = "remove_local_mcp_server"
)

// localMCPToolNames 是本地 MCP 管理工具，按注册顺序排列。
var localMCPToolNames = []string{addLocalMCPToolName, removeLocalMCPToolName}

const addLocalMCPToolDesc = `为这台电脑添加本地 MCP 服务，这台电脑上负责人的所有个人 AI 员工共用。
- 添加前会试连接服务并读取工具目录，失败时返回原因；成功后返回服务提供的工具。
- 本地进程服务：type 省略或为 stdio，填写 command、args 与所需的 env；例如 command 为 uvx、args 为 ["mcp-server-fetch"]。%s
- 通过网络连接的服务：type 为 http（Streamable HTTP）或 sse，填写 url 与所需的 headers。
- name 只能包含字母、数字、下划线与连字符；同名服务会被替换。
- 新添加的服务从下一次运行起可用。`

// managedToolchainMCPNote 是执行设备提供托管运行环境时补充在添加工具说明中的命令说明。
const managedToolchainMCPNote = "command 可以直接使用 uvx、npx、uv、python、node，这些已预装，无需另行安装。"

// organizationMCPNote 是本次运行绑定企业服务时补充在添加工具说明末尾的说明。
const organizationMCPNote = "\n- 企业为你绑定的服务：%s。服务可用时其工具已在工具列表中，不需要在这台电脑上重复添加同一服务；工具列表中没有其工具时，说明服务暂时不可用。"

const removeLocalMCPToolDesc = `移除这台电脑上的本地 MCP 服务。当前已添加：%s。`

// addLocalMCPArgs 是添加本地 MCP 服务的参数。
type addLocalMCPArgs struct {
	Name    string            `json:"name" jsonschema:"description=服务名称"`
	Type    string            `json:"type,omitempty" jsonschema:"enum=stdio,enum=http,enum=sse,description=连接方式，省略时为 stdio"`
	Command string            `json:"command,omitempty" jsonschema:"description=本地进程的启动命令"`
	Args    []string          `json:"args,omitempty" jsonschema:"description=本地进程的启动参数"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"description=本地进程需要的环境变量"`
	URL     string            `json:"url,omitempty" jsonschema:"description=http 或 sse 服务的地址"`
	Headers map[string]string `json:"headers,omitempty" jsonschema:"description=http 或 sse 服务需要的请求头"`
}

// removeLocalMCPArgs 是移除本地 MCP 服务的参数。
type removeLocalMCPArgs struct {
	Name string `json:"name" jsonschema:"description=服务名称"`
}

// newLocalMCPTools 按有效配置创建本地 MCP 管理工具，没有列出这些工具时返回空列表。
func newLocalMCPTools(ctx context.Context, request RunRequest) ([]tool.BaseTool, error) {
	if !slices.ContainsFunc(localMCPToolNames, func(name string) bool { return slices.Contains(request.Assignment.Tools, name) }) {
		return nil, nil
	}
	if request.LocalMCP == nil {
		return nil, fmt.Errorf("agent run assignment requires local MCP tools without local MCP management")
	}
	names, err := request.LocalMCP.Names(ctx)
	if err != nil {
		return nil, fmt.Errorf("list local MCP servers: %w", err)
	}
	// 移除工具的说明列出当前已添加的服务。
	current := "无"
	if len(names) > 0 {
		current = strings.Join(names, "、")
	}
	// 执行设备提供托管运行环境时说明可直接使用的启动命令。
	note := ""
	if request.ManagedToolchain {
		note = managedToolchainMCPNote
	}
	addDesc := fmt.Sprintf(addLocalMCPToolDesc, note)
	// 本次运行绑定企业服务时列出其名称。
	if len(request.Assignment.MCPServers) > 0 {
		addDesc += fmt.Sprintf(organizationMCPNote, strings.Join(request.Assignment.MCPServers, "、"))
	}
	add, err := utils.InferTool(addLocalMCPToolName, addDesc, func(ctx context.Context, input addLocalMCPArgs) (string, error) {
		tools, err := request.LocalMCP.Add(ctx, LocalMCPServer{
			Name: input.Name, Type: input.Type, Command: input.Command, Args: input.Args, Env: input.Env, URL: input.URL, Headers: input.Headers,
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已添加本地 MCP 服务 %s，提供的工具：%s。下一次运行起可用。", input.Name, strings.Join(tools, "、")), nil
	})
	if err != nil {
		return nil, fmt.Errorf("create add local MCP tool: %w", err)
	}
	remove, err := utils.InferTool(removeLocalMCPToolName, fmt.Sprintf(removeLocalMCPToolDesc, current), func(ctx context.Context, input removeLocalMCPArgs) (string, error) {
		removed, err := request.LocalMCP.Remove(ctx, input.Name)
		if err != nil {
			return "", err
		}
		if !removed {
			return "", fmt.Errorf("没有名为 %s 的本地 MCP 服务", input.Name)
		}
		return "已移除本地 MCP 服务 " + input.Name + "。", nil
	})
	if err != nil {
		return nil, fmt.Errorf("create remove local MCP tool: %w", err)
	}
	return []tool.BaseTool{add, remove}, nil
}
