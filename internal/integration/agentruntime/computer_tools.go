package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/mcp"
)

const (
	// readFileToolName 是读取电脑上文本文件的工具名称。
	readFileToolName = "read_file"
	// writeFileToolName 是在电脑上创建或覆盖文件的工具名称。
	writeFileToolName = "write_file"
	// editFileToolName 是替换电脑上文件中一段原文的工具名称。
	editFileToolName = "edit_file"
	// executeToolName 是在电脑上执行命令的工具名称。
	executeToolName = "execute"
)

const readFileToolDesc = `读取电脑上的文本文件，结果以 cat -n 格式带行号返回，行号从 1 开始，默认最多读取 2000 行。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 已知需要的范围时用 offset 与 limit 只读取该部分。
- 二进制文件与超过 10 MB 的文件无法读取。`

const writeFileToolDesc = `以完整内容创建或覆盖电脑上的文件，缺少的上级文件夹会一并创建。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 覆盖已有文件前必须先用 read_file 读取；读取后文件被改动过时写入失败，重新读取后再写。局部修改使用 edit_file。
- 只写入文本；Word、Excel、PPT 等文件用 execute 运行脚本生成。`

const editFileToolDesc = `把电脑上文本文件中的一段原文精确替换为新内容。
- 修改前必须先用 read_file 读取文件；读取后文件被改动过时修改失败，重新读取后再改。
- old_string 必须与文件内容完全一致（包括缩进与换行），不含 read_file 输出的行号前缀。
- old_string 在文件中必须唯一，否则补充上下文使其唯一；replace_all 为 true 时替换全部出现处。
- new_string 为空表示删除这段原文。`

const executeToolDesc = `在电脑上执行一条 %s 命令，返回合并后的标准输出与标准错误。
- 工作目录是本会话的默认文件夹。
- 单次命令最长运行 10 分钟，超时即终止；命令结束时由它启动的后台进程也会终止，不要启动需要长期运行的服务。
- 命令无法交互输入，需要确认的命令使用非交互参数（如 -y）。
- 列出目录、按名称查找文件、在文件中搜索内容与删除文件都用命令完成；读取与修改文件使用 read_file、edit_file、write_file。`

// managedToolchainGuidance 是电脑提供托管运行环境时补充在命令工具说明后的用法。
const managedToolchainGuidance = `
- 命令中可以直接使用 python、uv、uvx、node、npm、npx，这些已预装，无需检查或安装。
- 运行需要第三方包的 Python 脚本时用 uv run --with 包名 python 脚本.py。
- 需要在当前目录保留依赖时，先 uv venv，再 uv pip install 包名，之后用 uv run python 脚本.py 运行；直接执行 python 读不到这个虚拟环境里的包。
- 不要使用 pip、python -m pip、uv pip install --system 或 --break-system-packages，它们会失败或把包装进电脑上原有的 Python。
- 需要 Node.js 包时用 npm 或 npx。`

// Computer 是运行使用的电脑，执行文件、命令、本机 MCP 与技能操作。
type Computer interface {
	// Execute 把当前工具调用作为操作派发到电脑并返回电脑上报的结果；suspend 为 true 时结果未在等待时限内返回即返回 ErrAwaitExternal，
	// 结果写入调用记录后运行被唤醒；操作失败或电脑断开时返回交给模型的错误。
	Execute(ctx context.Context, operation domain.ComputerOperation, suspend bool) (domain.ComputerOutcome, error)
}

// readFileArgs 是读取文件工具的参数。
type readFileArgs struct {
	FilePath string `json:"file_path" jsonschema:"required" jsonschema_description:"文件路径"`
	Offset   int    `json:"offset,omitempty" jsonschema_description:"起始行号，从 1 开始"`
	Limit    int    `json:"limit,omitempty" jsonschema_description:"最多读取的行数"`
}

// writeFileArgs 是写入文件工具的参数。
type writeFileArgs struct {
	FilePath string `json:"file_path" jsonschema:"required" jsonschema_description:"文件路径"`
	Content  string `json:"content" jsonschema:"required" jsonschema_description:"文件的完整内容"`
}

// editFileArgs 是修改文件工具的参数。
type editFileArgs struct {
	FilePath   string `json:"file_path" jsonschema:"required" jsonschema_description:"文件路径"`
	OldString  string `json:"old_string" jsonschema:"required" jsonschema_description:"要替换的原文"`
	NewString  string `json:"new_string" jsonschema:"required" jsonschema_description:"替换后的内容"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema_description:"是否替换全部出现处"`
}

