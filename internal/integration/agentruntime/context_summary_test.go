package agentruntime

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/integration/knowledgeretrieval"
)

// summaryModel 对不带工具的摘要调用返回带分析段的摘要并记录其输入，其余调用交给主模型。
type summaryModel struct {
	model.AgenticModel
	mu        sync.Mutex
	summaries [][]*schema.AgenticMessage
}

// Generate 按是否携带工具区分摘要调用与主循环规划。
func (m *summaryModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	if model.GetCommonOptions(nil, opts...).Tools != nil {
		return m.AgenticModel.Generate(ctx, input, opts...)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.summaries = append(m.summaries, input)
	summary := assistantReply("<analysis>逐条分析</analysis>\n<summary>用户先让计算，已给出长篇说明</summary>")
	summary.ResponseMeta = &schema.AgenticResponseMeta{TokenUsage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}}
	return summary, nil
}

// Stream 以单个分片返回当前测试步骤的模型输出。
func (m *summaryModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return singleChunkStream(m.Generate(ctx, input, opts...))
}

// conversation 返回模型输入中除系统指令外各消息的文本。
func conversation(input []*schema.AgenticMessage) []string {
	texts := make([]string, 0, len(input))
	for _, message := range input {
		if message.Role != schema.AgenticRoleTypeSystem {
			texts = append(texts, messageText(message))
		}
	}
	return texts
}

