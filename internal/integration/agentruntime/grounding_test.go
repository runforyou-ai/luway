package agentruntime

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
)

const groundedAnswer = "退货期限是 7 天"

// groundingModel 按调用顺序执行预设脚本，并记录每次调用可见的工具与最后一条用户消息。
type groundingModel struct {
	mu       sync.Mutex
	script   []func([]*schema.AgenticMessage) *schema.AgenticMessage
	calls    int
	tools    [][]string
	lastUser []string
	inputs   [][]*schema.AgenticMessage
}

// Generate 返回当前调用序号对应的脚本输出。
func (m *groundingModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0)
	for _, info := range model.GetCommonOptions(&model.Options{}, opts...).Tools {
		names = append(names, info.Name)
	}
	m.tools = append(m.tools, names)
	last := ""
	for _, message := range input {
		if messageKind(message) == "user" {
			last = messageText(message)
		}
	}
	m.lastUser = append(m.lastUser, last)
	m.inputs = append(m.inputs, input)
	output := m.script[min(m.calls, len(m.script)-1)](input)
	m.calls++
	return output, nil
}

// Stream 以单个分片返回当前调用的输出。
func (m *groundingModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// reply 返回固定模型输出的脚本步骤。
func reply(text string, calls ...*schema.FunctionToolCall) func([]*schema.AgenticMessage) *schema.AgenticMessage {
	return func([]*schema.AgenticMessage) *schema.AgenticMessage { return assistantReply(text, calls...) }
}

// matchedKnowledge 返回一条命中记录的知识检索实现。
func matchedKnowledge(content string) KnowledgeSearch {
	return func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
		return knowledgeretrieval.Result{Records: []knowledgeretrieval.Record{{SegmentID: "s1", Content: content, Matched: true}}}, nil
	}
}

// runGrounded 以严格依据策略执行一次客服运行。
func runGrounded(t *testing.T, chatModel *groundingModel, feed *testInputFeed, request RunRequest) RunResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request.RunID = "grounding-run"
	request.Assignment.AgentName, request.Assignment.Scene, request.Assignment.Grounding = "客服", SceneCustomer, GroundingStrict
	if request.MaxIterations == 0 {
		request.MaxIterations = 6
	}
	request.MaxTurns = 4
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
	result, err := runtime.Run(ctx, request, feed)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestGroundingGate 验证严格依据策略下正文的放行、纠正与转人工。
