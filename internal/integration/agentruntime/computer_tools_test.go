package agentruntime

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
)

// fakeComputer 记录派发的操作并按操作原语返回结果：读取返回固定正文与摘要，写入与修改返回新摘要，技能读取取自技能存储。
type fakeComputer struct {
	mu         sync.Mutex
	operations []domain.ComputerOperation
	suspends   []bool
	skills     *localskill.Store
	// await 非空时对指定原语返回 ErrAwaitExternal。
	await domain.ComputerOperationKind
}

// Execute 记录操作并返回预设结果。
func (c *fakeComputer) Execute(ctx context.Context, operation domain.ComputerOperation, suspend bool) (domain.ComputerOutcome, error) {
	c.mu.Lock()
	c.operations = append(c.operations, operation)
	c.suspends = append(c.suspends, suspend)
	c.mu.Unlock()
	if ToolCallID(ctx) == "" {
		return domain.ComputerOutcome{}, context.Canceled
	}
	if operation.Kind == c.await {
		return domain.ComputerOutcome{}, ErrAwaitExternal
	}
	switch operation.Kind {
	case domain.ComputerOperationReadFile:
		return domain.ComputerOutcome{Output: "     1\ttext view", Path: "/home/" + strings.TrimPrefix(operation.Path, "/home/"), Hash: "hash-read"}, nil
	case domain.ComputerOperationWriteFile, domain.ComputerOperationEditFile:
		return domain.ComputerOutcome{Output: "已写入", Path: "/home/" + strings.TrimPrefix(operation.Path, "/home/"), Hash: "hash-" + string(operation.Kind)}, nil
	case domain.ComputerOperationLoadSkill:
		skill, body, err := c.skills.Load(ctx, operation.Skill)
		if err != nil {
			return domain.ComputerOutcome{}, err
		}
		return domain.ComputerOutcome{Output: body, Path: skill.Dir, Files: []string{"scripts/recalc.py"}}, nil
	}
	return domain.ComputerOutcome{Output: "done"}, nil
}

// recorded 返回已派发的操作副本。
func (c *fakeComputer) recorded() []domain.ComputerOperation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.operations)
}

// computerChatModel 按调用次序返回预设输出，并记录每次调用的工具定义与输入。
type computerChatModel struct {
	mu      sync.Mutex
	calls   int
	tools   [][]*schema.ToolInfo
	inputs  [][]*schema.AgenticMessage
	outputs func(call int) *schema.AgenticMessage
}

// Generate 记录工具定义与输入并返回本次调用的预设输出。
func (m *computerChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.tools = append(m.tools, model.GetCommonOptions(nil, opts...).Tools)
	m.inputs = append(m.inputs, input)
	return m.outputs(m.calls), nil
}