// TestContextSummaryKeepsLatestInput 验证摘要只压缩最新输入之前的消息，后续轮次沿用摘要，摘要调用的用量计入结果。
func TestContextSummaryKeepsLatestInput(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("开始计算")
	long := strings.Repeat("长", 3000)
	var mu sync.Mutex
	var inputs [][]*schema.AgenticMessage
	main := &processChatModel{generate: func(_ context.Context, input []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		inputs = append(inputs, input)
		switch len(inputs) {
		case 1:
			feed.appendUser("再算一次")
			return assistantReply(long), nil
		case 2:
			feed.appendUser("第三问")
		}
		return assistantReply("算完了"), nil
	}}
	chatModel := &summaryModel{AgenticModel: main}
	calculator, err := newCalculatorTool()
	if err != nil {
		t.Fatal(err)
	}
	var configs []ModelConfig
	runtime := &EinoRuntime{
		newModel: func(_ context.Context, config ModelConfig) (model.AgenticModel, error) {
			configs = append(configs, config)
			return chatModel, nil
		},
		tools: []tool.BaseTool{calculator},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 窗口 4000 Token 的摘要阈值为 1600，第二轮上下文含三千字的回答时触发摘要。
	result, err := runtime.Run(ctx, RunRequest{
		RunID: "summary-run", Assignment: Assignment{AgentName: "test-agent", Model: AssignmentModel{ContextWindow: 4000}},
		MaxTurns: 3,
	}, feed)
	if err != nil || result.Content != "算完了" || result.EndSeq != 3 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	// 摘要模型按窗口的一成限定最大输出。
	if len(configs) != 2 || configs[0].MaxOutputTokens != 0 || configs[1].MaxOutputTokens != 400 {
		t.Fatalf("model configs = %+v", configs)
	}
	if len(chatModel.summaries) != 1 || len(inputs) != 3 || result.Usage.TotalTokens != 110 {
		t.Fatalf("summaries = %d, calls = %d, usage = %+v", len(chatModel.summaries), len(inputs), result.Usage)
	}
	if got := conversation(chatModel.summaries[0]); len(got) != 3 || got[0] != "开始计算" || got[1] != long {
		t.Fatalf("summarized messages = %d", len(got))
	}
	summary := summaryPreamble + "\n\n<summary>用户先让计算，已给出长篇说明</summary>"
	if got := conversation(inputs[1]); !slices.Equal(got, []string{summary, "再算一次"}) {
		t.Fatalf("second turn input = %q", got)
	}
	if got := conversation(inputs[2]); !slices.Equal(got, []string{summary, "再算一次", "算完了", "第三问"}) {
		t.Fatalf("third turn input = %q", got)
	}
}

// TestContextSummaryCompactsWithinTurn 验证同一轮内上下文超过阈值时保留最新输入与最近一轮工具调用及其结果，本轮更早的规划压进摘要并在下一轮沿用。
func TestContextSummaryCompactsWithinTurn(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser("开始计算")
	long := strings.Repeat("长", 1000)
	var mu sync.Mutex
	var inputs [][]*schema.AgenticMessage
	main := &processChatModel{generate: func(_ context.Context, input []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		inputs = append(inputs, input)
		switch len(inputs) {
		case 1, 2:
			return assistantReply(long, &schema.FunctionToolCall{
				CallID: fmt.Sprint("call-", len(inputs)), Name: "calculator", Arguments: `{"operation":"add","left":1,"right":1}`,
			}), nil
		case 3:
			feed.appendUser("再来")
		}
		return assistantReply("算完了"), nil
	}}
	chatModel := &summaryModel{AgenticModel: main}
	calculator, err := newCalculatorTool()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &EinoRuntime{
		newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil },
		tools:    []tool.BaseTool{calculator},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 窗口 4000 Token 的摘要阈值为 1600，两段千字说明使第三次规划超过阈值。
	result, err := runtime.Run(ctx, RunRequest{
		RunID: "summary-turn-run", Assignment: Assignment{AgentName: "test-agent", Model: AssignmentModel{ContextWindow: 4000}},
		MaxTurns: 2,
	}, feed)
	if err != nil || result.Content != "算完了" || len(chatModel.summaries) != 1 || len(inputs) != 4 {
		t.Fatalf("result = %#v, err = %v, summaries = %d, calls = %d", result, err, len(chatModel.summaries), len(inputs))
	}
	summary := summaryPreamble + "\n\n<summary>用户先让计算，已给出长篇说明</summary>"
	if got := conversation(inputs[2]); !slices.Equal(got, []string{summary, "开始计算", long, `{"result":2}`}) || toolCalls(inputs[2][len(inputs[2])-2])[0].CallID != "call-2" {
		t.Fatalf("input after summary = %q", got)
	}
	if got := conversation(inputs[3]); !slices.Equal(got, []string{summary, "开始计算", long, `{"result":2}`, "算完了", "再来"}) {
		t.Fatalf("next turn input = %q", got)
	}
}

// TestContextSummaryPairsPreemptedToolCalls 验证抢占留下的空参数工具调用在摘要前补全参数并补上结果，摘要请求中的工具调用都有参数和对应结果。
func TestContextSummaryPairsPreemptedToolCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	feed := &testInputFeed{}
	feed.appendUser("开始计算")
	mediaEnabled := &atomic.Bool{}
	mediaEnabled.Store(true)
	execution := &einoExecution{inputs: &turnInputs{feed: feed}, recorder: newProcessRecorder(RunRequest{}), maxTurns: 2, mediaEnabled: mediaEnabled}
	calls := 0
	chatModel := &summaryModel{AgenticModel: &processChatModel{generate: func(ctx context.Context, _ []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
		calls++
		if calls > 1 {
			return assistantReply("已按新问题回答"), nil
		}
		feed.appendUser("换一个问题")
		if err := execution.inputs.poll(ctx, true); err != nil {
			return nil, err
		}
		return assistantReply(strings.Repeat("长", 3000), &schema.FunctionToolCall{CallID: "skipped", Name: "calculator"}), nil
	}}}
	summarizer, err := newContextSummarizer(ctx, chatModel, ModelConfig{ContextWindow: 4000}, SceneAgentChat, &Usage{})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := newToolCallPatchHandler(ctx)
	if err != nil {
		t.Fatal(err)
	}
	calculator, err := newCalculatorTool()
	if err != nil {
		t.Fatal(err)
	}
	execution.summarizer = summarizer
	agent, err := adk.NewTypedChatModelAgent(ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name: "test", Model: chatModel, Handlers: []adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]{execution.recorder, &toolArgumentsNormalizer{}, patch, summarizer},
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{calculator}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	execution.inputs.loop = adk.NewTurnLoop(adk.TurnLoopConfig[Trigger, *schema.AgenticMessage]{
		GenInput: execution.genInput,
		PrepareAgent: func(context.Context, *adk.TurnLoop[Trigger, *schema.AgenticMessage], []Trigger) (adk.TypedAgent[*schema.AgenticMessage], error) {
			return agent, nil
		},
		OnAgentEvents: execution.onAgentEvents,
	})
	if err := execution.inputs.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(chatModel.summaries) != 1 || execution.result.Content != "已按新问题回答" {
		t.Fatalf("summaries = %d, result = %#v", len(chatModel.summaries), execution.result)
	}
	// 摘要请求中每个工具调用都紧跟对应结果。
	results := make(map[string]bool)
	for _, message := range chatModel.summaries[0] {
		if result := toolResult(message); result != nil {
			results[result.CallID] = true
		}
	}
	for _, message := range chatModel.summaries[0] {
		for _, call := range toolCalls(message) {
			if !results[call.CallID] || call.Arguments != "{}" {
				t.Fatalf("tool call %+v in summary request lacks arguments or result", call)
			}
		}
	}
}

