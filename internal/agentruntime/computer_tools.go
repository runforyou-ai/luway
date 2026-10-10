package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
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

// readFileToolDesc 是读取文件工具的说明。
const readFileToolDesc = `读取电脑上的文本文件，结果以 cat -n 格式带行号返回，行号从 1 开始，默认最多读取 2000 行。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 已知需要的范围时用 offset 与 limit 只读取该部分。
- 二进制文件与超过 10 MB 的文件无法读取。`

// writeFileToolDesc 是写入文件工具的说明。
const writeFileToolDesc = `以完整内容创建或覆盖电脑上的文件，缺少的上级文件夹会一并创建。
- file_path 可以是绝对路径、~ 开头的路径或相对默认文件夹的路径。
- 覆盖已有文件前必须先用 read_file 读取；读取后文件被改动过时写入失败，重新读取后再写。局部修改使用 edit_file。
- 只写入文本；Word、Excel、PPT 等文件用 execute 运行脚本生成。`

// editFileToolDesc 是修改文件工具的说明。
const editFileToolDesc = `把电脑上文本文件中的一段原文精确替换为新内容。
- 修改前必须先用 read_file 读取文件；读取后文件被改动过时修改失败，重新读取后再改。
- old_string 必须与文件内容完全一致（包括缩进与换行），不含 read_file 输出的行号前缀。
- old_string 在文件中必须唯一，否则补充上下文使其唯一；replace_all 为 true 时替换全部出现处。
- new_string 为空表示删除这段原文。`