// Stream 以单个分片返回模型输出。
func (m *computerChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// toolNames 返回第 index 次调用时注册的工具名称。
func (m *computerChatModel) toolNames(index int) []string {
	names := make([]string, 0, len(m.tools[index]))
	for _, info := range m.tools[index] {
		names = append(names, info.Name)
	}
	return names
}

// runComputer 以指定电脑工具与能力执行一次运行。
func runComputer(t *testing.T, chatModel *computerChatModel, computer *fakeComputer, tools []string, capabilities domain.ComputerCapabilities) RunResult {
	t.Helper()
	runtime := &EinoRuntime{}
	feed := &testInputFeed{}
	feed.appendUser("处理一下文件")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := RunRequest{
		RunID: "computer-run", Models: fixedModels(chatModel), Assignment: Assignment{AgentName: "小码", Tools: tools},
		Computer: computer, ComputerCapabilities: capabilities,
	}
	request.MCPConnections = ComputerMCPServers(computer, capabilities)
	result, err := runtime.Run(ctx, request, feed)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestComputerToolsFollowAssignment 验证只注册有效配置列出的电脑工具，调用作为操作派发到电脑并把结果交给模型；没有电脑时创建失败。
func TestComputerToolsFollowAssignment(t *testing.T) {
	chatModel := &computerChatModel{outputs: func(call int) *schema.AgenticMessage {
		if call == 1 {
			return assistantReply("", &schema.FunctionToolCall{CallID: "read-1", Name: readFileToolName, Arguments: `{"file_path":"notes.txt","offset":2,"limit":3}`})
		}
		return assistantReply("已读取")
	}}
	computer := &fakeComputer{}
	result := runComputer(t, chatModel, computer, []string{readFileToolName}, domain.ComputerCapabilities{Shell: "bash"})
	if result.Content != "已读取" {
		t.Fatalf("result=%+v", result)
	}
	tools := chatModel.toolNames(0)
	if !slices.Contains(tools, readFileToolName) || slices.Contains(tools, writeFileToolName) || slices.Contains(tools, executeToolName) {
		t.Fatalf("registered tools=%v", tools)
	}
	operations := computer.recorded()
	if len(operations) != 1 || operations[0] != (domain.ComputerOperation{Kind: domain.ComputerOperationReadFile, Path: "notes.txt", Offset: 2, Limit: 3}) || !computer.suspends[0] {
		t.Fatalf("operations=%+v suspends=%v", operations, computer.suspends)
	}
	if text := messageText(chatModel.inputs[1][len(chatModel.inputs[1])-1]); !strings.Contains(text, "text view") {
		t.Fatalf("tool result=%q", text)
	}
	if _, err := newComputerTools(RunRequest{Assignment: Assignment{Tools: []string{readFileToolName}}}, newFileVersions(FileVersions{})); err == nil {
		t.Fatal("computer tools created without computer")
	}
}

// TestComputerFileVersions 验证写入与修改带上读取或写入后的内容摘要，按模型给出的路径与解析出的绝对路径都能找到，没有记录的路径不带摘要。
func TestComputerFileVersions(t *testing.T) {
	chatModel := &computerChatModel{outputs: func(call int) *schema.AgenticMessage {
		switch call {
		case 1:
			return assistantReply("", &schema.FunctionToolCall{CallID: "read-1", Name: readFileToolName, Arguments: `{"file_path":"a.txt"}`})
		case 2:
			return assistantReply("", &schema.FunctionToolCall{CallID: "edit-1", Name: editFileToolName, Arguments: `{"file_path":"/home/a.txt","old_string":"x","new_string":"y"}`})
		case 3:
			return assistantReply("",
				&schema.FunctionToolCall{CallID: "write-1", Name: writeFileToolName, Arguments: `{"file_path":"a.txt","content":"z"}`},
				&schema.FunctionToolCall{CallID: "write-2", Name: writeFileToolName, Arguments: `{"file_path":"b.txt","content":"new"}`})
		}
		return assistantReply("改好了")
	}}
	computer := &fakeComputer{}
	runComputer(t, chatModel, computer, ComputerTools(), domain.ComputerCapabilities{Shell: "bash"})
	operations := computer.recorded()
	if len(operations) != 4 {
		t.Fatalf("operations=%+v", operations)
	}
	if operations[1].Kind != domain.ComputerOperationEditFile || operations[1].BaseHash != "hash-read" {
		t.Fatalf("edit=%+v", operations[1])
	}
	bases := map[string]string{}
	for _, operation := range operations[2:] {
		bases[operation.Path] = operation.BaseHash
	}
	if bases["a.txt"] != "hash-edit_file" || bases["b.txt"] != "" {
		t.Fatalf("write bases=%v", bases)
	}
}

// TestExecuteToolDescribesComputer 验证命令工具说明按电脑上报的命令解释器给出，提供托管运行环境时补充用法。
func TestExecuteToolDescribesComputer(t *testing.T) {
	for _, managed := range []bool{true, false} {
		chatModel := &computerChatModel{outputs: func(int) *schema.AgenticMessage { return assistantReply("完成") }}
		runComputer(t, chatModel, &fakeComputer{}, []string{executeToolName}, domain.ComputerCapabilities{Shell: "PowerShell", ManagedToolchain: managed})
		desc := ""
		for _, info := range chatModel.tools[0] {
			if info.Name == executeToolName {
				desc = info.Desc
			}
		}
		if !strings.Contains(desc, "PowerShell 命令") || strings.Contains(desc, "uv run --with") != managed {
			t.Fatalf("managed=%v 时命令工具说明不符合预期:\n%s", managed, desc)
		}
	}
}

// TestComputerSideEffectsRunOnce 验证模型空正文重试时已执行的命令与写入不重复派发。
func TestComputerSideEffectsRunOnce(t *testing.T) {
	chatModel := &computerChatModel{outputs: func(call int) *schema.AgenticMessage {
		switch call {
		case 1:
			return assistantReply("",
				&schema.FunctionToolCall{CallID: "execute-1", Name: executeToolName, Arguments: `{"command":"rm old.txt"}`},
				&schema.FunctionToolCall{CallID: "write-1", Name: writeFileToolName, Arguments: `{"file_path":"new.txt","content":"x"}`})
		case 2:
			return assistantReply("")
		}
		return assistantReply("已完成")
	}}
	computer := &fakeComputer{}
	result := runComputer(t, chatModel, computer, ComputerTools(), domain.ComputerCapabilities{Shell: "bash"})
	if result.Content != "已完成" || chatModel.calls != 3 || len(computer.recorded()) != 2 {
		t.Fatalf("result=%+v calls=%d operations=%+v", result, chatModel.calls, computer.recorded())
	}
	for _, block := range result.Blocks {
		if call := block.Payload.ToolCall; call != nil && (!call.SideEffects || call.Replayable) {
			t.Fatalf("computer call traits=%+v", call)
		}
	}
}

// TestComputerToolAwaitsResult 验证电脑结果未在等待时限内返回时调用记为等待、运行挂起。
func TestComputerToolAwaitsResult(t *testing.T) {
	chatModel := &computerChatModel{outputs: func(call int) *schema.AgenticMessage {
		if call == 1 {
			return assistantReply("", &schema.FunctionToolCall{CallID: "execute-1", Name: executeToolName, Arguments: `{"command":"make"}`})
		}
		return assistantReply("不应继续")
	}}
	computer := &fakeComputer{await: domain.ComputerOperationCommand}
	result := runComputer(t, chatModel, computer, ComputerTools(), domain.ComputerCapabilities{Shell: "bash"})
	if !result.Suspended || chatModel.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, chatModel.calls)
	}
	call := result.Blocks[len(result.Blocks)-1].Payload.ToolCall
	if call == nil || call.Status != domain.AgentToolCallWaiting {
		t.Fatalf("call=%+v", call)
	}
}