// TestContextSummaryKeepsCurrentEvidence 验证客服场景摘要较早的会话后，本轮检索结果仍对模型可见并作为依据放行回答。
func TestContextSummaryKeepsCurrentEvidence(t *testing.T) {
	feed := &testInputFeed{}
	feed.appendUser(strings.Repeat("早", 2000))
	feed.appendUser(strings.Repeat("前", 2000))
	feed.appendUser("退货期限是几天")
	followUp := "那换货呢" + strings.Repeat("换", 1500)
	grounding := &groundingModel{script: []func([]*schema.AgenticMessage) *schema.AgenticMessage{
		reply("", terminalCall("k1", KnowledgeToolName, `{"queries":["退货期限"]}`)),
		func([]*schema.AgenticMessage) *schema.AgenticMessage {
			feed.appendUser(followUp)
			return assistantReply(groundedAnswer)
		},
		reply("", terminalCall("k2", KnowledgeToolName, `{"queries":["换货期限"]}`)),
		reply(groundedAnswer),
	}}
	chatModel := &summaryModel{AgenticModel: grounding}
	runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 窗口 9000 Token 的摘要阈值为 6100，第二轮补入的长问题使上下文超过阈值。
	result, err := runtime.Run(ctx, RunRequest{
		RunID: "summary-grounding-run",
		Assignment: Assignment{AgentName: "客服", Scene: SceneCustomer, Grounding: GroundingStrict,
			Model: AssignmentModel{ContextWindow: 9000}},
		MaxIterations: 6, MaxTurns: 2,
		KnowledgeSearch: matchedKnowledge("退货与换货期限都是 7 天。"),
	}, feed)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Kind != "" || result.Content != groundedAnswer || result.EndSeq != 4 || len(chatModel.summaries) != 1 {
		t.Fatalf("result = %+v, summaries = %d", result, len(chatModel.summaries))
	}
	if got := conversation(grounding.inputs[3]); len(got) != 4 || !strings.HasPrefix(got[0], summaryPreamble) || got[1] != followUp {
		t.Fatalf("input after summary = %d messages", len(got))
	}
}

