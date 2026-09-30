package agentruntime

import (
	"context"
	"log/slog"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// finalIterationGuard 在迭代预算用尽时收敛工具，让模型基于已获得的信息结束本轮；客服场景保留追问与转人工两个终止工具。
type finalIterationGuard struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	maxIterations int
	keepTerminal  bool

	mu         sync.Mutex
	iterations int
	carry      bool // 下一次执行沿用当前边界的剩余预算，用于依据纠正的重新执行。
	exhausted  bool // 本轮最近一次模型规划发生在预算末端。
}

// newFinalIterationGuard 按本次运行的迭代上限创建收尾守卫，keepTerminal 为 true 时预算末端保留终止工具。
func newFinalIterationGuard(maxIterations int, keepTerminal bool) *finalIterationGuard {
	return &finalIterationGuard{maxIterations: maxIterations, keepTerminal: keepTerminal}
}

// BeforeAgent 在每轮开始时重置模型规划计数，依据纠正的重新执行沿用剩余预算。
func (g *finalIterationGuard) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext[*schema.AgenticMessage]) (context.Context, *adk.ChatModelAgentContext[*schema.AgenticMessage], error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.carry {
		g.carry = false
		return ctx, runCtx, nil
	}
	g.iterations, g.exhausted = 0, false
	return ctx, runCtx, nil
}

// BeforeModelRewriteState 记录模型规划次数，预算用尽时收敛工具并要求模型给出最终结果。
func (g *finalIterationGuard) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.iterations++
	if g.iterations < g.maxIterations {
		return ctx, state, nil
	}
	g.exhausted = true
	slog.Warn("Agent 工具调用次数达到本轮上限，进入收尾规划",
		"agent_run_id", runIDFromContext(ctx), "max_iterations", g.maxIterations)
	state.DeferredToolInfos = nil
	if !g.keepTerminal {
		// 空工具列表必须保持非 nil，否则框架按未设置处理并填回全量工具。
		state.ToolInfos = []*schema.ToolInfo{}
		state.Messages = append(state.Messages, schema.UserAgenticMessage("工具调用次数已达本轮上限，请基于已获得的信息给出最终回答。"))
		return ctx, state, nil
	}
	// 客服场景只保留追问、转人工与确认解决，模型在预算末端仍有出口。
	kept := make([]*schema.ToolInfo, 0, 3)
	for _, info := range state.ToolInfos {
		if isTerminalToolName(info.Name) {
			kept = append(kept, info)
		}
	}
	state.ToolInfos = kept
	state.Messages = append(state.Messages, schema.UserAgenticMessage("工具调用次数已达本轮上限，不能再查询资料。已取得依据时直接给出最终回答；需要客户补充信息请调用 ask_customer；无法解答请调用 handoff_to_human。"))
	return ctx, state, nil
}

// carryBudget 让下一次执行沿用当前边界的剩余迭代预算。
func (g *finalIterationGuard) carryBudget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.carry = true
}

// budgetExhausted 判断本轮最近一次模型规划是否发生在预算末端。
func (g *finalIterationGuard) budgetExhausted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.exhausted
}