// executeArgs 是执行命令工具的参数。
type executeArgs struct {
	Command string `json:"command" jsonschema:"required" jsonschema_description:"要执行的命令"`
}

// fileVersions 记录本次运行读取或写入后电脑上文件内容的摘要：摘要按电脑解析出的绝对路径登记，模型给出的路径指向其绝对路径，写入与修改已有文件时以它作为条件；
// 结果未在等待时限内送达的文件调用记入 awaiting，恢复时以调用记录中的结果补登。
type fileVersions struct {
	mu       sync.Mutex
	hashes   map[string]string
	aliases  map[string]string
	awaiting map[string]string
}

// newFileVersions 以恢复状态中的摘要、路径别名与等待结果的调用创建文件版本记录。
func newFileVersions(saved FileVersions) *fileVersions {
	return &fileVersions{hashes: maps.Clone(nonNil(saved.Hashes)), aliases: maps.Clone(nonNil(saved.Aliases)), awaiting: maps.Clone(nonNil(saved.Awaiting))}
}

// nonNil 返回非空的映射。
func nonNil(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

// base 返回路径最近一次读取或写入后的内容摘要，没有记录时为空。
func (v *fileVersions) base(path string) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if resolved, ok := v.aliases[path]; ok {
		return v.hashes[resolved]
	}
	return v.hashes[path]
}

// record 按解析出的绝对路径登记操作完成后的内容摘要，并让模型给出的路径指向它。
func (v *fileVersions) record(path string, outcome domain.ComputerOutcome) {
	if outcome.Hash == "" || outcome.Path == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.recordLocked(path, outcome)
}

// recordLocked 登记摘要与路径别名，调用方持有 mu。
func (v *fileVersions) recordLocked(path string, outcome domain.ComputerOutcome) {
	v.hashes[outcome.Path] = outcome.Hash
	if path != outcome.Path {
		v.aliases[path] = outcome.Path
	}
}

// await 记录结果尚未送达的文件调用及其路径。
func (v *fileVersions) await(callID, path string) {
	if callID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.awaiting[callID] = path
}

// restoreOutcomes 以调用记录中电脑上报的结果补登挂起时仍在等待的文件调用的摘要，其余调用的摘要已在恢复状态中。
func (v *fileVersions) restoreOutcomes(blocks []Block) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, block := range blocks {
		call := block.Payload.ToolCall
		if call == nil {
			continue
		}
		path, ok := v.awaiting[call.ID]
		if !ok {
			continue
		}
		delete(v.awaiting, call.ID)
		if outcome := call.ComputerOutcome; outcome != nil && call.Status == domain.AgentToolCallSucceeded && outcome.Hash != "" && outcome.Path != "" {
			v.recordLocked(path, *outcome)
		}
	}
}

// snapshot 返回当前摘要、路径别名与等待结果的调用的副本，写入恢复状态。
func (v *fileVersions) snapshot() FileVersions {
	v.mu.Lock()
	defer v.mu.Unlock()
	return FileVersions{Hashes: maps.Clone(v.hashes), Aliases: maps.Clone(v.aliases), Awaiting: maps.Clone(v.awaiting)}
}

// computerToolset 是按有效配置创建的电脑工具：文件与命令工具作为普通工具注册，技能加载由中间件注册。
type computerToolset struct {
	tools  []tool.BaseTool
	skills adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage] // 有效配置不含技能工具时为空。
}

// newComputerToolset 按有效配置创建电脑工具与技能中间件；hub 非空时声明 fork 的技能交给其提供的子 Agent 执行。
func newComputerToolset(ctx context.Context, request RunRequest, versions *fileVersions, hub skill.TypedAgentHub[*schema.AgenticMessage]) (computerToolset, error) {
	tools, err := newComputerTools(request, versions)
	if err != nil {
		return computerToolset{}, err
	}
	skills, err := newSkillTools(ctx, request, hub)
	if err != nil {
		return computerToolset{}, err
	}
	return computerToolset{tools: tools, skills: skills}, nil
}

