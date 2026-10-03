package agentruntime

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

// memoryJournal 在内存中保存安全点与工具调用的最新状态。
type memoryJournal struct {
	mu    sync.Mutex
	steps []Step
	calls map[string]ToolCall
	saves []domain.AgentToolCallStatus
}

// SaveStep 记录安全点。
func (j *memoryJournal) SaveStep(_ context.Context, step Step) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.steps = append(j.steps, step)
	return nil
}

// SaveToolCall 记录工具调用的最新状态。
func (j *memoryJournal) SaveToolCall(_ context.Context, call ToolCall) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.calls == nil {
		j.calls = make(map[string]ToolCall)
	}
	j.calls[call.ID] = call
	j.saves = append(j.saves, call.Status)
	return nil
}

// lastStep 返回最近一次保存的安全点，其内容块中的工具调用替换为最新状态。
func (j *memoryJournal) lastStep(t *testing.T) Step {
	t.Helper()
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.steps) == 0 {
		t.Fatal("no step saved")
	}
	step := j.steps[len(j.steps)-1]
	blocks := slices.Clone(step.Blocks)
	for index, block := range blocks {
		if block.Payload.ToolCall == nil {
			continue
		}
		call := *block.Payload.ToolCall
		if latest, ok := j.calls[call.ID]; ok {
			call = latest
		}
		blocks[index].Payload.ToolCall = &call
	}
	step.Blocks = blocks
	return step
}

// awaitInput 是测试用等待外部结果工具的参数。
type awaitInput struct {
	Question string `json:"question"`
}

// newAwaitTool 创建总是等待外部结果的测试工具。
func newAwaitTool() (tool.InvokableTool, error) {
	return toolutils.InferTool("await_result", "Wait for an external result.", func(context.Context, awaitInput) (string, error) {
		return "", ErrAwaitExternal
	})
}

// scriptedModel 按调用顺序返回预设输出，并记录每次调用看到的上下文与模型调用编号。
type scriptedModel struct {
	mu       sync.Mutex
	outputs  []*schema.AgenticMessage
	inputs   [][]*schema.AgenticMessage
	callIDs  []string
	position int
}

// next 返回下一条预设输出并记录本次输入。
func (m *scriptedModel) next(ctx context.Context, input []*schema.AgenticMessage) *schema.AgenticMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inputs = append(m.inputs, slices.Clone(input))
	m.callIDs = append(m.callIDs, ModelCallID(ctx))
	output := m.outputs[min(m.position, len(m.outputs)-1)]
	m.position++
	return output
}

// Generate 返回下一条预设输出。
func (m *scriptedModel) Generate(ctx context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	return m.next(ctx, input), nil
}

// Stream 以单分片流返回下一条预设输出。
func (m *scriptedModel) Stream(ctx context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.next(ctx, input), nil)
}

// toolResultTexts 返回上下文中各工具结果按调用标识索引的文本。
func toolResultTexts(messages []*schema.AgenticMessage) map[string]string {
	results := make(map[string]string)
	for _, message := range messages {
		if result := toolResult(message); result != nil {
			results[result.CallID] = messageText(message)
		}
	}
	return results
}

// TestRunJournalSavesStepsAndToolCalls 验证每次模型输出定稿后保存安全点，工具调用带调用编号、模型调用编号与特性并逐次写入状态。
func TestRunJournalSavesStepsAndToolCalls(t *testing.T) {
	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	chatModel := &scriptedModel{outputs: []*schema.AgenticMessage{
		assistantReply("", &schema.FunctionToolCall{CallID: "e1", Name: "echo", Arguments: `{"text":"3"}`}),
		assistantReply("结果是 3"),
	}}
	journal := &memoryJournal{}
	feed := &testInputFeed{}
	feed.appendUser("计算")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime := &EinoRuntime{tools: []tool.BaseTool{echoTool}}
	result, err := runtime.Run(ctx, RunRequest{RunID: "journal-run", Assignment: Assignment{AgentName: "test"}, Models: fixedModels(chatModel), Journal: journal}, feed)
	if err != nil || result.Content != "结果是 3" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	// 两次模型输出定稿与一批工具结束各保存一次。
	if len(journal.steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(journal.steps))
	}
	call := journal.steps[0].Blocks[0].Payload.ToolCall
	if call == nil || call.ID == "" || call.ModelCallID != chatModel.callIDs[0] || call.Source != domain.AgentToolSourceBuiltin || !call.Replayable || call.SideEffects {
		t.Fatalf("saved tool call = %#v, model call ids = %v", call, chatModel.callIDs)
	}
	if !slices.Equal(journal.saves, []domain.AgentToolCallStatus{domain.AgentToolCallRunning, domain.AgentToolCallSucceeded}) {
		t.Fatalf("tool call saves = %v", journal.saves)
	}
	state, err := decodeCheckpoint(journal.steps[2].State)
	if err != nil {
		t.Fatal(err)
	}
	if state.ClaimedSeq != 1 || state.Turns != 1 || len(state.Messages) != 4 {
		t.Fatalf("checkpoint = claimed %d, turns %d, messages %d", state.ClaimedSeq, state.Turns, len(state.Messages))
	}
}