// TestComputerMCPTools 验证电脑上报的本机 MCP 工具按目录注册，调用作为操作派发到电脑并按有副作用登记。
func TestComputerMCPTools(t *testing.T) {
	name := MCPToolName(MCPSourceLocal, "notes", "notes", "append")
	chatModel := &computerChatModel{outputs: func(call int) *schema.AgenticMessage {
		if call == 1 {
			return assistantReply("", &schema.FunctionToolCall{CallID: "mcp-1", Name: name, Arguments: `{"text":"hi"}`})
		}
		return assistantReply("记好了")
	}}
	computer := &fakeComputer{}
	capabilities := domain.ComputerCapabilities{Shell: "bash", MCPServers: []domain.ComputerMCPServer{{Name: "notes", Tools: []domain.ComputerMCPTool{
		{Name: "append", Description: "追加笔记", InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)},
	}}}}
	result := runComputer(t, chatModel, computer, nil, capabilities)
	if result.Content != "记好了" || !slices.Contains(chatModel.toolNames(0), name) {
		t.Fatalf("result=%+v tools=%v", result, chatModel.toolNames(0))
	}
	operations := computer.recorded()
	if len(operations) != 1 || operations[0].Kind != domain.ComputerOperationMCPCall || operations[0].MCPServer != "notes" || operations[0].MCPTool != "append" || operations[0].Arguments != `{"text":"hi"}` {
		t.Fatalf("operations=%+v", operations)
	}
	index := slices.IndexFunc(result.Blocks, func(block Block) bool { return block.Payload.ToolCall != nil })
	if index < 0 || result.Blocks[index].Payload.ToolCall.Source != domain.AgentToolSourceMCP || !result.Blocks[index].Payload.ToolCall.SideEffects {
		t.Fatalf("blocks=%+v", result.Blocks)
	}
}

