package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/integration/localskill"
)

// orchestrationChatModel 按输入区分主 Agent 与子 Agent：主 Agent 按调用次序返回预设输出，子 Agent 由 delegate 按其输入决定输出；每次输出携带固定用量。
type orchestrationChatModel struct {
	mu       sync.Mutex
	main     func(call int, input []*schema.AgenticMessage) *schema.AgenticMessage
	delegate func(input []*schema.AgenticMessage) *schema.AgenticMessage
	calls    int
	inputs   [][]*schema.AgenticMessage
	tools    [][]*schema.ToolInfo
	subTools [][]*schema.ToolInfo
	subInput [][]*schema.AgenticMessage
}

// Generate 子 Agent 的输入以子 Agent 指令开头，其余调用属于主 Agent。
func (m *orchestrationChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	delegated := len(input) > 0 && input[0].Role == schema.AgenticRoleTypeSystem && strings.Contains(messageText(input[0]), subagentSceneRules)
	m.mu.Lock()
	var output *schema.AgenticMessage
	if delegated {
		m.subTools = append(m.subTools, model.GetCommonOptions(nil, opts...).Tools)
		m.subInput = append(m.subInput, input)
		m.mu.Unlock()
		output = m.delegate(input)
	} else {
		m.calls++
		m.inputs = append(m.inputs, input)
		m.tools = append(m.tools, model.GetCommonOptions(nil, opts...).Tools)
		call := m.calls
		m.mu.Unlock()
		output = m.main(call, input)
	}
	output.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}}
	return output, nil
}

