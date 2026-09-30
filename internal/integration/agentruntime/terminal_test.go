package agentruntime

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// scriptedTerminalModel 按调用顺序返回预设输出，并记录每次调用可见的工具与工具结果。
type scriptedTerminalModel struct {
	mu      sync.Mutex
	outputs []func() *schema.AgenticMessage
	calls   int
	tools   [][]string
	results []string
}

// Generate 返回当前调用序号对应的输出。
func (m *scriptedTerminalModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0)
	for _, info := range model.GetCommonOptions(&model.Options{}, opts...).Tools {
		names = append(names, info.Name)
	}
	m.tools = append(m.tools, names)
	for _, message := range input {
		if result := toolResult(message); result != nil {
			m.results = append(m.results, messageText(message))
		}
	}
	output := m.outputs[min(m.calls, len(m.outputs)-1)]
	m.calls++
	return output(), nil
}

// Stream 以单个分片返回当前调用的输出。
func (m *scriptedTerminalModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// terminalCall 构造一次工具调用。
func terminalCall(callID, name, arguments string) *schema.FunctionToolCall {
	return &schema.FunctionToolCall{CallID: callID, Name: name, Arguments: arguments}
}

// TestCustomerTerminalDecisions 验证客服场景的终止工具、同批校验、参数纠正与额度用尽后的转人工。
func TestCustomerTerminalDecisions(t *testing.T) {
	askArgs := `{"purpose":"clarify","message":"请提供订单号"}`
	handoffArgs := `{"reason":"needs_human_judgment","category":"退款","note":"客户要求退款"}`
	for _, scenario := range []struct {
		name         string
		outputs      []func() *schema.AgenticMessage
		lateInput    bool
		wantKind     domain.AgentRunOutcome
		wantReason   domain.AgentHandoffReason
		wantCategory string
		wantContent  string
		wantCalls    int
		wantHistory  int
	}{
		{name: "追问", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask", askCustomerToolName, askArgs))
			},
		}, wantKind: domain.AgentRunOutcomeAskCustomer, wantContent: "请提供订单号", wantCalls: 1},
		{name: "请客户确认问题已解决", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("confirm", askCustomerToolName, `{"purpose":"confirm_resolution","message":"请问问题解决了吗？"}`))
			},
		}, wantKind: domain.AgentRunOutcomeAskCustomer, wantContent: "请问问题解决了吗？", wantCalls: 1},
		{name: "客户确认后结束服务", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("resolve", resolveToolName, `{"message":"不客气，祝您生活愉快"}`))
			},
		}, wantKind: domain.AgentRunOutcomeResolve, wantContent: "不客气，祝您生活愉快", wantCalls: 1},
		{name: "结束语为空后纠正", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("resolve", resolveToolName, `{"message":" "}`))
			},
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("resolve-2", resolveToolName, `{"message":"感谢您的咨询"}`))
			},
		}, wantKind: domain.AgentRunOutcomeResolve, wantContent: "感谢您的咨询", wantCalls: 2},
		{name: "结束服务与追问同批后纠正", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("resolve", resolveToolName, `{"message":"再见"}`), terminalCall("ask", askCustomerToolName, askArgs))
			},
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask-2", askCustomerToolName, askArgs))
			},
		}, wantKind: domain.AgentRunOutcomeAskCustomer, wantContent: "请提供订单号", wantCalls: 2},
		{name: "转人工", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("handoff", handoffToolName, handoffArgs))
			},
		}, wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonNeedsHumanJudgment, wantCategory: "category-refund", wantCalls: 1},
		{name: "未列出的咨询分类后纠正", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("handoff", handoffToolName, `{"reason":"knowledge_gap","category":"物流","note":"查不到物流政策"}`))
			},
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("handoff-2", handoffToolName, `{"reason":"knowledge_gap","note":"查不到物流政策"}`))
			},
		}, wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonKnowledgeGap, wantCalls: 2},
		{name: "咨询分类为 null 视为未选择", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("handoff", handoffToolName, `{"reason":"complaint","category":null,"note":"客户投诉"}`))
			},
		}, wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonComplaint, wantCalls: 1},
		{name: "转人工后到达新输入仍不降级", lateInput: true, outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("handoff", handoffToolName, handoffArgs))
			},
		}, wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonNeedsHumanJudgment, wantCategory: "category-refund", wantCalls: 1},
		{name: "追问与转人工同批后纠正", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask", askCustomerToolName, askArgs), terminalCall("handoff", handoffToolName, handoffArgs))
			},
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask-2", askCustomerToolName, askArgs))
			},
		}, wantKind: domain.AgentRunOutcomeAskCustomer, wantContent: "请提供订单号", wantCalls: 2, wantHistory: 0},
		{name: "终止工具与其他工具同批", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("history", "search_customer_history", `{"query":"订单"}`), terminalCall("ask", askCustomerToolName, askArgs))
			},
			func() *schema.AgenticMessage { return assistantReply("请告诉我订单号") },
		}, wantKind: "", wantContent: "请告诉我订单号", wantCalls: 2, wantHistory: 0},
		{name: "无效参数后纠正", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask", askCustomerToolName, `{"purpose":"chat","message":""}`))
			},
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("handoff", handoffToolName, `{"reason":"customer_requested","note":"客户要求真人"}`))
			},
		}, wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonCustomerRequested, wantContent: "", wantCalls: 2},
		{name: "纠正额度用尽后转人工", outputs: []func() *schema.AgenticMessage{
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask", askCustomerToolName, askArgs), terminalCall("handoff", handoffToolName, handoffArgs))
			},
			func() *schema.AgenticMessage {
				return assistantReply("", terminalCall("ask-2", askCustomerToolName, `{}`))
			},
		}, wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonInvalidOutput, wantContent: "", wantCalls: 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			feed := &testInputFeed{}
			feed.appendUser("我要退款")
			chatModel := &scriptedTerminalModel{outputs: scenario.outputs}
			if scenario.lateInput {
				first := scenario.outputs[0]
				chatModel.outputs = []func() *schema.AgenticMessage{func() *schema.AgenticMessage {
					feed.appendUser("还在吗")
					return first()
				}}
			}
			historyCalls := 0
			runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
			result, err := runtime.Run(ctx, RunRequest{
				RunID: "terminal-run", MaxIterations: 5, MaxTurns: 3,
				Assignment: Assignment{AgentName: "客服", Scene: SceneCustomer, HandoffCategories: []HandoffCategory{{ID: "category-refund", Name: "退款", Description: "退款、退货"}}},
				CustomerHistorySearch: func(context.Context, string) (CustomerHistoryResult, error) {
					historyCalls++
					return CustomerHistoryResult{}, nil
				},
			}, feed)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision.Kind != scenario.wantKind || result.Decision.Reason != scenario.wantReason || result.Decision.CategoryID != scenario.wantCategory ||
				result.Content != scenario.wantContent || result.EndSeq != 1 {
				t.Fatalf("result = %+v", result)
			}
			if chatModel.calls != scenario.wantCalls || historyCalls != scenario.wantHistory {
				t.Fatalf("model calls = %d, history calls = %d", chatModel.calls, historyCalls)
			}
			if !slices.Contains(chatModel.tools[0], askCustomerToolName) || !slices.Contains(chatModel.tools[0], handoffToolName) {
				t.Fatalf("customer tools = %v", chatModel.tools[0])
			}
			if scenario.wantKind == domain.AgentRunOutcomeHandoff && slices.Contains(domain.AgentHandoffBusinessReasons, scenario.wantReason) &&
				result.Decision.ReasonText == "" {
				t.Fatal("handoff reason text missing")
			}
			if scenario.wantCalls > 1 && (len(chatModel.results) == 0 || !strings.Contains(strings.Join(chatModel.results, "\n"), "error")) {
				t.Fatalf("correction was not returned to model: %v", chatModel.results)
			}
		})
	}
}

// TestInternalSceneHasNoTerminalTools 验证内部场景不注册终止工具，正文直接作为回答。
func TestInternalSceneHasNoTerminalTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	feed := &testInputFeed{}
	feed.appendUser("你好")
	chatModel := &scriptedTerminalModel{outputs: []func() *schema.AgenticMessage{
		func() *schema.AgenticMessage { return assistantReply("你好，有什么需要") },
	}}
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
	result, err := runtime.Run(ctx, RunRequest{RunID: "internal-run", Assignment: Assignment{AgentName: "助理", Scene: SceneAgentChat}}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Outcome() != domain.AgentRunOutcomeReply || result.Content != "你好，有什么需要" ||
		slices.Contains(chatModel.tools[0], askCustomerToolName) || slices.Contains(chatModel.tools[0], handoffToolName) {
		t.Fatalf("result = %+v, tools = %v", result, chatModel.tools[0])
	}
}
