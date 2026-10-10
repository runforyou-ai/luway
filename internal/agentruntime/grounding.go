package agentruntime

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"
	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/knowledgeretrieval"
	"github.com/runforyou-ai/support/str"
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

// groundingGateName 是依据门禁扩展的名称。
const groundingGateName = "luway.grounding"

// groundingGate 按模型实际可见的上下文判定直接输出的正文是否在当前输入边界内取得有效依据，依据来源由工具名到判定函数的登记决定。
// 它作为运行扩展同时审查候选正文、观察工具结果与认领的输入、在摘要时保留依据调用，并把依据边界保存在恢复状态中。
type groundingGate struct {
	judges map[string]evidenceJudge
	writes map[string]struct{} // 结果构成依据但会写入业务数据的工具，纠正提示不要求调用它们查证。

	mu      sync.Mutex
	valid   map[string]string // 当前输入边界内原始结果通过判定的工具调用（模型给出的标识）及其原始结果。
	facts   []string          // 当前输入边界内认领的、位于最近一条 AI 回复之后的系统事实。
	records map[string]string // 主 Agent 调用的模型给出的标识到调用记录编号。
}

// groundingState 是依据门禁在恢复状态中的内容。
type groundingState struct {
	Valid   map[string]string `json:"valid,omitempty"`
	Facts   []string          `json:"facts,omitempty"`
	Records map[string]string `json:"records,omitempty"`
}

// newGroundingGate 创建一次运行的依据门禁，依据来源由注册工具时按工具描述登记。
func newGroundingGate() *groundingGate {
	return &groundingGate{judges: map[string]evidenceJudge{}, writes: map[string]struct{}{}, valid: map[string]string{}, records: map[string]string{}}
}

// Name 返回扩展名称。
func (g *groundingGate) Name() string { return groundingGateName }

// OnClaim 在认领新的持久输入时开始新的依据边界，依据只计入此后登记的来源调用与本次认领中位于最近一条 AI 回复之后的系统事实。
func (g *groundingGate) OnClaim(_ context.Context, claim einorun.Claim) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.valid = map[string]string{}
	g.facts = nil
	for index := len(claim.Messages) - 1; index >= 0 && claim.Messages[index].Role != einorun.RoleAssistant; index-- {
		if claim.Messages[index].Meta[agentcontract.MetaFact] == "true" {
			g.facts = append(g.facts, claim.Messages[index].Content)
		}
	}
}

// AfterTool 登记主 Agent 依据来源调用的原始结果：执行成功且通过判定的结果、提交确认或审批的回执，以及恢复时补回且不是转存预览的结果。
func (g *groundingGate) AfterTool(_ context.Context, outcome einorun.CallOutcome) {
	if !outcome.Agent.Main {
		return
	}
	call := outcome.Call
	g.mu.Lock()
	defer g.mu.Unlock()
	g.records[call.CallID] = call.ID
	judge, ok := g.judges[outcome.Name]
	if !ok {
		return
	}
	switch {
	case outcome.Origin == einorun.OriginSubmitted || call.Handover == einorun.HandoverSubmitted:
		g.valid[call.CallID] = outcome.Raw
	case outcome.Origin == einorun.OriginRestored:
		// 过程记录保存的是模型看到的结果，超长结果为转存预览，不是原始结果，不登记；模型经转存读回时按读回登记。
		if str.Contains(outcome.Raw, truncOffloadDir+call.CallID, clearOffloadDir+call.CallID) {
			return
		}
		if call.Status == einorun.StatusSucceeded && judge(outcome.Raw) {
			g.valid[call.CallID] = outcome.Raw
		}
	case call.Status == einorun.StatusSucceeded && judge(outcome.Raw):
		g.valid[call.CallID] = outcome.Raw
	}
}

// Review 审查直接输出的正文：按模型实际看到的上下文判定是否取得依据，完整可见的来源调用标记为回答依据；没有依据时要求纠正，额度用尽时由完成策略转人工。
func (g *groundingGate) Review(_ context.Context, view einorun.TurnView) (einorun.Verdict, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	evidence := g.visibleEvidence(view.ModelInput)
	notes := map[string]map[string]string{}
	for _, item := range evidence {
		if record, ok := g.records[item.source]; ok && item.source != "" {
			notes[record] = map[string]string{agentcontract.NoteEvidence: "true"}
		}
	}
	if len(evidence) > 0 {
		return einorun.Verdict{Kind: einorun.Accept, Notes: notes}, nil
	}
	return einorun.Verdict{Kind: einorun.Correct, Prompt: g.correction(), Notes: notes}, nil
}

// correction 返回正文缺少依据时的纠正提示，按名称顺序列出本次登记的查询类依据来源工具。
func (g *groundingGate) correction() string {
	tools := slices.DeleteFunc(slices.Sorted(maps.Keys(g.judges)), func(name string) bool {
		_, write := g.writes[name]
		return write
	})
	lookup := ""
	if len(tools) > 0 {
		lookup = "涉及企业具体信息请先调用 " + strings.Join(tools, "、") + " 查证；"
	}
	return "【系统提示】上面的回答没有取得依据，不会发给客户。" + lookup + "需要客户补充信息或只是问候请调用 ask_customer；无法解答请调用 handoff_to_human。"
}

// Pin 返回上下文中构成依据的工具结果所属调用，摘要压缩时原样保留。
func (g *groundingGate) Pin(messages []*schema.AgenticMessage) map[string]bool {
	return g.evidenceCallIDs(messages)
}

// Save 返回当前依据边界，写入恢复状态。
func (g *groundingGate) Save() (json.RawMessage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return json.Marshal(groundingState{Valid: g.valid, Facts: g.facts, Records: g.records})
}

// Restore 以恢复状态中的依据边界重建门禁。
func (g *groundingGate) Restore(data json.RawMessage) error {
	var state groundingState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.valid, g.facts, g.records = maps.Clone(state.Valid), state.Facts, maps.Clone(state.Records)
	if g.valid == nil {
		g.valid = map[string]string{}
	}
	if g.records == nil {
		g.records = map[string]string{}
	}
	return nil
}

// evidenceCallIDs 返回上下文中构成依据的工具结果所属调用编号，包括读回依据的转存读回调用。
func (g *groundingGate) evidenceCallIDs(messages []*schema.AgenticMessage) map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	ids := make(map[string]bool)
	for _, item := range g.visibleEvidence(messages) {
		if item.callID != "" {
			ids[item.callID] = true
		}
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
			// 系统事实以用户消息进入上下文，正文包含完整事实时构成依据，不对应本次运行的工具调用。
			if block.Type == schema.ContentBlockTypeUserInputText && slices.ContainsFunc(g.facts, func(fact string) bool {
				return strings.Contains(block.UserInputText.Text, fact)
			}) {
				evidence = append(evidence, evidenceResult{})
				continue
			}
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
			if call.Name == einorun.OffloadReadTool {
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

// queryEvidence 判定业务系统工具的查询或写入结果去除空白后非空，没有记录的空列表或空对象同样构成依据。
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