// Stream 以单个分片返回本次调用的输出。
func (m *orchestrationChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// runOrchestration 以带回显工具的运行时执行一次内部单聊运行，工具清单追加任务清单与委派工具并提供子 Agent 指令，返回结果与收到的运行流增量。
func runOrchestration(t *testing.T, chatModel *orchestrationChatModel, request RunRequest) (RunResult, []runstream.Delta) {
	t.Helper()
	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }, tools: []tool.BaseTool{echoTool}}
	feed := &testInputFeed{}
	feed.appendUser("帮我把报价整理一下")
	var mu sync.Mutex
	var deltas []runstream.Delta
	request.RunID = "orchestration-run"
	request.Assignment.AgentName = "小码"
	request.Assignment.Scene = SceneAgentChat
	request.Assignment.Tools = append(request.Assignment.Tools, planToolNames...)
	request.Assignment.Tools = append(request.Assignment.Tools, subagentToolName)
	request.Assignment.DelegateInstruction = subagentSceneRules
	request.OnStream = func(delta runstream.Delta) {
		mu.Lock()
		defer mu.Unlock()
		deltas = append(deltas, delta)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, request, feed)
	if err != nil {
		t.Fatalf("run err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return result, slices.Clone(deltas)
}

// TestResolveAssignmentOrchestration 验证任务清单与委派工具在内部场景进入工具清单并在指令中说明，与执行位置无关，服务场景不提供。
func TestResolveAssignmentOrchestration(t *testing.T) {
	facts := AssignmentFacts{OrganizationName: "测试企业", AgentName: "小码", Scene: SceneContext{Scene: SceneAgentChat}}
	for _, scene := range []Scene{SceneAgentChat, SceneGroup, SceneCopilot} {
		facts.Scene.Scene = scene
		assignment := ResolveAssignment(facts, Capabilities{})
		if !slices.Contains(assignment.Tools, "TaskCreate") || !slices.Contains(assignment.Tools, subagentToolName) ||
			!strings.Contains(assignment.Instruction, "TaskCreate、TaskGet、TaskUpdate、TaskList") || !strings.Contains(assignment.Instruction, "- agent：") {
			t.Fatalf("%s assignment = %+v", scene, assignment)
		}
	}
	// 子 Agent 指令沿用企业指令与其余工具说明，不含群聊规则、任务清单与委派工具说明。
	facts.Instruction = "报价以人民币计"
	facts.Scene = SceneContext{Scene: SceneGroup, MentionCandidates: []string{"张三"}}
	group := ResolveAssignment(facts, Capabilities{WebSearch: true})
	delegate := group.DelegateInstruction
	if !strings.Contains(delegate, subagentSceneRules) || !strings.Contains(delegate, "报价以人民币计") || !strings.Contains(delegate, "- web_search：") ||
		strings.Contains(delegate, "TaskCreate") || strings.Contains(delegate, "- agent：") || strings.Contains(delegate, "张三") {
		t.Fatalf("delegate instruction = %q", delegate)
	}
	for _, scene := range []Scene{SceneCustomer, SceneEmployeeService} {
		facts.Scene = SceneContext{Scene: scene}
		if service := ResolveAssignment(facts, Capabilities{}); slices.Contains(service.Tools, subagentToolName) || slices.Contains(service.Tools, "TaskCreate") || service.DelegateInstruction != "" {
			t.Fatalf("%s assignment = %+v", scene, service)
		}
	}
}

// TestPlanTasksFollowRun 验证任务清单随任务工具实时发布，删除的任务移出清单，全部完成后框架清理任务文件时清单保留完成状态并随结果返回。
func TestPlanTasksFollowRun(t *testing.T) {
	script := []*schema.AgenticMessage{
		assistantReply("", terminalCall("c1", "TaskCreate", `{"subject":"整理报价","description":"汇总三家报价","activeForm":"正在整理报价"}`)),
		assistantReply("", terminalCall("c2", "TaskCreate", `{"subject":"生成表格","description":"输出 Excel"}`)),
		assistantReply("", terminalCall("c3", "TaskCreate", `{"subject":"多余的任务","description":"不需要"}`)),
		assistantReply("", terminalCall("c4", "TaskUpdate", `{"taskId":"3","status":"deleted"}`)),
		assistantReply("", terminalCall("c5", "TaskUpdate", `{"taskId":"1","status":"in_progress"}`)),
		assistantReply("", terminalCall("c6", "TaskUpdate", `{"taskId":"1","status":"completed"}`)),
		assistantReply("", terminalCall("c7", "TaskUpdate", `{"taskId":"2","status":"completed"}`)),
		assistantReply("报价已整理好"),
	}
	chatModel := &orchestrationChatModel{main: func(call int, _ []*schema.AgenticMessage) *schema.AgenticMessage {
		// 模型调用间留出发布周期，每次清单变化各自成为一条增量。
		time.Sleep(2 * streamFlushInterval)
		return script[call-1]
	}}
	result, deltas := runOrchestration(t, chatModel, RunRequest{})
	want := []runstream.PlanTask{
		{ID: "1", Subject: "整理报价", ActiveForm: "正在整理报价", Status: domain.AgentPlanTaskCompleted},
		{ID: "2", Subject: "生成表格", Status: domain.AgentPlanTaskCompleted},
	}
	if result.Content != "报价已整理好" || !slices.Equal(result.Plan, want) {
		t.Fatalf("result = %+v", result)
	}
	// 运行流依次发布过含已删除任务的清单与进行中的清单。
	var plans [][]runstream.PlanTask
	for _, delta := range deltas {
		for _, operation := range delta.Operations {
			if operation.Kind == runstream.OperationSetPlan {
				plans = append(plans, operation.Plan)
			}
		}
	}
	if !slices.ContainsFunc(plans, func(plan []runstream.PlanTask) bool { return len(plan) == 3 }) ||
		!slices.ContainsFunc(plans, func(plan []runstream.PlanTask) bool {
			return len(plan) == 2 && plan[0].Status == domain.AgentPlanTaskInProgress
		}) || !slices.Equal(plans[len(plans)-1], want) {
		t.Fatalf("plans = %+v", plans)
	}
	if !slices.ContainsFunc(result.Blocks, func(block Block) bool {
		return block.Payload.ToolCall != nil && block.Payload.ToolCall.Name == "TaskCreate"
	}) {
		t.Fatalf("blocks = %+v", result.Blocks)
	}
}

// TestSubagentDelegation 验证并行委派的子 Agent 在独立上下文中执行、不带委派与任务清单工具，结论交回主 Agent，用量计入运行，运行流在委派调用上展示子任务说明与当前活动。
func TestSubagentDelegation(t *testing.T) {
	chatModel := &orchestrationChatModel{
		main: func(call int, input []*schema.AgenticMessage) *schema.AgenticMessage {
			if call == 1 {
				return assistantReply("",
					terminalCall("a1", subagentToolName, `{"subagent_type":"general","prompt":"计算 A 的合计","description":"计算 A"}`),
					terminalCall("a2", subagentToolName, `{"subagent_type":"general","prompt":"计算 B 的合计","description":"计算 B"}`))
			}
			return assistantReply("两项都算好了")
		},
		delegate: func(input []*schema.AgenticMessage) *schema.AgenticMessage {
			last := input[len(input)-1]
			task := messageText(input[1])
			if last.Role == schema.AgenticRoleTypeUser && toolResult(last) == nil {
				// 回显工具延时返回，委派调用的活动在发布周期内可见。
				return assistantReply("", terminalCall("k-"+task, "echo", `{"text":"3","delayMilliseconds":150}`))
			}
			return assistantReply(task + "：结果是 3")
		},
	}
	result, deltas := runOrchestration(t, chatModel, RunRequest{})
	if result.Content != "两项都算好了" {
		t.Fatalf("result = %+v", result)
	}
	// 主 Agent 两次调用、两个子 Agent 各两次调用，每次用量 10 + 1。
	if result.Usage.PromptTokens != 60 || result.Usage.CompletionTokens != 6 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	results := conversation(chatModel.inputs[1])
	if !slices.ContainsFunc(results, func(text string) bool { return strings.Contains(text, "计算 A 的合计：结果是 3") }) ||
		!slices.ContainsFunc(results, func(text string) bool { return strings.Contains(text, "计算 B 的合计：结果是 3") }) {
		t.Fatalf("main second input = %q", results)
	}
	for _, tools := range chatModel.subTools {
		names := make([]string, len(tools))
		for i, info := range tools {
			names[i] = info.Name
		}
		if !slices.Contains(names, "echo") || slices.Contains(names, subagentToolName) || slices.Contains(names, "TaskCreate") {
			t.Fatalf("subagent tools = %v", names)
		}
	}
	var described, active bool
	for _, delta := range deltas {
		for _, operation := range delta.Operations {
			if operation.Block == nil || operation.Block.ToolCall == nil || operation.Block.ToolCall.Name != subagentToolName {
				continue
			}
			described = described || operation.Block.ToolCall.Description == "计算 A"
			active = active || operation.Block.ToolCall.Activity == "echo"
		}
	}
	if !described || !active {
		t.Fatalf("stream described = %v, active = %v, deltas = %+v", described, active, deltas)
	}
	for _, block := range result.Blocks {
		if call := block.Payload.ToolCall; call != nil && call.Name == subagentToolName && (call.Status != domain.AgentToolCallSucceeded || call.Activity != "") {
			t.Fatalf("delegation call = %+v", call)
		}
	}
}

// TestForkSkillRunsInSubagent 验证声明 context: fork 的技能在列表中标注独立执行，只传技能名时要求补充任务说明且不启动子 Agent，传入任务说明后子 Agent 以技能说明与任务说明执行，技能调用返回子 Agent 的最终回复。
func TestForkSkillRunsInSubagent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	skillDir := filepath.Join(dir, "report")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, localskill.FileName), []byte("---\nname: report\ndescription: 生成周报\ncontext: fork\n---\n按模板生成周报"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := localskill.NewStore([]localskill.Dir{{Path: dir, Source: localskill.SourceManaged}}, func() {})
	chatModel := &orchestrationChatModel{
		main: func(call int, _ []*schema.AgenticMessage) *schema.AgenticMessage {
			switch call {
			case 1:
				return assistantReply("", terminalCall("s1", skillToolName, `{"skill":"report"}`))
			case 2:
				return assistantReply("", terminalCall("s2", skillToolName, `{"skill":"report","args":"用户要本周销售周报，数据在 ~/销售/本周.xlsx"}`))
			}
			return assistantReply("周报好了")
		},
		delegate: func(input []*schema.AgenticMessage) *schema.AgenticMessage {
			task := messageText(input[len(input)-1])
			if !strings.Contains(task, "按模板生成周报") || !strings.Contains(task, "~/销售/本周.xlsx") {
				return assistantReply("没有收到完整任务")
			}
			return assistantReply("周报已生成在默认文件夹")
		},
	}
	result, _ := runOrchestration(t, chatModel, RunRequest{
		Assignment: Assignment{Tools: []string{skillToolName}}, Workspace: &imageWorkspace{}, Skills: skills,
	})
	if result.Content != "周报好了" || len(chatModel.subInput) != 1 {
		t.Fatalf("result = %+v, subagent calls = %d", result, len(chatModel.subInput))
	}
	index := slices.IndexFunc(chatModel.tools[0], func(info *schema.ToolInfo) bool { return info.Name == skillToolName })
	if index < 0 || !strings.Contains(chatModel.tools[0][index].Desc, "- report：生成周报"+forkSkillNote) {
		t.Fatalf("skill tool = %+v", chatModel.tools[0])
	}
	missing := messageText(chatModel.inputs[1][len(chatModel.inputs[1])-1])
	if !strings.Contains(missing, "请在 args 中写清") {
		t.Fatalf("skill without args = %q", missing)
	}
	loaded := messageText(chatModel.inputs[2][len(chatModel.inputs[2])-1])
	if !strings.Contains(loaded, "已由子 Agent 执行完成") || !strings.Contains(loaded, "周报已生成在默认文件夹") {
		t.Fatalf("skill result = %q", loaded)
	}
}