// executeToolDesc 是执行命令工具的说明模板，%s 处填入电脑的命令解释器。
const executeToolDesc = `在电脑上执行一条 %s 命令，返回合并后的标准输出与标准错误。
- 工作目录是本会话的默认文件夹，其中的 shared/ 是本会话共享文件区的副本，命令执行前后自动与共享文件区同步：命令可以直接读写 shared/ 下的文件，改动会回到共享文件区。
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

// Computer 是运行使用的电脑，执行文件、命令、本机 MCP、技能、浏览器与桌面操作。
type Computer interface {
	// Execute 把当前工具调用作为操作派发到电脑并返回电脑上报的结果；suspend 为 true 时结果未在等待时限内返回即返回 ErrAwaitExternal，
	// 结果写入调用记录后运行被唤醒；操作失败或电脑断开时返回交给模型的错误。
	Execute(ctx context.Context, operation domain.ComputerOperation, suspend bool) (domain.ComputerOutcome, error)
	// Target 返回操作派发到这台电脑时的电脑编号与实际执行的操作，需要人工介入的调用提交时记录，批准后原样派发。
	Target(operation domain.ComputerOperation) agentcontract.ComputerTarget
	// Submit 把当前工具调用作为委派本机 Agent 的一轮交给本机 Agent 会话后立即返回交给模型的结果，这一轮此后由会话推进；电脑离线时返回交给模型的错误。
	Submit(ctx context.Context, operation domain.ComputerOperation) (string, error)
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

// fileVersionsName 是文件内容摘要扩展的名称。
const fileVersionsName = "luway.file_versions"

// fileVersionState 是文件内容摘要在恢复状态中的内容：按绝对路径登记的摘要、模型给出的路径到绝对路径的别名，以及结果尚未送达的文件调用的路径。
type fileVersionState struct {
	Hashes   map[string]string `json:"hashes,omitempty"`
	Aliases  map[string]string `json:"aliases,omitempty"`
	Awaiting map[string]string `json:"awaiting,omitempty"`
}

// fileVersions 记录本次运行读取或写入后电脑上文件内容的摘要，主 Agent 与子 Agent 共用：摘要按电脑解析出的绝对路径登记，模型给出的路径指向其绝对路径，写入与修改已有文件时以它作为条件；
// 结果未在等待时限内送达的文件调用按调用记录编号记入 awaiting，恢复时以调用记录中电脑上报的结果补登。它作为运行扩展保存在恢复状态中。
type fileVersions struct {
	mu       sync.Mutex
	hashes   map[string]string
	aliases  map[string]string
	awaiting map[string]string
}

// newFileVersions 创建空的文件版本记录。
func newFileVersions() *fileVersions {
	return &fileVersions{hashes: map[string]string{}, aliases: map[string]string{}, awaiting: map[string]string{}}
}

// Name 返回扩展名称。
func (v *fileVersions) Name() string { return fileVersionsName }

// Save 返回当前摘要、路径别名与等待结果的调用，写入恢复状态。
func (v *fileVersions) Save() (json.RawMessage, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return json.Marshal(fileVersionState{Hashes: v.hashes, Aliases: v.aliases, Awaiting: v.awaiting})
}

// Restore 以恢复状态中的内容重建记录。
func (v *fileVersions) Restore(data json.RawMessage) error {
	var state fileVersionState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.hashes, v.aliases, v.awaiting = maps.Clone(state.Hashes), maps.Clone(state.Aliases), maps.Clone(state.Awaiting)
	for _, values := range []*map[string]string{&v.hashes, &v.aliases, &v.awaiting} {
		if *values == nil {
			*values = map[string]string{}
		}
	}
	return nil
}

// AfterRestore 以调用记录载荷中电脑上报的结果补登挂起时仍在等待的文件调用的摘要。
func (v *fileVersions) AfterRestore(_ context.Context, view einorun.RestoreView) error {
	calls := slices.Clone(view.Calls)
	for _, block := range view.Blocks {
		if block.Call != nil {
			calls = append(calls, *block.Call)
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, call := range calls {
		path, ok := v.awaiting[call.ID]
		if !ok {
			continue
		}
		delete(v.awaiting, call.ID)
		var dispatched domain.ComputerCall
		if call.Status != einorun.StatusSucceeded || json.Unmarshal(call.Payload, &dispatched) != nil || dispatched.Outcome == nil {
			continue
		}
		if outcome := *dispatched.Outcome; outcome.Hash != "" && outcome.Path != "" {
			v.recordLocked(path, outcome)
		}
	}
	return nil
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

// computerTool 把模型给出的参数转换为电脑操作并派发到电脑，返回电脑上报的结果正文；文件操作完成后登记文件内容摘要。
type computerTool struct {
	info      *schema.ToolInfo
	policy    agentcontract.ToolPolicy
	operation func(arguments string) (domain.ComputerOperation, error) // 参数无法转换时返回交给模型的错误。
	computer  Computer
	versions  *fileVersions
	sequencer *interfaceSequencer // 浏览器与桌面工具按模型给出的顺序执行，其余工具为空。
	agent     string              // 工具所属 Agent 的标识，界面调用按它排序。
	// detached 表示调用交给本机 Agent 会话后立即返回，由会话推进，只有委派本机 Agent 的工具取值。
	detached bool
}

// newComputerTool 创建参数结构为 A 的电脑工具，参数定义由 A 推导。
func newComputerTool[A any](name, desc string, build func(A) (domain.ComputerOperation, error)) (*computerTool, error) {
	info, err := utils.GoStruct2ToolInfo[A](name, desc)
	if err != nil {
		return nil, err
	}
	return &computerTool{info: info, operation: func(arguments string) (domain.ComputerOperation, error) {
		var input A
		if err := json.Unmarshal([]byte(arguments), &input); err != nil {
			return domain.ComputerOperation{}, errors.New("参数不是合法 JSON，请重新提交。")
		}
		return build(input)
	}}, nil
}

// newComputerMCPTool 按已解析的参数定义创建电脑上一个本机 MCP 工具，描述开头注明来自这台电脑上的服务。
func newComputerMCPTool(server string, item domain.ComputerMCPTool, parameters *jsonschema.Schema) *computerTool {
	return &computerTool{
		info: &schema.ToolInfo{
			Name:        MCPToolName(server, item.Name),
			Desc:        strings.TrimSpace(fmt.Sprintf("［这台电脑 · %s］%s", server, item.Description)),
			ParamsOneOf: schema.NewParamsOneOfByJSONSchema(parameters),
		},
		operation: func(arguments string) (domain.ComputerOperation, error) {
			if !json.Valid([]byte(arguments)) {
				return domain.ComputerOperation{}, errors.New("参数不是合法 JSON，请重新提交。")
			}
			return domain.ComputerOperation{Kind: domain.ComputerOperationMCPCall, MCPServer: server, MCPTool: item.Name, Arguments: arguments}, nil
		},
	}
}

// Info 返回模型可见的名称、描述和参数定义。
func (t *computerTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 把调用作为操作派发到电脑并返回电脑上报的结果正文；委派本机 Agent 的调用交给会话后以回执交出；同一批中前一个界面调用仍在等待结果时界面调用不派发；
// 可挂起的调用超过等待时长时交给电脑继续推进，运行随后挂起，文件调用记入等待，恢复时据调用记录补登摘要。
func (t *computerTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.sequencer != nil && t.sequencer.held(t.agent) {
		return "", errInterfaceAwaiting
	}
	operation, err := t.operation(argumentsInJSON)
	if err != nil {
		return "", err
	}
	if t.detached {
		receipt, err := t.computer.Submit(ctx, operation)
		if err != nil {
			return "", err
		}
		return "", einorun.Detached(receipt, dispatchPayload(t.computer.Target(operation)))
	}
	outcome, err := t.computer.Execute(ctx, operation, einorun.CanSuspend(ctx))
	if errors.Is(err, ErrAwaitExternal) {
		if t.sequencer != nil {
			t.sequencer.hold(t.agent)
		}
		if call, ok := einorun.CallFrom(ctx); ok && operation.Path != "" {
			t.versions.await(call.RecordID, operation.Path)
		}
		return "", einorun.Await(dispatchPayload(t.computer.Target(operation)))
	}
	if err != nil {
		return "", err
	}
	if operation.Path != "" {
		t.versions.record(operation.Path, outcome)
	}
	return outcome.Output, nil
}

// dispatchPayload 返回交给电脑推进的调用载荷：派发的操作。
func dispatchPayload(target agentcontract.ComputerTarget) json.RawMessage {
	payload, _ := json.Marshal(domain.ComputerCall{Operation: target.Operation})
	return payload
}

// target 把模型给出的参数转换为批准后派发的电脑与操作。
func (t *computerTool) target(arguments string) (agentcontract.ComputerTarget, error) {
	operation, err := t.operation(arguments)
	if err != nil {
		return agentcontract.ComputerTarget{}, err
	}
	return t.computer.Target(operation), nil
}
