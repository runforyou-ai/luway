package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// interruptedReplayableResult 是可重新执行的调用中断后交给模型的结果。
	interruptedReplayableResult = "执行被中断，没有产生结果，需要时可以重新调用。"
	// interruptedResult 是不可重新执行、没有外部副作用的调用中断后交给模型的结果。
	interruptedResult = "执行被中断，结果未知。"
	// needsReviewResult 是有外部副作用的调用中断后交给模型的结果。
	needsReviewResult = "执行被中断，外部操作的实际结果未知，已交由人工核对，不要重复执行。"
	// cancelledToolResult 是没有结果的调用在调用模型前补上的说明，依次填入工具名与调用标识。
	cancelledToolResult = "工具调用 %s（ID 为 %s）已被取消——在其完成之前收到了另一条消息。"
)

// sideEffectToolNames 是会修改电脑文件、命令环境或本机配置的内置工具。
var sideEffectToolNames = []string{
	"write_file", "edit_file", "delete_file", "execute",
	addLocalMCPToolName, removeLocalMCPToolName, installSkillToolName, removeSkillToolName,
}

// registerToolTraits 登记本次运行可调用工具的来源与特性：MCP 工具按管理员标记的查询用途判断，修改电脑或本机配置的内置工具有副作用，
// 委派调用在子 Agent 可用工具中有副作用工具时视为有副作用，其余内置工具与框架工具可重新执行且没有副作用。
func registerToolTraits(ctx context.Context, traits map[string]toolTraits, tools []tool.BaseTool, frameworkTools []string, delegation bool) error {
	readOnly := toolTraits{source: domain.AgentToolSourceBuiltin, replayable: true}
	for _, name := range frameworkTools {
		traits[name] = readOnly
	}
	for _, item := range tools {
		info, err := item.Info(ctx)
		if err != nil {
			return fmt.Errorf("read tool info: %w", err)
		}
		switch remote, isMCP := item.(*mcpTool); {
		case isMCP:
			traits[info.Name] = toolTraits{source: domain.AgentToolSourceMCP, replayable: remote.query, sideEffects: !remote.query}
		case slices.Contains(sideEffectToolNames, info.Name):
			traits[info.Name] = toolTraits{source: domain.AgentToolSourceBuiltin, sideEffects: true}
		default:
			traits[info.Name] = readOnly
		}
	}
	for _, name := range sideEffectToolNames {
		if _, registered := traits[name]; registered {
			traits[name] = toolTraits{source: domain.AgentToolSourceBuiltin, sideEffects: true}
		}
	}
	if delegation {
		delegated := toolTraits{source: domain.AgentToolSourceDelegation}
		for name, item := range traits {
			if name != subagentToolName && item.sideEffects {
				delegated.sideEffects = true
			}
		}
		traits[subagentToolName] = delegated
	}
	return nil
}

// settleInterrupted 把恢复时仍未结束的调用按特性改为中断或待核对并写入交给模型的结果，返回改动的调用；waiting 返回仍在等待外部结果的调用是否存在。
func settleInterrupted(blocks []Block, children []ToolCall, at time.Time) (changed []ToolCall, waiting bool) {
	settle := func(call *ToolCall) {
		switch call.Status {
		case domain.AgentToolCallWaiting:
			waiting = true
			return
		case domain.AgentToolCallQueued, domain.AgentToolCallRunning:
		default:
			return
		}
		result := interruptedResult
		switch {
		case call.SideEffects && !call.Replayable:
			call.Status, result = domain.AgentToolCallNeedsReview, needsReviewResult
		case call.Replayable:
			call.Status, result = domain.AgentToolCallInterrupted, interruptedReplayableResult
		default:
			call.Status = domain.AgentToolCallInterrupted
		}
		call.Result, call.CompletedAt = &result, &at
		changed = append(changed, *call)
	}
	for _, block := range blocks {
		if block.Payload.ToolCall != nil {
			settle(block.Payload.ToolCall)
		}
	}
	for index := range children {
		// 子 Agent 不挂起，恢复时仍在执行的子 Agent 调用一律视为中断。
		if children[index].Status == domain.AgentToolCallWaiting {
			children[index].Status = domain.AgentToolCallRunning
		}
		settle(&children[index])
	}
	return changed, waiting
}

