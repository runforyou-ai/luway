package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type customerHistoryChatModel struct {
	*processChatModel
	tools []*schema.ToolInfo
}

// Generate 记录调用参数中的工具并执行当前测试步骤。
func (m *customerHistoryChatModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.tools = model.GetCommonOptions(nil, opts...).Tools
	return m.processChatModel.Generate(ctx, input, opts...)
}

// Stream 记录调用参数中的工具并以单个分片返回当前测试步骤的模型输出。
func (m *customerHistoryChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// TestCustomerHistoryTool 验证模型收到历史检索结果及工具的运行范围隔离。
func TestCustomerHistoryTool(t *testing.T) {
	runtime, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			modelCalls, searchCalls := 0, 0
			chatModel := &customerHistoryChatModel{}
			chatModel.processChatModel = &processChatModel{generate: func(_ context.Context, messages []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
				modelCalls++
				if enabled && modelCalls == 1 {
					return assistantReply("", &schema.FunctionToolCall{
						CallID: "history", Name: "search_customer_history", Arguments: `{"query":"上次退款的处理结果"}`,
					}), nil
				}
				if enabled {
					for _, message := range messages {
						if reply := toolResult(message); reply != nil && reply.CallID == "history" {
							var result CustomerHistoryResult
							if err := json.Unmarshal([]byte(messageText(message)), &result); err != nil {
								return nil, err
							}
							if len(result.Sessions) != 1 || result.Sessions[0].Summary != "退款已原路退回" || result.Sessions[0].Messages[0].Body != "订单 A-1 申请退款" {
								return nil, fmt.Errorf("unexpected history result: %+v", result)
							}
							return assistantReply("请补充上次退款的信息"), nil
						}
					}
					return nil, errors.New("history result not delivered to model")
				}
				return assistantReply("单聊回答"), nil
			}}
			runtime.newModel = func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }
			request := RunRequest{RunID: "history-test", Assignment: Assignment{AgentName: "客服助手"}}
			if enabled {
				request.CustomerHistorySearch = func(_ context.Context, query string) (CustomerHistoryResult, error) {
					searchCalls++
					if query != "上次退款的处理结果" {
						return CustomerHistoryResult{}, fmt.Errorf("unexpected query: %q", query)
					}
					return CustomerHistoryResult{Sessions: []CustomerHistorySession{{
						CustomerHistorySummary: CustomerHistorySummary{Summary: "退款已原路退回"},
						Messages:               []CustomerHistoryMessage{{Sender: "customer", Body: "订单 A-1 申请退款"}},
					}}}, nil
				}
			}
			feed := &testInputFeed{}
			feed.appendUser("请继续处理退款")
			result, err := runtime.Run(context.Background(), request, feed)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, info := range chatModel.tools {
				found = found || info.Name == "search_customer_history"
			}
			if found != enabled {
				t.Fatalf("history tool registered=%t, expected=%t", found, enabled)
			}
			if enabled && (searchCalls != 1 || result.Content != "请补充上次退款的信息") {
				t.Fatalf("search calls=%d, result=%+v", searchCalls, result)
			}
		})
	}
}