func TestGroundingGate(t *testing.T) {
	search := terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)
	ask := terminalCall("ask", askCustomerToolName, `{"purpose":"clarify","message":"请提供订单号"}`)
	emptyKnowledge := func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
		return knowledgeretrieval.Result{Records: []knowledgeretrieval.Record{{SegmentID: "s1", Content: "周边内容"}}}, nil
	}
	failingKnowledge := func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
		return knowledgeretrieval.Result{}, errors.New("知识库暂不可用")
	}
	for _, scenario := range []struct {
		name           string
		knowledge      KnowledgeSearch
		history        bool
		script         []func([]*schema.AgenticMessage) *schema.AgenticMessage
		wantKind       domain.AgentRunOutcome
		wantReason     domain.AgentHandoffReason
		wantContent    string
		wantCalls      int
		wantCorrection string
	}{
		{name: "有依据的正文放行", knowledge: matchedKnowledge("退货期限为 7 天"),
			script:   []func([]*schema.AgenticMessage) *schema.AgenticMessage{reply("", search), reply(groundedAnswer)},
			wantKind: "", wantContent: groundedAnswer, wantCalls: 2},
		{name: "空知识结果纠正后仍无依据转人工", knowledge: emptyKnowledge,
			script:   []func([]*schema.AgenticMessage) *schema.AgenticMessage{reply("", search), reply(groundedAnswer), reply(groundedAnswer)},
			wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonInsufficientEvidence, wantCalls: 3,
			wantCorrection: "search_knowledge"},
		{name: "知识检索报错不计入依据且追问不需要依据", knowledge: failingKnowledge,
			script:   []func([]*schema.AgenticMessage) *schema.AgenticMessage{reply("", search), reply(groundedAnswer), reply("", ask)},
			wantKind: domain.AgentRunOutcomeAskCustomer, wantContent: "请提供订单号", wantCalls: 3, wantCorrection: "search_knowledge"},
		{name: "历史查询不计入依据", knowledge: matchedKnowledge("退货期限为 7 天"), history: true,
			script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
				reply("", terminalCall("h1", "search_customer_history", `{"query":"退货"}`)), reply(groundedAnswer), reply(groundedAnswer),
			},
			wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonInsufficientEvidence, wantCalls: 3,
			wantCorrection: "search_knowledge"},
		{name: "纠正后查证放行", knowledge: matchedKnowledge("退货期限为 7 天"),
			script:   []func([]*schema.AgenticMessage) *schema.AgenticMessage{reply(groundedAnswer), reply("", search), reply(groundedAnswer)},
			wantKind: "", wantContent: groundedAnswer, wantCalls: 3, wantCorrection: "search_knowledge"},
		{name: "直接追问不需要依据",
			script:   []func([]*schema.AgenticMessage) *schema.AgenticMessage{reply("", ask)},
			wantKind: domain.AgentRunOutcomeAskCustomer, wantContent: "请提供订单号", wantCalls: 1},
		{name: "没有知识库时纠正提示不含检索工具",
			script:   []func([]*schema.AgenticMessage) *schema.AgenticMessage{reply(groundedAnswer), reply(groundedAnswer)},
			wantKind: domain.AgentRunOutcomeHandoff, wantReason: domain.AgentHandoffReasonInsufficientEvidence, wantCalls: 2,
			wantCorrection: "ask_customer"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			feed := &testInputFeed{}
			feed.appendUser("退货期限是几天")
			chatModel := &groundingModel{script: scenario.script}
			request := RunRequest{KnowledgeSearch: scenario.knowledge}
			if scenario.history {
				request.CustomerHistorySearch = func(context.Context, string) (CustomerHistoryResult, error) {
					return CustomerHistoryResult{Message: "暂未开放"}, nil
				}
			}
			result := runGrounded(t, chatModel, feed, request)
			if result.Decision.Kind != scenario.wantKind || result.Decision.Reason != scenario.wantReason ||
				result.Content != scenario.wantContent || result.EndSeq != 1 {
				t.Fatalf("result = %+v", result)
			}
			if chatModel.calls != scenario.wantCalls {
				t.Fatalf("model calls = %d", chatModel.calls)
			}
			// 最后一次调用的上下文中纠正提示至多出现一次。
			corrections, correction := 0, ""
			for _, message := range chatModel.inputs[len(chatModel.inputs)-1] {
				if text := messageText(message); messageKind(message) == "user" && strings.HasPrefix(text, "【系统提示】上面的回答没有取得依据") {
					corrections, correction = corrections+1, text
				}
			}
			if scenario.wantCorrection == "" {
				if corrections != 0 {
					t.Fatalf("unexpected correction: %q", correction)
				}
				return
			}
			if corrections != 1 || !strings.Contains(correction, scenario.wantCorrection) ||
				(scenario.knowledge == nil && strings.Contains(correction, KnowledgeToolName)) {
				t.Fatalf("corrections = %d, correction = %q", corrections, correction)
			}
		})
	}
}

// TestGroundingCorrectionKeepsBudget 验证纠正重新执行沿用剩余迭代预算，预算末端只保留终止工具，仍无依据时按预算耗尽转人工。
func TestGroundingCorrectionKeepsBudget(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("退货期限是几天")
	chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		reply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)), reply(groundedAnswer), reply(groundedAnswer),
	}}
	result := runGrounded(t, chatModel, feed, RunRequest{
		MaxIterations: 3,
		KnowledgeSearch: func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
			return knowledgeretrieval.Result{}, nil
		},
	})
	if result.Decision.Kind != domain.AgentRunOutcomeHandoff || result.Decision.Reason != domain.AgentHandoffReasonBudgetExhausted {
		t.Fatalf("result = %+v", result)
	}
	if chatModel.calls != 3 || !slices.Equal(chatModel.tools[2], []string{askCustomerToolName, handoffToolName, resolveToolName}) {
		t.Fatalf("calls = %d, tools = %v", chatModel.calls, chatModel.tools)
	}
	if !strings.Contains(chatModel.lastUser[2], "工具调用次数已达本轮上限") {
		t.Fatalf("final planning prompt = %q", chatModel.lastUser[2])
	}
}

