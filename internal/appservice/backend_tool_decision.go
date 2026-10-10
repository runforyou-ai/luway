package appservice

import "context"

// ToolDecisionBackend 定义 AI 员工待处理操作的确认、审批与核对的业务调用。
type ToolDecisionBackend interface {
	// ListAgentToolDecisions 返回待当前成员确认、审批或核对的 AI 员工操作。
	//appservice:route GET /agent-tool-decisions perm=none
	ListAgentToolDecisions(context.Context, RequestMeta) (AgentToolDecisionList, error)
	// DecideAgentToolCall 由处理成员确认、批准或拒绝 AI 员工提交的操作。
	//appservice:route POST /agent-tool-calls/{toolCallID:uuid}/decision perm=none
	DecideAgentToolCall(context.Context, RequestMeta, string, AgentToolDecisionInput) error
	// ReviewAgentToolCall 由 AI 员工的负责人把结果未知的操作标记为已核对。
	//appservice:route POST /agent-tool-calls/{toolCallID:uuid}/review perm=none
	ReviewAgentToolCall(context.Context, RequestMeta, string) error
}
