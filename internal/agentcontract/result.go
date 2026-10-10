package agentcontract

import (
	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/domain"
)

// Usage 定义一次业务运行累计的模型用量，是该运行模型调用记录（ai_model_calls）中对话模型 Token 用量的汇总投影：PromptTokens 对应输入、CompletionTokens 对应输出，TotalTokens 为两者之和。
type Usage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

// UsageFrom 把模型调用的用量转换为运行用量，合计按输入与输出之和计算。
func UsageFrom(used llm.Usage) Usage {
	return Usage{PromptTokens: used.Input, CompletionTokens: used.Output, TotalTokens: used.Input + used.Output}
}

// Merge 累计另一份用量。
func (u *Usage) Merge(other Usage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

// TerminalDecision 定义一次执行的结束方式；Kind 为空表示直接输出正文作为回答。
type TerminalDecision struct {
	Kind       domain.AgentRunOutcome
	Purpose    domain.AgentAskCustomerPurpose // ask_customer 的发问用途。
	Reason     domain.AgentHandoffReason      // handoff 的原因。
	ReasonText string                         // handoff 时模型写明的转交说明，仅成员可见。
	CategoryID string                         // handoff 时模型选择的咨询分类编号，未选择时为空。
}

// Outcome 返回决定对应的运行结果类型。
func (d TerminalDecision) Outcome() domain.AgentRunOutcome {
	if d.Kind == "" {
		return domain.AgentRunOutcomeReply
	}
	return d.Kind
}
