package agentruntime

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// toolHungryChatModel 只要还能看到工具就继续调用，工具被收敛后给出最终结果。
type toolHungryChatModel struct {
	mu           sync.Mutex
	lastTools    []string
	toolsByCall  [][]string
	lastUserText string
}

// Generate 按本次可用工具决定继续调用还是收尾。
func (m *toolHungryChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTools = nil
	for _, info := range model.GetCommonOptions(&model.Options{}, opts...).Tools {
		m.lastTools = append(m.lastTools, info.Name)
	}
	m.toolsByCall = append(m.toolsByCall, m.lastTools)
	for _, message := range input {
		if messageKind(message) == "user" {
			m.lastUserText = messageText(message)
		}
	}
	if slices.Contains(m.lastTools, "calculator") {
		return assistantReply("继续算", &schema.FunctionToolCall{
			CallID: "calculator-call", Name: "calculator", Arguments: `{"operation":"add","left":1,"right":1}`,
		}), nil
	}
	return assistantReply("按已有资料回答"), nil
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *toolHungryChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestFinalIterationAnswersWithoutTools 验证到达迭代上限时移除工具，本轮以最终回答收尾。
func TestFinalIterationAnswersWithoutTools(t *testing.T) {
	calculator, err := newCalculatorTool()
	if err != nil {
		t.Fatal(err)
	}
	chatModel := &toolHungryChatModel{}
	runtime := &EinoRuntime{
		newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil },
		tools:    []tool.BaseTool{calculator},
	}
	feed := &testInputFeed{}
	feed.appendUser("帮我查一下")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{RunID: "budget-run", Assignment: Assignment{AgentName: "test-agent"}, MaxIterations: 3, MaxTurns: 2}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "按已有资料回答" {
		t.Fatalf("result = %#v", result)
	}
	chatModel.mu.Lock()
	defer chatModel.mu.Unlock()
	if len(chatModel.toolsByCall) != 3 || !slices.Contains(chatModel.toolsByCall[1], "calculator") ||
		len(chatModel.toolsByCall[2]) != 0 {
		t.Fatalf("tools by iteration = %v", chatModel.toolsByCall)
	}
	if chatModel.lastUserText != "工具调用次数已达本轮上限，请基于已获得的信息给出最终回答。" {
		t.Fatalf("final iteration hint = %q", chatModel.lastUserText)
	}
}
