package agentruntime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// TestCountContextTokens 验证中文按字计、其余字符按四分之一计。
func TestCountContextTokens(t *testing.T) {
	messages := []*schema.AgenticMessage{
		schema.UserAgenticMessage(strings.Repeat("字", 100)),
		assistantReply(strings.Repeat("a", 40)),
	}
	tokens, err := countContextTokens(context.Background(), messages, nil)
	if err != nil || tokens != 110 {
		t.Fatalf("tokens = %d, err = %v", tokens, err)
	}
}

type repeatedToolChatModel struct {
	mu           sync.Mutex
	calls        int
	earliestSeen string
	latestSeen   string
}

// Generate 反复调用计算器，并记录最后一次规划时最早与最近的工具结果内容。
func (m *repeatedToolChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls > 4 {
		for _, message := range input {
			if toolResult(message) == nil {
				continue
			}
			if m.earliestSeen == "" {
				m.earliestSeen = messageText(message)
			}
			m.latestSeen = messageText(message)
		}
		return assistantReply("算完了"), nil
	}
	// 每轮说明占八千字，第五次规划时上下文超过清理阈值、低于摘要阈值。
	return assistantReply(strings.Repeat("算", 8000), &schema.FunctionToolCall{
		CallID: "calculator-call-" + string(rune('a'+m.calls)), Name: "calculator", Arguments: `{"operation":"add","left":1,"right":1}`,
	}), nil
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *repeatedToolChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestContextClearsOldToolResults 验证上下文超过模型窗口预算后清理较早的工具结果，并保留最近两轮。
func TestContextClearsOldToolResults(t *testing.T) {
	calculator, err := newCalculatorTool()
	if err != nil {
		t.Fatal(err)
	}
	chatModel := &repeatedToolChatModel{}
	runtime := &EinoRuntime{
		newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil },
		tools:    []tool.BaseTool{calculator},
	}
	feed := &testInputFeed{}
	feed.appendUser("一直算")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{
		RunID: "clear-run", Assignment: Assignment{AgentName: "test-agent", Model: AssignmentModel{ContextWindow: 40000}},
		MaxIterations: 6, MaxTurns: 2,
	}, feed)
	if err != nil || result.Content != "算完了" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	chatModel.mu.Lock()
	defer chatModel.mu.Unlock()
	if chatModel.earliestSeen == `{"result":2}` {
		t.Fatalf("earliest tool result was not cleared: %q", chatModel.earliestSeen)
	}
	if chatModel.latestSeen != `{"result":2}` {
		t.Fatalf("latest tool result was cleared: %q", chatModel.latestSeen)
	}
}

// TestTrimClaimedHistory 验证会话历史按模型窗口预算保留较新的消息。
func TestTrimClaimedHistory(t *testing.T) {
	messages := make([]Message, 0, 4)
	for i := range 4 {
		messages = append(messages, Message{ID: string(rune('a' + i)), Content: strings.Repeat("字", 100)})
	}
	// 窗口 600 Token 的一半为预算，只容纳最近三条各一百字的消息。
	kept := trimClaimedHistory(context.Background(), messages, 600)
	if len(kept) != 3 || kept[0].ID != "b" {
		t.Fatalf("kept = %+v", kept)
	}
	if all := trimClaimedHistory(context.Background(), messages, 4000); len(all) != 4 {
		t.Fatalf("kept within budget = %d", len(all))
	}
	// 最新一条自身超出预算时仍然保留，否则本轮输入会被裁空。
	if single := trimClaimedHistory(context.Background(), messages, 100); len(single) != 1 || single[0].ID != "d" {
		t.Fatalf("kept oversized latest = %+v", single)
	}
	if zero := trimClaimedHistory(context.Background(), messages, 0); len(zero) != 1 || zero[0].ID != "d" {
		t.Fatalf("kept with zero budget = %+v", zero)
	}
}

// TestOffloadThresholdFollowsContextWindow 验证转存阈值随模型窗口变化并有下限。
func TestOffloadThresholdFollowsContextWindow(t *testing.T) {
	if bytes := offloadThresholdBytes(128000); bytes != 38400 {
		t.Fatalf("large window offload bytes = %d", bytes)
	}
	if bytes := offloadThresholdBytes(4000); bytes != minToolResultOffloadBytes {
		t.Fatalf("small window offload bytes = %d", bytes)
	}
	handlers, err := newContextReductionHandlers(context.Background(), ContextWindowTokens(ModelConfig{ContextWindow: 128000}), nil, nil)
	if err != nil || len(handlers) != 2 {
		t.Fatalf("handlers = %d, err = %v", len(handlers), err)
	}
}