// TestRunSuspendsAndResumesWithExternalResult 验证等待外部结果的调用使运行在本批工具结束后挂起，恢复时模型收到外部结果并继续。
func TestRunSuspendsAndResumesWithExternalResult(t *testing.T) {
	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	awaitTool, err := newAwaitTool()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &EinoRuntime{tools: []tool.BaseTool{echoTool, awaitTool}}
	journal := &memoryJournal{}
	feed := &testInputFeed{}
	feed.appendUser("查询并回显")
	first := &scriptedModel{outputs: []*schema.AgenticMessage{assistantReply("",
		&schema.FunctionToolCall{CallID: "e1", Name: "echo", Arguments: `{"text":"ok"}`},
		&schema.FunctionToolCall{CallID: "w1", Name: "await_result", Arguments: `{"question":"q"}`},
	)}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request := RunRequest{RunID: "suspend-run", Assignment: Assignment{AgentName: "test"}, Models: fixedModels(first), Journal: journal}
	result, err := runtime.Run(ctx, request, feed)
	if err != nil || !result.Suspended {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(first.inputs) != 1 {
		t.Fatalf("model calls before suspension = %d", len(first.inputs))
	}
	step := journal.lastStep(t)
	statuses := map[string]domain.AgentToolCallStatus{}
	for _, block := range step.Blocks {
		if call := block.Payload.ToolCall; call != nil {
			statuses[call.CallID] = call.Status
			// 外部结果送达后调用记为成功。
			if call.CallID == "w1" {
				resolved := "外部结果 42"
				call.Status, call.Result = domain.AgentToolCallSucceeded, &resolved
			}
		}
	}
	if statuses["e1"] != domain.AgentToolCallSucceeded || statuses["w1"] != domain.AgentToolCallWaiting {
		t.Fatalf("statuses before resume = %v", statuses)
	}

	second := &scriptedModel{outputs: []*schema.AgenticMessage{assistantReply("完成：42")}}
	request.Models = fixedModels(second)
	request.Resume = &Resume{State: step.State, Blocks: step.Blocks, Calls: step.Calls, Plan: step.Plan}
	result, err = runtime.Run(ctx, request, feed)
	if err != nil || result.Content != "完成：42" || result.EndSeq != 1 {
		t.Fatalf("resumed result = %#v, err = %v", result, err)
	}
	results := toolResultTexts(second.inputs[0])
	if results["w1"] != "外部结果 42" || !strings.Contains(results["e1"], "ok") {
		t.Fatalf("resumed tool results = %v", results)
	}
}

// TestRunResumeInterruptsUnfinishedCalls 验证崩溃恢复时未结束的调用按特性改为中断或待核对，模型收到对应说明。
func TestRunResumeInterruptsUnfinishedCalls(t *testing.T) {
	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &EinoRuntime{tools: []tool.BaseTool{echoTool}}
	journal := &memoryJournal{}
	feed := &testInputFeed{}
	feed.appendUser("回显")
	first := &scriptedModel{outputs: []*schema.AgenticMessage{
		assistantReply("", &schema.FunctionToolCall{CallID: "e1", Name: "echo", Arguments: `{"text":"ok"}`}),
		assistantReply("完成"),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request := RunRequest{RunID: "crash-run", Assignment: Assignment{AgentName: "test"}, Models: fixedModels(first), Journal: journal}
	if _, err := runtime.Run(ctx, request, feed); err != nil {
		t.Fatal(err)
	}
	// 取第一次模型输出定稿时的安全点，并把调用视为崩溃时仍在执行。
	step := journal.steps[0]
	blocks := slices.Clone(step.Blocks)
	running := *blocks[0].Payload.ToolCall
	running.Status = domain.AgentToolCallRunning
	blocks[0].Payload.ToolCall = &running

	second := &scriptedModel{outputs: []*schema.AgenticMessage{assistantReply("重新回答")}}
	journal.saves = nil
	request.Models = fixedModels(second)
	request.Resume = &Resume{State: step.State, Blocks: blocks}
	result, err := runtime.Run(ctx, request, feed)
	if err != nil || result.Content != "重新回答" {
		t.Fatalf("resumed result = %#v, err = %v", result, err)
	}
	if got := toolResultTexts(second.inputs[0])["e1"]; got != interruptedReplayableResult {
		t.Fatalf("interrupted tool result = %q", got)
	}
	if len(journal.saves) == 0 || journal.saves[0] != domain.AgentToolCallInterrupted {
		t.Fatalf("tool call saves after resume = %v", journal.saves)
	}
}

// TestRunResumeKeepsPlanAfterToolBatch 验证一批工具结束后保存的恢复状态包含工具写入的任务清单，崩溃恢复后仍可读取。
func TestRunResumeKeepsPlanAfterToolBatch(t *testing.T) {
	runtime := &EinoRuntime{}
	journal := &memoryJournal{}
	feed := &testInputFeed{}
	feed.appendUser("先列计划")
	assignment := Assignment{AgentName: "test", Tools: planToolNames}
	first := &scriptedModel{outputs: []*schema.AgenticMessage{
		assistantReply("", &schema.FunctionToolCall{CallID: "p1", Name: plantask.TaskCreateToolName, Arguments: `{"subject":"整理报价","description":"汇总三家报价"}`}),
		assistantReply("计划已建立"),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request := RunRequest{RunID: "plan-run", Assignment: assignment, Models: fixedModels(first), Journal: journal}
	if _, err := runtime.Run(ctx, request, feed); err != nil {
		t.Fatal(err)
	}
	// 取工具批次结束后的安全点，模拟随后崩溃。
	step := journal.steps[1]
	second := &scriptedModel{outputs: []*schema.AgenticMessage{
		assistantReply("", &schema.FunctionToolCall{CallID: "p2", Name: plantask.TaskListToolName, Arguments: `{}`}),
		assistantReply("继续执行"),
	}}
	request.Models = fixedModels(second)
	request.Resume = &Resume{State: step.State, Blocks: journal.lastStep(t).Blocks[:1], Plan: step.Plan}
	if _, err := runtime.Run(ctx, request, feed); err != nil {
		t.Fatal(err)
	}
	if listed := toolResultTexts(second.inputs[1])["p2"]; !strings.Contains(listed, "整理报价") {
		t.Fatalf("plan after resume = %q", listed)
	}
}

// TestRunResumeKeepsPlanMidBatch 验证同批工具中任务清单工具先结束后即保存执行期状态，其余工具仍在执行时崩溃，恢复后任务清单仍在。
func TestRunResumeKeepsPlanMidBatch(t *testing.T) {
	echoTool, err := newEchoTool()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &EinoRuntime{tools: []tool.BaseTool{echoTool}}
	journal := &memoryJournal{}
	feed := &testInputFeed{}
	feed.appendUser("列计划并回显")
	assignment := Assignment{AgentName: "test", Tools: planToolNames}
	first := &scriptedModel{outputs: []*schema.AgenticMessage{
		assistantReply("",
			&schema.FunctionToolCall{CallID: "p1", Name: plantask.TaskCreateToolName, Arguments: `{"subject":"整理报价","description":"汇总三家报价"}`},
			&schema.FunctionToolCall{CallID: "e1", Name: "echo", Arguments: `{"text":"slow","delayMilliseconds":100}`},
		),
		assistantReply("完成"),
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request := RunRequest{RunID: "midbatch-run", Assignment: assignment, Models: fixedModels(first), Journal: journal}
	if _, err := runtime.Run(ctx, request, feed); err != nil {
		t.Fatal(err)
	}
	// 找到任务清单工具已结束、回显工具尚未结束时保存的安全点。
	var step Step
	found := false
	for _, saved := range journal.steps {
		statuses := map[string]domain.AgentToolCallStatus{}
		for _, block := range saved.Blocks {
			if call := block.Payload.ToolCall; call != nil {
				statuses[call.CallID] = call.Status
			}
		}
		if statuses["p1"] == domain.AgentToolCallSucceeded && statuses["e1"] != domain.AgentToolCallSucceeded {
			step, found = saved, true
			break
		}
	}
	if !found {
		t.Fatal("no step saved between the two tool results")
	}
	second := &scriptedModel{outputs: []*schema.AgenticMessage{
		assistantReply("", &schema.FunctionToolCall{CallID: "p2", Name: plantask.TaskListToolName, Arguments: `{}`}),
		assistantReply("继续执行"),
	}}
	request.Models = fixedModels(second)
	request.Resume = &Resume{State: step.State, Blocks: step.Blocks, Plan: step.Plan}
	if _, err := runtime.Run(ctx, request, feed); err != nil {
		t.Fatal(err)
	}
	if got := toolResultTexts(second.inputs[0])["e1"]; got != interruptedReplayableResult {
		t.Fatalf("unfinished echo result = %q", got)
	}
	if listed := toolResultTexts(second.inputs[1])["p2"]; !strings.Contains(listed, "整理报价") {
		t.Fatalf("plan after mid-batch resume = %q", listed)
	}
}