// newComputerTools 按有效配置中的电脑工具创建文件与命令工具；有效配置不含这些工具时返回空列表。
func newComputerTools(request RunRequest, versions *fileVersions) ([]tool.BaseTool, error) {
	names := slices.DeleteFunc(slices.Clone(request.Assignment.Tools), func(name string) bool {
		return !IsComputerTool(name) || name == skillToolName
	})
	if len(names) == 0 {
		return nil, nil
	}
	if request.Computer == nil {
		return nil, fmt.Errorf("agent run assignment requires computer tools %v without a computer", names)
	}
	computer := request.Computer
	execute := func(ctx context.Context, operation domain.ComputerOperation) (domain.ComputerOutcome, error) {
		outcome, err := computer.Execute(ctx, operation, suspendable(ctx))
		// 文件调用的结果在挂起后送达，恢复时据调用记录补登摘要。
		if errors.Is(err, ErrAwaitExternal) && operation.Path != "" {
			versions.await(ToolCallID(ctx), operation.Path)
		}
		return outcome, err
	}
	tools := make([]tool.BaseTool, 0, len(names))
	for _, name := range names {
		var created tool.BaseTool
		var err error
		switch name {
		case readFileToolName:
			created, err = utils.InferTool(name, readFileToolDesc, func(ctx context.Context, input readFileArgs) (string, error) {
				outcome, err := execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationReadFile, Path: input.FilePath, Offset: input.Offset, Limit: input.Limit})
				if err != nil {
					return "", err
				}
				versions.record(input.FilePath, outcome)
				return outcome.Output, nil
			})
		case writeFileToolName:
			created, err = utils.InferTool(name, writeFileToolDesc, func(ctx context.Context, input writeFileArgs) (string, error) {
				outcome, err := execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationWriteFile, Path: input.FilePath, Content: input.Content, BaseHash: versions.base(input.FilePath)})
				if err != nil {
					return "", err
				}
				versions.record(input.FilePath, outcome)
				return outcome.Output, nil
			})
		case editFileToolName:
			created, err = utils.InferTool(name, editFileToolDesc, func(ctx context.Context, input editFileArgs) (string, error) {
				outcome, err := execute(ctx, domain.ComputerOperation{
					Kind: domain.ComputerOperationEditFile, Path: input.FilePath, OldString: input.OldString, NewString: input.NewString,
					ReplaceAll: input.ReplaceAll, BaseHash: versions.base(input.FilePath),
				})
				if err != nil {
					return "", err
				}
				versions.record(input.FilePath, outcome)
				return outcome.Output, nil
			})
		case executeToolName:
			// 命令语法随电脑的命令解释器说明。
			desc := fmt.Sprintf(executeToolDesc, request.ComputerCapabilities.Shell)
			if request.ComputerCapabilities.ManagedToolchain {
				desc += managedToolchainGuidance
			}
			created, err = utils.InferTool(name, desc, func(ctx context.Context, input executeArgs) (string, error) {
				outcome, err := execute(ctx, domain.ComputerOperation{Kind: domain.ComputerOperationCommand, Command: input.Command})
				if err != nil {
					return "", err
				}
				return outcome.Output, nil
			})
		}
		if err != nil {
			return nil, fmt.Errorf("create computer tool %s: %w", name, err)
		}
		tools = append(tools, created)
	}
	return tools, nil
}

// ComputerMCPServers 把电脑上报的本机 MCP 服务转换为本次运行的 MCP 服务，工具目录取自上报结果，调用作为操作派发到电脑。
func ComputerMCPServers(computer Computer, capabilities domain.ComputerCapabilities) []MCPServer {
	servers := make([]MCPServer, 0, len(capabilities.MCPServers))
	for _, server := range capabilities.MCPServers {
		connection := &computerMCPConnection{computer: computer, server: server}
		servers = append(servers, MCPServer{
			Source: MCPSourceLocal, ID: server.Name, Name: server.Name,
			Connect: func(context.Context) (MCPConnection, error) { return connection, nil },
		})
	}
	return servers
}

// computerMCPConnection 以电脑上报的工具目录提供本机 MCP 服务，工具调用作为操作派发到电脑。
type computerMCPConnection struct {
	computer Computer
	server   domain.ComputerMCPServer
}

// Tools 返回电脑上报的工具目录。
func (c *computerMCPConnection) Tools(context.Context) ([]mcp.Tool, error) {
	tools := make([]mcp.Tool, 0, len(c.server.Tools))
	for _, item := range c.server.Tools {
		tools = append(tools, mcp.Tool{Name: item.Name, Description: item.Description, InputSchema: item.InputSchema})
	}
	return tools, nil
}

// Call 把工具调用派发到电脑，电脑报告的失败作为错误返回。
func (c *computerMCPConnection) Call(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	outcome, err := c.computer.Execute(ctx, domain.ComputerOperation{
		Kind: domain.ComputerOperationMCPCall, MCPServer: c.server.Name, MCPTool: name, Arguments: string(arguments),
	}, suspendable(ctx))
	if err != nil {
		return "", err
	}
	return outcome.Output, nil
}

// Close 不持有资源。
func (c *computerMCPConnection) Close() error { return nil }
