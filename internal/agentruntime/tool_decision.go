package agentruntime

import (
	"encoding/json"

	"github.com/runforyou-ai/luway/internal/domain"
)

// toolCallOutcomeNotes 是各结果状态的工具调用事件交给模型的说明。
var toolCallOutcomeNotes = map[domain.AgentToolCallStatus]string{
	domain.AgentToolCallSucceeded:   "你之前提交的操作已获准并执行成功，result 是执行结果。告知对方结果，不要重复提交。",
	domain.AgentToolCallFailed:      "你之前提交的操作已获准，但执行失败，error 是失败原因。告知对方失败原因与后续可行的做法。",
	domain.AgentToolCallNeedsReview: "你之前提交的操作已获准，但执行中断，实际结果未知，已交由人工核对。告知对方结果待核对，不要重复提交。",
	domain.AgentToolCallRejected:    "你之前提交的操作被拒绝，没有执行。告知对方操作未获准。",
	domain.AgentToolCallExpired:     "你之前提交的操作超过确认期限未获处理，没有执行。告知对方操作已失效，需要时重新发起。",
	domain.AgentToolCallCancelled:   "你之前提交的操作已取消，没有执行：处理人已无法处理，或会话已不由你负责。需要时向当前对方重新确认后再发起。",
}

// ToolCallOutcome 是需要确认或审批的工具调用有了结果时交给模型的事件内容。
type ToolCallOutcome struct {
	Tool           string                     `json:"tool"`
	BusinessSystem string                     `json:"businessSystem,omitempty"`
	Arguments      json.RawMessage            `json:"arguments,omitempty"`
	Status         domain.AgentToolCallStatus `json:"status"`
	Result         *string                    `json:"result,omitempty"`
	Error          *string                    `json:"error,omitempty"`
	DecidedBy      string                     `json:"decidedBy,omitempty"`
}

// Message 返回作为系统事件交给模型的消息正文：事件类型、说明与调用结果。
func (o ToolCallOutcome) Message() string {
	if !json.Valid(o.Arguments) {
		o.Arguments = nil
	}
	encoded, _ := json.Marshal(struct {
		Kind string `json:"kind"`
		Note string `json:"note"`
		ToolCallOutcome
	}{Kind: "tool_call_resolved", Note: toolCallOutcomeNotes[o.Status], ToolCallOutcome: o})
	return string(encoded)
}
