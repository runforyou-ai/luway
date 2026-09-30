package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// TestRunFollowUpsDoNotConsumeIterationBudget 验证连续补充使用独立迭代预算并保留历史中间正文。
func TestRunFollowUpsDoNotConsumeIterationBudget(t *testing.T) {
	for _, maxTurns := range []int{0, 3} {
		t.Run(fmt.Sprintf("max-turns-%d", maxTurns), func(t *testing.T) {
			feed := &testInputFeed{}
			feed.appendUser("开始")
			calls := 0
			chatModel := &processChatModel{generate: func(_ context.Context, messages []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
				calls++
				var assistantCount int
				for _, message := range messages {
					if message.Role == schema.AgenticRoleTypeAssistant {
						assistantCount++
					}
				}
				if assistantCount != calls-1 {
					return nil, fmt.Errorf("previous assistant messages = %d, want %d", assistantCount, calls-1)
				}
				if calls < 12 {
					feed.appendUser("继续")
				}
				return assistantReply(fmt.Sprintf("回答 %d", calls)), nil
			}}
			runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := runtime.Run(ctx, RunRequest{Assignment: Assignment{AgentName: "test"}, MaxIterations: 1, MaxTurns: maxTurns}, feed)
			if maxTurns > 0 {
				if err == nil || !strings.Contains(err.Error(), "agent turn limit 3 exceeded") || calls != 3 {
					t.Fatalf("limited calls = %d, error = %v", calls, err)
				}
			} else if err != nil || calls != 12 || result.EndSeq != 12 || result.Content != "回答 12" {
				t.Fatalf("result = %#v, calls = %d, error = %v", result, calls, err)
			}
		})
	}
}

// TestRunRetainsToolsAcrossRepeatedPreemption 验证工具执行期间到达的新输入在工具完成后抢占，工具结果保留，各轮输入使用独立迭代预算。
func TestRunRetainsToolsAcrossRepeatedPreemption(t *testing.T) {
	runtime, err := New()
	if err != nil {
		t.Fatal(err)
	}
	feed := &testInputFeed{}
	feed.appendUser("开始计算")
	// 计算器在执行期间追加一条新输入，抢占只能落在本轮工具调用完成之后。
	calculator, err := toolutils.InferTool("calculator", "Add two numbers.", func(ctx context.Context, input calculatorInput) (calculatorOutput, error) {
		feed.appendUser("再补充一项")
		return calculate(ctx, input)
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.tools = []tool.BaseTool{calculator}
	calls := 0
	chatModel := &processChatModel{generate: func(_ context.Context, messages []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		calls++
		var results int
		for _, message := range messages {
			if reply := toolResult(message); reply != nil {
				results++
				if reply.CallID != fmt.Sprintf("call-%d", results) || messageText(message) != `{"result":3}` {
					return nil, fmt.Errorf("unexpected tool result: %#v", message)
				}
			}
		}
		if results != calls-1 || messageKind(messages[len(messages)-1]) != "user" {
			return nil, fmt.Errorf("tool results = %d for model call %d, last kind = %s", results, calls, messageKind(messages[len(messages)-1]))
		}
		if calls == 4 {
			return assistantReply("完成"), nil
		}
		return assistantReply("正在计算", &schema.FunctionToolCall{CallID: fmt.Sprintf("call-%d", calls), Name: "calculator", Arguments: `{"operation":"add","left":1,"right":2,"delayMilliseconds":200}`}), nil
	}}
	runtime.newModel = func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := runtime.Run(ctx, RunRequest{Assignment: Assignment{AgentName: "test"}, MaxIterations: 2}, feed)
	if err != nil || result.Content != "完成" || result.EndSeq != 4 || calls != 4 {
		t.Fatalf("result = %#v, calls = %d, error = %v", result, calls, err)
	}
}

// TestRunIterationLimitStillStopsToolLoop 验证单轮持续调用工具仍受迭代上限约束。
func TestRunIterationLimitStillStopsToolLoop(t *testing.T) {
	runtime, err := New()
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	chatModel := &processChatModel{generate: func(context.Context, []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		calls++
		return assistantReply("", &schema.FunctionToolCall{CallID: fmt.Sprintf("call-%d", calls), Name: "calculator", Arguments: `{"operation":"add","left":1,"right":2}`}), nil
	}}
	runtime.newModel = func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }
	feed := &testInputFeed{}
	feed.appendUser("计算")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = runtime.Run(ctx, RunRequest{Assignment: Assignment{AgentName: "test"}, MaxIterations: 2, MaxTurns: 20}, feed)
	if err == nil || calls > 2 || calls == 0 || ctx.Err() != nil {
		t.Fatalf("calls = %d, error = %v, context error = %v", calls, err, ctx.Err())
	}
}