// TestSettleInterruptedKeepsComputerCalls 验证恢复时已派发到电脑且未结束的主 Agent 调用保持不变并让运行继续挂起，其余调用按特性中断。
func TestSettleInterruptedKeepsComputerCalls(t *testing.T) {
	computerCall := &ToolCall{ID: "c1", Status: domain.AgentToolCallRunning, Computer: true, SideEffects: true}
	local := &ToolCall{ID: "c2", Status: domain.AgentToolCallRunning, Replayable: true}
	blocks := []Block{{Payload: BlockPayload{ToolCall: computerCall}}, {Payload: BlockPayload{ToolCall: local}}}
	children := []ToolCall{{ID: "c3", Status: domain.AgentToolCallRunning, Computer: true, SideEffects: true}}
	changed, waiting := settleInterrupted(blocks, children, time.Now())
	if !waiting || computerCall.Status != domain.AgentToolCallRunning || local.Status != domain.AgentToolCallInterrupted || children[0].Status != domain.AgentToolCallNeedsReview || len(changed) != 2 {
		t.Fatalf("waiting=%v computer=%+v local=%+v child=%+v changed=%d", waiting, computerCall, local, children[0], len(changed))
	}
	status, result, failure := ComputerLost(false, false, true)
	if status != domain.AgentToolCallFailed || result != nil || failure == nil || *failure != ErrComputerOffline.Error() {
		t.Fatalf("unclaimed lost = %v %v %v", status, result, failure)
	}
	if status, result, _ := ComputerLost(true, false, true); status != domain.AgentToolCallNeedsReview || *result != needsReviewResult {
		t.Fatalf("claimed lost = %v %v", status, result)
	}
}

// TestFileVersionsRestoreOutcomes 验证恢复时只以调用记录中的结果补登挂起时仍在等待的文件调用，其余调用保留恢复状态中的摘要。
func TestFileVersionsRestoreOutcomes(t *testing.T) {
	versions := newFileVersions(FileVersions{Hashes: map[string]string{"/home/a.txt": "h-edit"}, Aliases: map[string]string{"a.txt": "/home/a.txt"}})
	versions.await("c2", "b.txt")
	versions.await("c4", "d.txt")
	call := func(id, name, path, hash string, status domain.AgentToolCallStatus) Block {
		return Block{Payload: BlockPayload{ToolCall: &ToolCall{
			ID: id, Name: name, Arguments: `{"file_path":"` + path + `"}`, Status: status,
			ComputerOutcome: &domain.ComputerOutcome{Path: "/home/" + path, Hash: hash},
		}}}
	}
	saved := versions.snapshot()
	restored := newFileVersions(saved)
	restored.restoreOutcomes([]Block{
		call("c1", readFileToolName, "a.txt", "h-read", domain.AgentToolCallSucceeded),
		call("c2", readFileToolName, "b.txt", "h-b", domain.AgentToolCallSucceeded),
		call("c4", writeFileToolName, "d.txt", "h-d", domain.AgentToolCallFailed),
	})
	if restored.base("a.txt") != "h-edit" || restored.base("b.txt") != "h-b" || restored.base("d.txt") != "" || len(restored.snapshot().Awaiting) != 0 {
		t.Fatalf("versions=%+v", restored.snapshot())
	}
}
