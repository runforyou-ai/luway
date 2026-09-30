package agentruntime

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	// summaryInstructionTokens 摘要调用在上下文之后追加的摘要指令的 Token 估值。
	summaryInstructionTokens = 2000
	// summaryOutputWindowPercent 摘要调用的最大输出占模型窗口的百分比，模型声明的最大输出更小时取后者。
	summaryOutputWindowPercent = 10
	// summaryPreamble 是摘要消息的开头，说明其后的消息保持原样。
	summaryPreamble = "【较早对话摘要】此前的对话已压缩为以下摘要，摘要之后的消息保持原样。"
	// summaryExtraKey 标记摘要消息的扩展字段。
	summaryExtraKey = "context_summary"
)

// summaryAnalysisPattern 匹配摘要输出中供模型梳理思路的分析段。
var summaryAnalysisPattern = regexp.MustCompile(`(?s)<analysis>.*?</analysis>`)

// summaryOutputTokens 返回摘要调用的最大输出 Token 数。
func summaryOutputTokens(model ModelConfig) int {
	output := ContextWindowTokens(model) * summaryOutputWindowPercent / 100
	if model.MaxOutputTokens > 0 {
		output = min(output, model.MaxOutputTokens)
	}
	return output
}

// summaryTriggerTokens 返回摘要触发阈值：上下文、摘要指令与摘要输出之和不超过模型窗口。
func summaryTriggerTokens(model ModelConfig) int {
	return ContextWindowTokens(model) - summaryOutputTokens(model) - summaryInstructionTokens
}

// contextSummarizer 在上下文超过阈值时把较早的消息压缩为一条摘要：优先保留本轮最新输入及其后的全部上下文；放不下时保留最新输入、当前有效依据的工具调用与结果和最近一轮工具调用，本轮其余过程一并压缩。
// 加载技能的工具调用与结果在两种情况下都原样保留在摘要之后。
type contextSummarizer struct {
	// 框架摘要中间件，提供系统指令中的上下文管理说明。
	adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage]
	summarizer *summarization.TypedMiddleware[*schema.AgenticMessage]
	trigger    int
	output     int                                                     // 摘要调用的最大输出 Token 数。
	evidence   func(messages []*schema.AgenticMessage) map[string]bool // 返回上下文中构成依据的工具调用编号，未设置时不保留依据。

	mu         sync.Mutex
	keepFromID string // 本轮最新输入的消息编号，摘要只覆盖它之前的消息。
}

// historyCompacted 是摘要完成后交给轮次历史的事件。keepFromCallID 为空时，历史中 keepFromID 之前的消息替换为 summary 与 kept；
// 否则历史替换为 summary 与 kept，本轮中间消息只保留首个工具调用编号为 keepFromCallID 的模型输出及其后的消息。
type historyCompacted struct {
	summary        *schema.AgenticMessage
	keepFromID     string
	keepFromCallID string
	kept           []*schema.AgenticMessage
}

// newContextSummarizer 创建上下文摘要中间件，summaryModel 须按 summaryOutputTokens 限定最大输出，摘要调用的用量计入 usage；客服场景不在系统指令中追加上下文管理说明。
func newContextSummarizer(ctx context.Context, summaryModel model.AgenticModel, modelConfig ModelConfig, scene Scene, usage *Usage) (*contextSummarizer, error) {
	config := &summarization.TypedConfig[*schema.AgenticMessage]{
		Model: &usageRecordingModel{AgenticModel: summaryModel, usage: usage},
		// 摘要消息由去掉分析段的摘要正文组成。
		Finalize: func(_ context.Context, _ []*schema.AgenticMessage, summary *schema.AgenticMessage) ([]*schema.AgenticMessage, error) {
			text := strings.TrimSpace(summaryAnalysisPattern.ReplaceAllString(assistantText(summary), ""))
			if text == "" {
				return nil, fmt.Errorf("context summary is empty")
			}
			message := schema.UserAgenticMessage(summaryPreamble + "\n\n" + text)
			message.Extra = map[string]any{summaryExtraKey: true}
			return []*schema.AgenticMessage{message}, nil
		},
	}
	if scene.Service() {
		config.CustomFormatContextManagementInstruction = func(context.Context) string { return "" }
	}
	handler, err := summarization.NewTyped(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create context summarization middleware: %w", err)
	}
	return &contextSummarizer{
		TypedChatModelAgentMiddleware: handler,
		summarizer:                    handler.(*summarization.TypedMiddleware[*schema.AgenticMessage]),
		trigger:                       summaryTriggerTokens(modelConfig),
		output:                        summaryOutputTokens(modelConfig),
	}, nil
}