// TestGroundingBudgetEndSkipsCorrection 验证预算末端首次出现无依据正文时直接按预算耗尽转人工，纠正额度保持未用。
func TestGroundingBudgetEndSkipsCorrection(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("退货期限是几天")
	chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		reply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)), reply(groundedAnswer),
	}}
	result := runGrounded(t, chatModel, feed, RunRequest{
		MaxIterations: 2,
		KnowledgeSearch: func(context.Context, knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
			return knowledgeretrieval.Result{}, nil
		},
	})
	if result.Decision.Reason != domain.AgentHandoffReasonBudgetExhausted || chatModel.calls != 2 {
		t.Fatalf("result = %+v, calls = %d", result, chatModel.calls)
	}
}

// TestGroundingResetsOnNewInput 验证认领新输入后，新问题的正文只按边界之后的依据放行。
func TestGroundingResetsOnNewInput(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("退货期限是几天")
	var once sync.Once
	chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		func([]*schema.AgenticMessage) *schema.AgenticMessage {
			once.Do(func() { feed.appendUser("那换货呢") })
			return assistantReply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`))
		},
		reply(groundedAnswer),
	}}
	result := runGrounded(t, chatModel, feed, RunRequest{KnowledgeSearch: matchedKnowledge("退货期限为 7 天")})
	if result.Decision.Kind != domain.AgentRunOutcomeHandoff || result.Decision.Reason != domain.AgentHandoffReasonInsufficientEvidence ||
		result.EndSeq != 2 {
		t.Fatalf("result = %+v", result)
	}
}

// TestGroundingFollowsOffloadedEvidence 验证知识结果被转存后不计入依据，模型读回转存内容后恢复有效。
func TestGroundingFollowsOffloadedEvidence(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("退货期限是几天")
	chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		reply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)),
		reply(groundedAnswer),
		reply("", terminalCall("r1", offloadedResultToolName, `{"file_path":"/trunc/k1"}`)),
		reply(groundedAnswer),
	}}
	result := runGrounded(t, chatModel, feed, RunRequest{
		Assignment:      Assignment{Model: AssignmentModel{ContextWindow: 13000}},
		KnowledgeSearch: matchedKnowledge("退货期限为 7 天。" + strings.Repeat("x", 6000)),
	})
	if result.Decision.Kind != "" || result.Content != groundedAnswer || chatModel.calls != 4 {
		t.Fatalf("result = %+v, calls = %d, last user = %v", result, chatModel.calls, chatModel.lastUser)
	}
	// 第二次调用看到的知识结果是转存预览，不是完整检索结果。
	for _, message := range chatModel.inputs[1] {
		if result := toolResult(message); result != nil && result.CallID == "k1" && !strings.Contains(messageText(message), "/trunc/k1") {
			t.Fatalf("knowledge result was not offloaded: %q", messageText(message)[:200])
		}
	}
}

// TestGroundingGateVisibility 验证依据按模型可见的上下文判定：读回须取回完整原文，失败说明、越界读取与截断预览都不计入。
func TestGroundingGateVisibility(t *testing.T) {
	original := `{"records":[{"content":"退货期限为 7 天","matched":true}]}`
	messages := func(readResult string) []*schema.AgenticMessage {
		return []*schema.AgenticMessage{
			schema.UserAgenticMessage("退货期限是几天"),
			assistantReply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)),
			toolReply("k1", "工具结果已清理，可读取 /clear/k1"),
			assistantReply("", terminalCall("r1", offloadedResultToolName, `{"file_path":"/clear/k1"}`)),
			toolReply("r1", readResult),
			assistantReply(groundedAnswer),
		}
	}
	for _, scenario := range []struct {
		name     string
		matched  bool
		before   bool
		read     string
		grounded bool
	}{
		{name: "读回完整原文后恢复", matched: true, read: "     1\t" + original, grounded: true},
		{name: "来源检索没有命中", read: "     1\t" + original},
		{name: "来源检索在边界之前", matched: true, before: true, read: "     1\t" + original},
		{name: "转存文件不存在", matched: true, read: "No content found at path: /clear/k1"},
		{name: "读取超出文件范围", matched: true, read: "No content read from /clear/k1: the file is empty, or offset 5 is past its last line"},
		{name: "只读回截断预览", matched: true, read: "     1\t" + original[:20] + "…已转存至 /trunc/k1"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			marked := ""
			gate := newGroundingGate(map[string]evidenceJudge{KnowledgeToolName: knowledgeEvidence}, func(callID string) { marked = callID })
			if scenario.matched {
				gate.valid["k1"] = original
			}
			if scenario.before {
				gate.resetBoundary()
			}
			state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: messages(scenario.read)}
			if _, _, err := gate.AfterModelRewriteState(context.Background(), state, nil); err != nil {
				t.Fatal(err)
			}
			// 完整读回时标记的是来源调用而不是读回调用。
			if gate.verdict() != scenario.grounded || (marked == "k1") != scenario.grounded {
				t.Fatalf("grounded = %v, marked = %q", gate.verdict(), marked)
			}
		})
	}
}

// TestGroundingQueryEvidenceSource 验证业务查询工具按原始结果登记依据，空列表同样构成依据，模型可见文本与原始结果一致时才计入依据并通知过程记录。
func TestGroundingQueryEvidenceSource(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		output   string
		visible  string
		evidence bool
		grounded bool
	}{
		{name: "完整可见", output: "订单 1001 已发货", visible: "订单 1001 已发货", evidence: true, grounded: true},
		{name: "空列表", output: `{"orders":[]}`, visible: `{"orders":[]}`, evidence: true, grounded: true},
		{name: "截断预览不计入", output: "订单 1001 已发货", visible: "订单 10…（结果已转存至 /trunc/o1）"},
		{name: "空白结果未通过判定", output: " ", visible: " "},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			marked := ""
			gate := newGroundingGate(map[string]evidenceJudge{"lookup_order": queryEvidence}, func(callID string) { marked = callID })
			endpoint, err := gate.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
				return scenario.output, nil
			}, &adk.ToolContext{Name: "lookup_order", CallID: "o1"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := endpoint(context.Background(), `{"orderId":"1001"}`); err != nil {
				t.Fatal(err)
			}
			state := &adk.TypedChatModelAgentState[*schema.AgenticMessage]{Messages: []*schema.AgenticMessage{
				schema.UserAgenticMessage("我的订单到哪了"),
				assistantReply("", terminalCall("o1", "lookup_order", `{"orderId":"1001"}`)),
				toolReply("o1", scenario.visible),
				assistantReply("您的订单已发货"),
			}}
			if _, _, err := gate.AfterModelRewriteState(context.Background(), state, nil); err != nil {
				t.Fatal(err)
			}
			if gate.verdict() != scenario.grounded || (marked == "o1") != scenario.evidence {
				t.Fatalf("grounded = %v, marked = %q", gate.verdict(), marked)
			}
		})
	}
}

// TestGroundingMultipleSearches 验证同一轮多次检索中任一次有效且可见即取得依据。
func TestGroundingMultipleSearches(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		results map[string]error // 查询词到检索错误，nil 表示命中，errNoMatch 表示没有命中。
	}{
		{name: "先未命中后命中", results: map[string]error{"A": errNoMatch, "B": nil}},
		{name: "先命中后报错", results: map[string]error{"A": nil, "B": errors.New("知识库暂不可用")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			feed := &testInputFeed{}
			feed.appendUser("退货期限是几天")
			chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
				reply("", terminalCall("k1", KnowledgeToolName, `{"queries":["A"]}`)),
				reply("", terminalCall("k2", KnowledgeToolName, `{"queries":["B"]}`)),
				reply(groundedAnswer),
			}}
			result := runGrounded(t, chatModel, feed, RunRequest{
				KnowledgeSearch: func(_ context.Context, request knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
					switch err := scenario.results[request.Queries[0]]; {
					case errors.Is(err, errNoMatch):
						return knowledgeretrieval.Result{}, nil
					case err != nil:
						return knowledgeretrieval.Result{}, err
					}
					return knowledgeretrieval.Result{Records: []knowledgeretrieval.Record{{SegmentID: "s1", Content: "退货期限为 7 天", Matched: true}}}, nil
				},
			})
			if result.Decision.Kind != "" || result.Content != groundedAnswer || chatModel.calls != 3 {
				t.Fatalf("result = %+v, calls = %d", result, chatModel.calls)
			}
		})
	}
}

var errNoMatch = errors.New("no match")

// TestGroundingBudgetEndInvalidTerminalCall 验证预算末端的无效终止调用直接按预算耗尽转人工。
func TestGroundingBudgetEndInvalidTerminalCall(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("退货期限是几天")
	chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		reply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)),
		reply("", terminalCall("ask", askCustomerToolName, `{}`)),
	}}
	result := runGrounded(t, chatModel, feed, RunRequest{MaxIterations: 2, KnowledgeSearch: matchedKnowledge("退货期限为 7 天")})
	if result.Decision.Kind != domain.AgentRunOutcomeHandoff || result.Decision.Reason != domain.AgentHandoffReasonBudgetExhausted ||
		chatModel.calls != 2 {
		t.Fatalf("result = %+v, calls = %d", result, chatModel.calls)
	}
}

// TestGroundingCorrectionStaysOutOfHistory 验证被拦下的回答与纠正提示只进入纠正重新执行的输入，后续轮次看不到。
func TestGroundingCorrectionStaysOutOfHistory(t *testing.T) {
	const rejected = "未经查证的回答"
	feed := &testInputFeed{}
	feed.appendUser("退货期限是几天")
	var once sync.Once
	chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		reply(rejected),
		func([]*schema.AgenticMessage) *schema.AgenticMessage {
			once.Do(func() { feed.appendUser("订单号是 1001") })
			return assistantReply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`))
		},
		reply("", terminalCall("ask", askCustomerToolName, `{"purpose":"confirm","message":"请确认订单号"}`)),
	}}
	result := runGrounded(t, chatModel, feed, RunRequest{KnowledgeSearch: matchedKnowledge("退货期限为 7 天")})
	if result.Decision.Kind != domain.AgentRunOutcomeAskCustomer || result.EndSeq != 2 || chatModel.calls != 3 {
		t.Fatalf("result = %+v, calls = %d", result, chatModel.calls)
	}
	// 统计指定调用输入中被拦下的回答与纠正提示的出现次数。
	count := func(call int) (int, int) {
		answers, corrections := 0, 0
		for _, message := range chatModel.inputs[call] {
			text := messageText(message)
			if message.Role == schema.AgenticRoleTypeAssistant && text == rejected {
				answers++
			}
			if strings.HasPrefix(text, "【系统提示】上面的回答没有取得依据") {
				corrections++
			}
		}
		return answers, corrections
	}
	if answers, corrections := count(1); answers != 1 || corrections != 1 {
		t.Fatalf("correction input: answers = %d, corrections = %d", answers, corrections)
	}
	if answers, corrections := count(2); answers != 0 || corrections != 0 {
		t.Fatalf("next turn input: answers = %d, corrections = %d", answers, corrections)
	}
}