// TestContextSummaryKeepsEvidence 验证客服场景同一轮取得依据后继续调用工具，压缩时依据的工具调用与结果原样保留，本轮其余过程进入摘要，回答按依据放行。
func TestContextSummaryKeepsEvidence(t *testing.T) {
	search := func(id string) *schema.FunctionToolCall {
		return terminalCall(id, KnowledgeToolName, `{"queries":["退货期限"]}`)
	}
	history := func(id string) *schema.FunctionToolCall {
		return terminalCall(id, "search_customer_history", `{"query":"退货"}`)
	}
	readBack := terminalCall("r1", offloadedResultToolName, `{"file_path":"/trunc/none"}`)
	for _, scenario := range []struct {
		name  string
		calls []*schema.FunctionToolCall
	}{
		{name: "检索后查询客户历史", calls: []*schema.FunctionToolCall{search("k1"), history("h1")}},
		{name: "检索命中后再次检索为空", calls: []*schema.FunctionToolCall{search("k1"), search("k2")}},
		{name: "检索后读回无关转存", calls: []*schema.FunctionToolCall{search("k1"), readBack}},
		{name: "检索后连续查询客户历史", calls: []*schema.FunctionToolCall{search("k1"), history("h1"), history("h2"), history("h3")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			feed := &testInputFeed{}
			feed.appendUser("退货期限是几天")
			script := make([]func([]*schema.AgenticMessage) *schema.AgenticMessage, 0, len(scenario.calls)+1)
			for i, call := range scenario.calls {
				script = append(script, func([]*schema.AgenticMessage) *schema.AgenticMessage {
					return assistantReply(strings.Repeat(string(rune('甲'+i)), 3000), call)
				})
			}
			grounding := &groundingModel{script: append(script, reply(groundedAnswer))}
			chatModel := &summaryModel{AgenticModel: grounding}
			runtime := &EinoRuntime{newModel: func(context.Context, ModelConfig) (model.AgenticModel, error) { return chatModel, nil }}
			searches := 0
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// 窗口 8000 Token 的摘要阈值为 5200，每段三千字说明使后续规划超过阈值；只有首次检索命中。
			result, err := runtime.Run(ctx, RunRequest{
				RunID: "summary-evidence-run",
				Assignment: Assignment{AgentName: "客服", Scene: SceneCustomer, Grounding: GroundingStrict,
					Model: AssignmentModel{ContextWindow: 8000}},
				MaxIterations: 8, MaxTurns: 1,
				KnowledgeSearch: func(ctx context.Context, request knowledgeretrieval.Request) (knowledgeretrieval.Result, error) {
					if searches++; searches > 1 {
						return knowledgeretrieval.Result{}, nil
					}
					return matchedKnowledge("退货期限为 7 天。")(ctx, request)
				},
				CustomerHistorySearch: func(context.Context, string) (CustomerHistoryResult, error) {
					return CustomerHistoryResult{Message: "没有相关记录"}, nil
				},
			}, feed)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision.Kind != "" || result.Content != groundedAnswer || len(chatModel.summaries) == 0 {
				t.Fatalf("result = %+v, summaries = %d", result, len(chatModel.summaries))
			}
			// 回答前的上下文依次为摘要、最新输入、依据调用与结果和最近一轮工具调用与结果，且不超过模型窗口。
			input := grounding.inputs[len(grounding.inputs)-1]
			got := conversation(input)
			if len(got) != 6 || !strings.HasPrefix(got[0], summaryPreamble) || got[1] != "退货期限是几天" ||
				toolCalls(input[len(input)-4])[0].CallID != "k1" || !strings.Contains(got[3], "退货期限为 7 天") {
				t.Fatalf("input before answer = %q", got)
			}
			if tokens, _ := countContextTokens(ctx, input, nil); tokens > 8000 {
				t.Fatalf("input tokens = %d", tokens)
			}
		})
	}
}

// TestSummaryTriggerTokens 验证摘要阈值为窗口扣除摘要输出与摘要指令，摘要输出取窗口的一成与模型最大输出中较小者。
func TestSummaryTriggerTokens(t *testing.T) {
	cases := []struct {
		model ModelConfig
		want  int
	}{
		{ModelConfig{ContextWindow: 128000, MaxOutputTokens: 8000}, 118000},
		{ModelConfig{ContextWindow: 262144, MaxOutputTokens: 262144}, 233930},
		{ModelConfig{ContextWindow: 100000}, 88000},
		{ModelConfig{}, 26800},
	}
	for _, c := range cases {
		if got := summaryTriggerTokens(c.model); got != c.want {
			t.Fatalf("summaryTriggerTokens(%+v) = %d, want %d", c.model, got, c.want)
		}
	}
}

// TestContextSummaryInstructionByScene 验证只有非客服场景在系统指令后追加上下文管理说明。
func TestContextSummaryInstructionByScene(t *testing.T) {
	ctx := context.Background()
	for _, scene := range []Scene{SceneCustomer, SceneAgentChat} {
		summarizer, err := newContextSummarizer(ctx, &summaryModel{}, ModelConfig{}, scene, &Usage{})
		if err != nil {
			t.Fatal(err)
		}
		_, runCtx, err := summarizer.BeforeAgent(ctx, &adk.ChatModelAgentContext[*schema.AgenticMessage]{Instruction: "你是客服"})
		if err != nil {
			t.Fatal(err)
		}
		if appended := runCtx.Instruction != "你是客服"; appended != (scene != SceneCustomer) {
			t.Fatal(fmt.Sprintf("scene %s instruction = %q", scene, runCtx.Instruction))
		}
	}
}