// modelToolResult 返回已结束调用交给模型的结果正文：失败按工具错误的编码返回。
func modelToolResult(call *ToolCall) (string, bool) {
	switch {
	case call.Status == domain.AgentToolCallFailed && call.Error != nil:
		encoded, err := json.Marshal(struct {
			Error string `json:"error"`
		}{Error: *call.Error})
		if err != nil {
			return "", false
		}
		return string(encoded), true
	case call.Status.Settled() && call.Result != nil:
		return *call.Result, true
	}
	return "", false
}

// newToolCallPatchHandler 创建工具调用修补中间件：没有结果的调用按 results 补上已保存的结果，查不到时补上取消说明；没有对应调用或重复的结果从模型输入中移除。
func newToolCallPatchHandler(ctx context.Context, results func(callID string) (string, bool)) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	handler, err := patchtoolcalls.NewTyped[*schema.AgenticMessage](ctx, &patchtoolcalls.Config{
		RemoveOrphanResults: true, RemoveDuplicateResults: true,
		PatchedToolResultGenerator: func(_ context.Context, toolName, callID string, _ *schema.ToolArgument) (*patchtoolcalls.PatchedToolResult, error) {
			if results != nil {
				if result, ok := results(callID); ok {
					return &patchtoolcalls.PatchedToolResult{Content: result}, nil
				}
			}
			return &patchtoolcalls.PatchedToolResult{Content: fmt.Sprintf(cancelledToolResult, toolName, callID)}, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create tool call patch middleware: %w", err)
	}
	return handler, nil
}

// export 返回终止工具的纠正额度与已登记意图。
func (t *terminalTools) export() terminalState {
	if t == nil {
		return terminalState{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := terminalState{Corrections: t.corrections, Handoff: t.handoff, Intents: make(map[string]terminalIntentState, len(t.intents))}
	for callID, intent := range t.intents {
		state.Intents[callID] = terminalIntentState{Decision: intent.decision, Message: intent.message}
	}
	if t.forced != nil {
		state.Forced = &terminalIntentState{Decision: t.forced.decision, Message: t.forced.message}
	}
	return state
}

// restore 写回终止工具的纠正额度与已登记意图。
func (t *terminalTools) restore(state terminalState) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.corrections, t.handoff = state.Corrections, state.Handoff
	for callID, intent := range state.Intents {
		t.intents[callID] = terminalIntent{decision: intent.Decision, message: intent.Message}
	}
	if state.Forced != nil {
		t.forced = &terminalIntent{decision: state.Forced.Decision, message: state.Forced.Message}
	}
}

// export 返回当前输入边界内的依据与最近一次判定。
func (g *groundingGate) export() (map[string]string, bool) {
	if g == nil {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return maps.Clone(g.valid), g.grounded
}

// restore 写回当前输入边界内的依据与最近一次判定。
func (g *groundingGate) restore(valid map[string]string, grounded bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.valid = maps.Clone(valid)
	if g.valid == nil {
		g.valid = make(map[string]string)
	}
	g.grounded = grounded
}

// spent 返回当前轮次已进行的模型规划次数。
func (g *finalIterationGuard) spent() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.iterations
}

// resume 写回已进行的模型规划次数，恢复后的执行沿用剩余预算。
func (g *finalIterationGuard) resume(iterations int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.iterations, g.carry = iterations, true
}

// keptFrom 返回摘要保留点的消息编号。
func (s *contextSummarizer) keptFrom() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keepFromID
}

// restoreKeepFrom 写回摘要保留点的消息编号。
func (s *contextSummarizer) restoreKeepFrom(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keepFromID = id
}

// withoutSystem 返回去掉系统指令后的消息，系统指令由 Agent 在每次执行时重新加入。
func withoutSystem(messages []*schema.AgenticMessage) []*schema.AgenticMessage {
	return slices.DeleteFunc(slices.Clone(messages), func(message *schema.AgenticMessage) bool {
		return message.Role == schema.AgenticRoleTypeSystem
	})
}
