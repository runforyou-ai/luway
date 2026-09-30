package appservice

import "github.com/runforyou-ai/cervi/internal/domain"

// AgentRunBlockKind 定义思考区域中的内容类型。
type AgentRunBlockKind string

const (
	AgentRunBlockThinking AgentRunBlockKind = AgentRunBlockKind(domain.AgentRunBlockThinking)
	AgentRunBlockContent  AgentRunBlockKind = AgentRunBlockKind(domain.AgentRunBlockContent)
	AgentRunBlockToolCall AgentRunBlockKind = AgentRunBlockKind(domain.AgentRunBlockToolCall)
)

// AgentToolCallStatus 定义工具执行状态。
type AgentToolCallStatus string

const (
	AgentToolCallQueued    AgentToolCallStatus = AgentToolCallStatus(domain.AgentToolCallQueued)
	AgentToolCallRunning   AgentToolCallStatus = AgentToolCallStatus(domain.AgentToolCallRunning)
	AgentToolCallSucceeded AgentToolCallStatus = AgentToolCallStatus(domain.AgentToolCallSucceeded)
	AgentToolCallFailed    AgentToolCallStatus = AgentToolCallStatus(domain.AgentToolCallFailed)
)

// AgentPlanTaskStatus 定义任务清单中一项任务的状态。
type AgentPlanTaskStatus string

const (
	AgentPlanTaskPending    AgentPlanTaskStatus = AgentPlanTaskStatus(domain.AgentPlanTaskPending)
	AgentPlanTaskInProgress AgentPlanTaskStatus = AgentPlanTaskStatus(domain.AgentPlanTaskInProgress)
	AgentPlanTaskCompleted  AgentPlanTaskStatus = AgentPlanTaskStatus(domain.AgentPlanTaskCompleted)
)

// AgentRunOutcome 定义 Agent 运行的结束方式。
type AgentRunOutcome string

const (
	AgentRunOutcomeReply       AgentRunOutcome = AgentRunOutcome(domain.AgentRunOutcomeReply)
	AgentRunOutcomeAskCustomer AgentRunOutcome = AgentRunOutcome(domain.AgentRunOutcomeAskCustomer)
	AgentRunOutcomeHandoff     AgentRunOutcome = AgentRunOutcome(domain.AgentRunOutcomeHandoff)
	AgentRunOutcomeResolve     AgentRunOutcome = AgentRunOutcome(domain.AgentRunOutcomeResolve)
)

// AgentHandoffReason 定义 AI 客服转交人工的原因。
type AgentHandoffReason string

const (
	AgentHandoffReasonKnowledgeGap         AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonKnowledgeGap)
	AgentHandoffReasonCustomerRequested    AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonCustomerRequested)
	AgentHandoffReasonNeedsHumanJudgment   AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonNeedsHumanJudgment)
	AgentHandoffReasonComplaint            AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonComplaint)
	AgentHandoffReasonInsufficientEvidence AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonInsufficientEvidence)
	AgentHandoffReasonBudgetExhausted      AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonBudgetExhausted)
	AgentHandoffReasonInvalidOutput        AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonInvalidOutput)
	AgentHandoffReasonRuntimeFailed        AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonRuntimeFailed)
	AgentHandoffReasonTimeout              AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonTimeout)
	AgentHandoffReasonAgentUnavailable     AgentHandoffReason = AgentHandoffReason(domain.AgentHandoffReasonAgentUnavailable)
)

// ConversationAgentProcess 定义已完成运行的过程引用和模型用量，过程内容按运行编号单独读取。
type ConversationAgentProcess struct {
	ID                   string              `json:"id"`
	DurationMilliseconds int64               `json:"durationMilliseconds"`
	InputTokens          int                 `json:"inputTokens"`
	OutputTokens         int                 `json:"outputTokens"`
	Outcome              *AgentRunOutcome    `json:"outcome"`
	OutcomeReason        *AgentHandoffReason `json:"outcomeReason"`
}

// AgentRunProcess 定义一次已完成运行的有序过程内容、任务清单和模型用量。
type AgentRunProcess struct {
	ID                   string                 `json:"id"`
	DurationMilliseconds int64                  `json:"durationMilliseconds"`
	InputTokens          int                    `json:"inputTokens"`
	OutputTokens         int                    `json:"outputTokens"`
	Outcome              *AgentRunOutcome       `json:"outcome"`
	OutcomeReason        *AgentHandoffReason    `json:"outcomeReason"`
	Blocks               []AgentRunContentBlock `json:"blocks"`
	Plan                 []AgentPlanTask        `json:"plan"` // 运行结束时的任务清单，没有建立清单时为空数组。
}

// AgentPlanTask 定义任务清单中的一项任务。
type AgentPlanTask struct {
	ID      string              `json:"id"`
	Subject string              `json:"subject"`
	Status  AgentPlanTaskStatus `json:"status"`
}

// AgentRunContentBlock 定义一个独立的文本或工具内容块。
type AgentRunContentBlock struct {
	ID       string            `json:"id"`
	Position int64             `json:"position"`
	Kind     AgentRunBlockKind `json:"kind"`
	Text     string            `json:"text"`
	ToolCall *AgentToolCall    `json:"toolCall"`
}

// AgentToolCall 定义完整工具参数、结果和错误。
type AgentToolCall struct {
	Name      string              `json:"name"`
	Arguments string              `json:"arguments"`
	Result    *string             `json:"result"`
	Error     *string             `json:"error"`
	Status    AgentToolCallStatus `json:"status"`
}

// ConversationAgentRun 定义消息窗口中尚未由结果消息表达的运行状态，取消运行携带自身过程引用。
type ConversationAgentRun struct {
	AgentName string `json:"agentName"`
	// AgentAssistantOwnerName 是执行者为助理时其主人的名称，AI 员工为空。
	AgentAssistantOwnerName *string                   `json:"agentAssistantOwnerName"`
	AgentAvatarURL          string                    `json:"agentAvatarUrl"`
	ID                      string                    `json:"id"`
	AgentIdentityID         string                    `json:"agentIdentityId"`
	Status                  AgentRunStatus            `json:"status"`
	ErrorCode               *string                   `json:"errorCode"`
	LastError               *string                   `json:"lastError"`
	Process                 *ConversationAgentProcess `json:"process"`
	// ExecutionDeviceID 是执行该运行的设备编号，服务端执行时为空。
	ExecutionDeviceID *string `json:"executionDeviceId"`
	// ExecutionDeviceName 是执行该运行的设备名称，服务端执行时为空。
	ExecutionDeviceName *string `json:"executionDeviceName"`
}
