//go:build server

package agentevaluation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
)

// TestJudgeStateFitsContextWindow 验证工具返回与前文共用判断模型窗口一半的预算：先保留最新的工具返回，再从新到旧保留前文。
func TestJudgeStateFitsContextWindow(t *testing.T) {
	long := strings.Repeat("退款规则说明", 300)
	messages := make([]ContextMessage, 0, 30)
	for range 30 {
		messages = append(messages, ContextMessage{Sender: "customer", Body: long})
	}
	messages = append(messages, ContextMessage{Sender: "ai", Body: "最新前文"})
	result := agentruntime.RunResult{Content: "回复", Blocks: []agentruntime.Block{
		{Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &agentruntime.ToolCall{Name: "search_knowledge", Result: new("较早的检索结果")}}},
		{Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &agentruntime.ToolCall{Name: "search_knowledge", Result: new("最新的检索结果")}}},
	}}
	const window = 32000
	state := judgeState(CaseSnapshot{Context: CaseContext{Messages: messages}, Question: "怎么退款"}, result, window)
	kept := state["context"].([]ContextMessage)
	budget := agentruntime.ContextWindowTokens(agentruntime.ModelConfig{ContextWindow: window}) * toolResultWindowPercent / 100
	used := 0
	for _, message := range kept {
		used += agentruntime.EstimateTextTokens(message.Body)
	}
	if len(kept) == 0 || len(kept) == len(messages) || kept[len(kept)-1].Body != "最新前文" || used > budget {
		t.Fatalf("kept %d of %d messages, used %d of %d tokens", len(kept), len(messages), used, budget)
	}
	if tools := fmt.Sprint(state["toolResults"]); !strings.Contains(tools, "最新的检索结果") || !strings.Contains(tools, "较早的检索结果") {
		t.Fatalf("tool results = %v", tools)
	}
}