// keepFrom 登记本轮最新输入，后续摘要保留该消息及其后的上下文。
func (s *contextSummarizer) keepFrom(message *schema.AgenticMessage) {
	adk.EnsureMessageID(message)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keepFromID = adk.GetMessageID(message)
}

// BeforeModelRewriteState 在上下文超过阈值且保留点之前有可压缩的消息时生成摘要，替换执行上下文并通知轮次历史。
func (s *contextSummarizer) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	tokens, err := countContextTokens(ctx, state.Messages, state.ToolInfos)
	if err != nil {
		return ctx, nil, err
	}
	if tokens <= int64(s.trigger) {
		return ctx, state, nil
	}
	s.mu.Lock()
	keepFromID := s.keepFromID
	s.mu.Unlock()
	// 跳过开头的系统指令，定位本轮最新输入与最近一轮工具调用。
	start := 0
	for start < len(state.Messages) && state.Messages[start].Role == schema.AgenticRoleTypeSystem {
		start++
	}
	latest, round := -1, -1
	for i := start; i < len(state.Messages); i++ {
		if adk.GetMessageID(state.Messages[i]) == keepFromID {
			latest = i
		}
		if state.Messages[i].Role == schema.AgenticRoleTypeAssistant && hasToolCalls(state.Messages[i]) {
			round = i
		}
	}
	system := state.Messages[:start]
	var compaction *historyCompacted
	var summarize, kept []*schema.AgenticMessage
	keepAll := latest > start && round <= latest
	if latest > start && round > latest {
		// 最新输入之后的全部上下文加上满额摘要不超过阈值时整段保留。
		tokens, err := countContextTokens(ctx, append(slices.Clone(system), state.Messages[latest:]...), state.ToolInfos)
		if err != nil {
			return ctx, nil, err
		}
		keepAll = tokens+int64(s.output) <= int64(s.trigger)
	}
	if keepAll {
		// 最新输入之前的消息中，技能说明原样保留，其余进入摘要。
		skills := skillCallIDs(state.Messages[:latest])
		var pinned []*schema.AgenticMessage
		for i := start; i < latest; i++ {
			message, rest := splitPinned(state.Messages[i], skills)
			if message != nil {
				pinned = append(pinned, message)
			}
			if rest != nil {
				summarize = append(summarize, rest)
			}
		}
		kept = append(slices.Clone(pinned), state.Messages[latest:]...)
		compaction = &historyCompacted{keepFromID: keepFromID, kept: pinned}
	} else {
		if round <= start {
			return ctx, state, nil
		}
		// 最近一轮之前的消息中，最新输入、当前有效依据与技能说明的工具调用及结果原样保留，其余进入摘要。
		pinned := skillCallIDs(state.Messages[:round])
		if s.evidence != nil {
			maps.Copy(pinned, s.evidence(state.Messages[:round]))
		}
		for i := start; i < round; i++ {
			if i == latest {
				kept = append(kept, state.Messages[i])
				continue
			}
			message, rest := splitPinned(state.Messages[i], pinned)
			if message != nil {
				kept = append(kept, message)
			}
			if rest != nil {
				summarize = append(summarize, rest)
			}
		}
		compaction = &historyCompacted{keepFromCallID: toolCalls(state.Messages[round])[0].CallID, kept: kept}
		kept = append(slices.Clone(kept), state.Messages[round:]...)
	}
	// 没有可压缩的消息或只剩上一次的摘要时，保持上下文原样。
	if len(summarize) == 0 || (len(summarize) == 1 && summarize[0].Extra[summaryExtraKey] == true) {
		return ctx, state, nil
	}
	prefix := *state
	prefix.Messages = append(slices.Clone(system), summarize...)
	summarized, err := s.summarizer.Summarize(ctx, &prefix)
	if err != nil {
		return ctx, nil, err
	}
	compaction.summary = summarized[len(summarized)-1]
	compacted := *state
	compacted.Messages = append(append(slices.Clone(system), compaction.summary), kept...)
	after, _ := countContextTokens(ctx, compacted.Messages, compacted.ToolInfos)
	slog.Info("Agent 上下文已摘要压缩",
		"agent_run_id", runIDFromContext(ctx), "summary_threshold_tokens", s.trigger,
		"tokens_before_summary", tokens, "tokens_after_summary", after, "summarized_message_count", len(summarize))
	if err := adk.TypedSendEvent(ctx, &adk.TypedAgentEvent[*schema.AgenticMessage]{
		Action: &adk.AgentAction{CustomizedAction: compaction},
	}); err != nil {
		return ctx, nil, err
	}
	return ctx, &compacted, nil
}

