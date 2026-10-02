package domain

// AgentRunStatus 定义一次 Agent 业务运行的状态。
type AgentRunStatus string

const (
	AgentRunStatusQueued    AgentRunStatus = "queued"
	AgentRunStatusRunning   AgentRunStatus = "running"
	AgentRunStatusSucceeded AgentRunStatus = "succeeded"
	AgentRunStatusFailed    AgentRunStatus = "failed"
	AgentRunStatusCancelled AgentRunStatus = "cancelled"
)

// AgentExecutionScopeKind 定义 Agent 执行范围的类型。
type AgentExecutionScopeKind string

const (
	AgentExecutionScopeConversation   AgentExecutionScopeKind = "conversation"
	AgentExecutionScopeServiceSession AgentExecutionScopeKind = "service_session"
)

// AgentInputKind 定义 Agent 持久输入的业务入口。
type AgentInputKind string

const (
	AgentInputKindMention      AgentInputKind = "mention"
	AgentInputKindHandoff      AgentInputKind = "handoff"
	AgentInputKindAgentDirect  AgentInputKind = "agent_direct"
	AgentInputKindCustomerAuto AgentInputKind = "customer_auto"
	AgentInputKindCopilot      AgentInputKind = "copilot"
	AgentInputKindFollowUp     AgentInputKind = "follow_up"
)

// AgentRunErrorCode 定义 Agent 运行取消或失败的稳定原因。
type AgentRunErrorCode string

const (
	AgentRunErrorCodeAssigneeChanged AgentRunErrorCode = "assignee_changed"
	AgentRunErrorCodeSessionClosed   AgentRunErrorCode = "session_closed"
	AgentRunErrorCodeUserCancelled   AgentRunErrorCode = "user_cancelled"
	AgentRunErrorCodeBotChanged      AgentRunErrorCode = "bot_changed"
	AgentRunErrorCodeAgentRemoved    AgentRunErrorCode = "agent_removed"
)

// 设备执行的运行失败原因。
const (
	// AgentRunErrorCodeDeviceLeaseExpired 表示执行设备未按时续租，运行以失败结束。
	AgentRunErrorCodeDeviceLeaseExpired AgentRunErrorCode = "device_lease_expired"
	// AgentRunErrorCodeDeviceUnavailable 表示执行设备已撤销或设备所属成员已停用。
	AgentRunErrorCodeDeviceUnavailable AgentRunErrorCode = "device_unavailable"
	// AgentRunErrorCodeExecutionChanged 表示运行派发后个人 AI 员工已换电脑，排队中的运行不再执行。
	AgentRunErrorCodeExecutionChanged AgentRunErrorCode = "execution_changed"
	// AgentRunErrorCodeDeviceRunFailed 表示设备上的运行时执行失败。
	AgentRunErrorCodeDeviceRunFailed AgentRunErrorCode = "device_run_failed"
	// AgentRunErrorCodeLocalAgentAuthRequired 表示执行运行的本机 Agent 尚未在电脑上登录。
	AgentRunErrorCodeLocalAgentAuthRequired AgentRunErrorCode = "local_agent_auth_required"
	// AgentRunErrorCodeDeviceRunTimedOut 表示设备运行自领取起超出总时限。
	AgentRunErrorCodeDeviceRunTimedOut AgentRunErrorCode = "device_run_timed_out"
)

// AgentRunErrorCodeAgentUnavailable 表示 AI 员工被停用或失去接客资格，由管理操作取消运行。
const AgentRunErrorCodeAgentUnavailable AgentRunErrorCode = "agent_unavailable"

// AgentRunOutcome 定义一次 Agent 运行的结束方式。
type AgentRunOutcome string

const (
	AgentRunOutcomeReply       AgentRunOutcome = "reply"
	AgentRunOutcomeAskCustomer AgentRunOutcome = "ask_customer"
	AgentRunOutcomeHandoff     AgentRunOutcome = "handoff"
	AgentRunOutcomeResolve     AgentRunOutcome = "resolve"
)

// AgentHandoffReason 定义 AI 客服把会话转交人工的原因：业务原因由模型调用 handoff_to_human 时给出，其余为 Runtime 给出的系统原因。
type AgentHandoffReason string

const (
	AgentHandoffReasonKnowledgeGap         AgentHandoffReason = "knowledge_gap"
	AgentHandoffReasonCustomerRequested    AgentHandoffReason = "customer_requested"
	AgentHandoffReasonNeedsHumanJudgment   AgentHandoffReason = "needs_human_judgment"
	AgentHandoffReasonComplaint            AgentHandoffReason = "complaint"
	AgentHandoffReasonInsufficientEvidence AgentHandoffReason = "insufficient_evidence"
	AgentHandoffReasonBudgetExhausted      AgentHandoffReason = "budget_exhausted"
	AgentHandoffReasonInvalidOutput        AgentHandoffReason = "invalid_output"
	AgentHandoffReasonRuntimeFailed        AgentHandoffReason = "runtime_failed"
	AgentHandoffReasonTimeout              AgentHandoffReason = "timeout"
	AgentHandoffReasonAgentUnavailable     AgentHandoffReason = "agent_unavailable"
)

// AgentHandoffBusinessReasons 按展示顺序列出模型可选的业务原因。
var AgentHandoffBusinessReasons = []AgentHandoffReason{
	AgentHandoffReasonKnowledgeGap, AgentHandoffReasonCustomerRequested, AgentHandoffReasonNeedsHumanJudgment, AgentHandoffReasonComplaint,
}

// AgentAskCustomerPurpose 定义 AI 客服向客户发问的用途；confirm_resolution 决定周期的超时关单，其余只用于审计与统计。
type AgentAskCustomerPurpose string

const (
	AgentAskCustomerPurposeGreeting AgentAskCustomerPurpose = "greeting"
	AgentAskCustomerPurposeClarify  AgentAskCustomerPurpose = "clarify"
	AgentAskCustomerPurposeConfirm  AgentAskCustomerPurpose = "confirm"
	// AgentAskCustomerPurposeConfirmResolution 请客户确认问题是否已解决，发出后周期进入等待确认。
	AgentAskCustomerPurposeConfirmResolution AgentAskCustomerPurpose = "confirm_resolution"
)
