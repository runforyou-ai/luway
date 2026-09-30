package agentruntime

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/cervi/internal/integration/knowledgeretrieval"
)

const (
	// truncOffloadDir 与 clearOffloadDir 是工具结果转存路径前缀，路径末段为来源工具调用编号。
	truncOffloadDir = "/trunc/"
	clearOffloadDir = "/clear/"
)

// evidenceResult 是一条对模型完整可见的依据：callID 是结果所属调用，source 是依据来源调用，转存读回时两者不同。
type evidenceResult struct {
	callID string
	source string
}

// evidenceJudge 判断一次工具调用的原始结果是否构成回答依据。
type evidenceJudge func(output string) bool

// groundingGate 按模型实际可见的上下文判定直接输出的正文是否在当前输入边界内取得有效依据；依据来源由工具名到判定函数的登记决定。
type groundingGate struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	judges     map[string]evidenceJudge
	onEvidence func(callID string) // 来源工具调用的原始结果对模型完整可见时通知过程记录。

	mu       sync.Mutex
	valid    map[string]string // 当前输入边界内原始结果通过判定的工具调用编号及其原始结果。
	grounded bool              // 最近一次不含工具调用的模型输出是否取得依据。
}

// newGroundingGate 按依据来源登记创建一次执行尝试共用的依据门禁。
func newGroundingGate(judges map[string]evidenceJudge, onEvidence func(callID string)) *groundingGate {
	return &groundingGate{judges: judges, onEvidence: onEvidence, valid: make(map[string]string)}
}

// WrapInvokableToolCall 在依据来源工具返回时按原始结果判定并登记，门禁位于上下文治理内层，取得的是截断前的结果。
func (g *groundingGate) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	judge, ok := g.judges[tCtx.Name]
	if !ok {
		return endpoint, nil
	}
	return func(ctx context.Context, arguments string, opts ...tool.Option) (string, error) {
		output, err := endpoint(ctx, arguments, opts...)
		if err == nil && judge(output) {
			g.mu.Lock()
			g.valid[tCtx.CallID] = output
			g.mu.Unlock()
		}
		return output, err
	}, nil
}

// resetBoundary 在认领新的持久输入时开始新的依据边界，依据只计入此后登记的来源调用。
func (g *groundingGate) resetBoundary() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.valid = make(map[string]string)
	g.grounded = false
}

// AfterModelRewriteState 在模型给出不含工具调用的输出时，按本次发给模型的上下文判定是否取得依据，并标记完整可见的来源调用。
func (g *groundingGate) AfterModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage], _ *adk.TypedModelContext[*schema.AgenticMessage]) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	if hasToolCalls(state.Messages[len(state.Messages)-1]) {
		return ctx, state, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	evidence := g.visibleEvidence(state.Messages[:len(state.Messages)-1])
	g.grounded = len(evidence) > 0
	for _, item := range evidence {
		g.onEvidence(item.source)
	}
	return ctx, state, nil
}

// evidenceCallIDs 返回上下文中构成依据的工具结果所属调用编号，包括读回依据的转存读回调用。
func (g *groundingGate) evidenceCallIDs(messages []*schema.AgenticMessage) map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make(map[string]bool)
	for _, item := range g.visibleEvidence(messages) {
		ids[item.callID] = true
	}
	return ids
}

// visibleEvidence 按上下文顺序返回对模型完整可见的依据；调用方须持有锁。
func (g *groundingGate) visibleEvidence(messages []*schema.AgenticMessage) []evidenceResult {
	// 汇总工具调用的名称与参数，转存读回按参数中的路径关联来源调用。
	calls := make(map[string]*schema.FunctionToolCall)
	for _, message := range messages {
		for _, block := range message.ContentBlocks {
			if block.Type == schema.ContentBlockTypeFunctionToolCall {
				calls[block.FunctionToolCall.CallID] = block.FunctionToolCall
			}
		}
	}
	var evidence []evidenceResult
	for _, message := range messages {
		for _, block := range message.ContentBlocks {
			if block.Type != schema.ContentBlockTypeFunctionToolResult {
				continue
			}
			result := block.FunctionToolResult
			call, called := calls[result.CallID]
			if !called {
				continue
			}
			// 拼接工具结果中的文本块。
			var builder strings.Builder
			for _, content := range result.Content {
				if content.Type == schema.FunctionToolResultContentBlockTypeText {
					builder.WriteString(content.Text.Text)
				}
			}
			text := builder.String()
			// 依据来源的结果与原始结果一致时模型可见；截断或清理后需经转存读回。
			source := result.CallID
			visible := false
			if call.Name == offloadedResultToolName {
				source, visible = g.readsBackEvidence(call.Arguments, text)
			} else if output, valid := g.valid[result.CallID]; valid {
				visible = text == output
			}
			if visible {
				evidence = append(evidence, evidenceResult{callID: result.CallID, source: source})
			}
		}
	}
	return evidence
}

// readsBackEvidence 判断一次转存读回是否把当前边界内已通过判定的来源结果完整取回，返回来源调用编号；读回文本去掉行号后须包含原始结果。
func (g *groundingGate) readsBackEvidence(arguments, text string) (string, bool) {
	input := struct {
		FilePath string `json:"file_path"`
	}{}
	if json.Unmarshal([]byte(arguments), &input) != nil {
		return "", false
	}
	source, trunc := strings.CutPrefix(input.FilePath, truncOffloadDir)
	if !trunc {
		var clear bool
		if source, clear = strings.CutPrefix(input.FilePath, clearOffloadDir); !clear {
			return "", false
		}
	}
	output, valid := g.valid[source]
	if !valid {
		return "", false
	}
	// 读回工具按「行号、制表符、原文」逐行返回；失败说明不带行号，按未读回处理。
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		number, content, numbered := strings.Cut(line, "\t")
		if _, err := strconv.Atoi(strings.TrimSpace(number)); !numbered || err != nil {
			return "", false
		}
		lines[i] = content
	}
	return source, strings.Contains(strings.Join(lines, "\n"), output)
}

// verdict 返回最近一次直接输出正文时的依据判定。
func (g *groundingGate) verdict() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.grounded
}

// queryEvidence 判定业务查询工具的结果去除空白后非空，没有记录的空列表或空对象同样构成依据。
func queryEvidence(output string) bool {
	return strings.TrimSpace(output) != ""
}

// knowledgeEvidence 判定知识检索结果含有正文非空的命中记录。
func knowledgeEvidence(output string) bool {
	result := knowledgeretrieval.Result{}
	if json.Unmarshal([]byte(output), &result) != nil {
		return false
	}
	for _, record := range result.Records {
		if record.Matched && strings.TrimSpace(record.Content) != "" {
			return true
		}
	}
	return false
}