// splitPinned 把消息拆为保留的工具调用与结果块组成的部分和其余部分；某部分没有内容时返回 nil，其余部分只剩思考内容时同样视为没有内容。
func splitPinned(message *schema.AgenticMessage, pinnedIDs map[string]bool) (*schema.AgenticMessage, *schema.AgenticMessage) {
	var pinned, rest []*schema.ContentBlock
	substantive := false
	for _, block := range message.ContentBlocks {
		switch {
		case block.Type == schema.ContentBlockTypeFunctionToolCall && pinnedIDs[block.FunctionToolCall.CallID],
			block.Type == schema.ContentBlockTypeFunctionToolResult && pinnedIDs[block.FunctionToolResult.CallID]:
			pinned = append(pinned, block)
		default:
			rest = append(rest, block)
			substantive = substantive || block.Type != schema.ContentBlockTypeReasoning
		}
	}
	if len(pinned) == 0 {
		return nil, message
	}
	var restMessage *schema.AgenticMessage
	if substantive {
		restMessage = &schema.AgenticMessage{Role: message.Role, ContentBlocks: rest}
	}
	return &schema.AgenticMessage{Role: message.Role, ContentBlocks: pinned}, restMessage
}

// toolCalls 返回模型输出中的工具调用块。
func toolCalls(message *schema.AgenticMessage) []*schema.FunctionToolCall {
	var calls []*schema.FunctionToolCall
	for _, block := range message.ContentBlocks {
		if block.Type == schema.ContentBlockTypeFunctionToolCall {
			calls = append(calls, block.FunctionToolCall)
		}
	}
	return calls
}

// newToolCallPatchHandler 创建工具调用修补中间件：没有结果的调用补上取消说明，没有对应调用或重复的结果从模型输入中移除。
func newToolCallPatchHandler(ctx context.Context) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	handler, err := patchtoolcalls.NewTyped[*schema.AgenticMessage](ctx, &patchtoolcalls.Config{
		RemoveOrphanResults: true, RemoveDuplicateResults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create tool call patch middleware: %w", err)
	}
	return handler, nil
}

// usageRecordingModel 在模型调用返回时累计其用量，用于摘要生成等不经过主循环的调用。
type usageRecordingModel struct {
	model.AgenticModel
	usage *Usage
}

// Generate 调用底层模型并累计本次用量。
func (m *usageRecordingModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	output, err := m.AgenticModel.Generate(ctx, input, opts...)
	if output != nil {
		m.usage.add(output.ResponseMeta)
	}
	return output, err
}