// TestUnsentOutputStaysOutOfHistory 验证客服场景中因新输入作废的正文与追问不进入下一轮的历史。
func TestUnsentOutputStaysOutOfHistory(t *testing.T) {
	const unsent = "退货期限是 30 天"
	confirm := terminalCall("ask-2", askCustomerToolName, `{"purpose":"confirm","message":"请确认订单 1001"}`)
	for _, scenario := range []struct {
		name  string
		first *schema.AgenticMessage
	}{
		{name: "无依据正文", first: assistantReply(unsent)},
		{name: "追问", first: assistantReply("", terminalCall("ask", askCustomerToolName, `{"purpose":"clarify","message":"请提供订单号"}`))},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			feed := &testInputFeed{}
			feed.appendUser("退货期限是几天")
			var once sync.Once
			chatModel := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
				func([]*schema.AgenticMessage) *schema.AgenticMessage {
					once.Do(func() { feed.appendUser("订单号是 1001") })
					return scenario.first
				},
				reply("", confirm),
			}}
			result := runGrounded(t, chatModel, feed, RunRequest{KnowledgeSearch: matchedKnowledge("退货期限为 7 天")})
			if result.Decision.Kind != domain.AgentRunOutcomeAskCustomer || result.Content != "请确认订单 1001" || result.EndSeq != 2 {
				t.Fatalf("result = %+v", result)
			}
			for _, message := range chatModel.inputs[len(chatModel.inputs)-1] {
				for _, call := range toolCalls(message) {
					if call.CallID == "ask" {
						t.Fatal("unsent ask_customer call remains in history")
					}
				}
				if message.Role == schema.AgenticRoleTypeAssistant && messageText(message) == unsent {
					t.Fatal("unsent reply remains in history")
				}
			}
		})
	}
}
