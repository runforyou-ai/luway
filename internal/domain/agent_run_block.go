package domain

import "time"

// AgentRunBlockKind 定义 Agent 中间内容的语义类型。
type AgentRunBlockKind string

const (
	AgentRunBlockThinking AgentRunBlockKind = "thinking"
	AgentRunBlockContent  AgentRunBlockKind = "content"
	AgentRunBlockToolCall AgentRunBlockKind = "tool_call"
)

// AgentToolCallStatus 定义一次工具调用的执行状态。
type AgentToolCallStatus string

const (
	AgentToolCallQueued    AgentToolCallStatus = "queued"
	AgentToolCallRunning   AgentToolCallStatus = "running"
	AgentToolCallWaiting   AgentToolCallStatus = "waiting"
	AgentToolCallSucceeded AgentToolCallStatus = "succeeded"
	AgentToolCallFailed    AgentToolCallStatus = "failed"
	// AgentToolCallCancelled 表示运行结束时调用尚未完成。
	AgentToolCallCancelled AgentToolCallStatus = "cancelled"
	// AgentToolCallInterrupted 表示执行中断且没有外部副作用，模型收到中断结果。
	AgentToolCallInterrupted AgentToolCallStatus = "interrupted"
	// AgentToolCallNeedsReview 表示有外部副作用的调用中断，实际结果待人工核对。
	AgentToolCallNeedsReview AgentToolCallStatus = "needs_review"
	// AgentToolCallAwaitingDecision 表示调用已提交，等待确认或审批；批准后转为待执行。
	AgentToolCallAwaitingDecision AgentToolCallStatus = "awaiting_decision"
	// AgentToolCallRejected 表示确认或审批被拒绝，调用未执行。
	AgentToolCallRejected AgentToolCallStatus = "rejected"
	// AgentToolCallExpired 表示确认或审批超过截止时间，调用未执行。
	AgentToolCallExpired AgentToolCallStatus = "expired"
	// AgentToolCallReviewed 表示待核对的调用已由成员核对。
	AgentToolCallReviewed AgentToolCallStatus = "reviewed"
)

// ToolDecisionTimeout 是确认或审批的有效期。
const ToolDecisionTimeout = 24 * time.Hour

// Settled 判断工具调用是否已有最终结果。
func (s AgentToolCallStatus) Settled() bool {
	switch s {
	case AgentToolCallSucceeded, AgentToolCallFailed, AgentToolCallCancelled, AgentToolCallInterrupted, AgentToolCallNeedsReview,
		AgentToolCallRejected, AgentToolCallExpired, AgentToolCallReviewed:
		return true
	}
	return false
}

// AgentToolSource 定义工具调用的来源。
type AgentToolSource string

const (
	AgentToolSourceBuiltin        AgentToolSource = "builtin"
	AgentToolSourceBusinessSystem AgentToolSource = "business_system"
	AgentToolSourceMCP            AgentToolSource = "mcp" // 电脑上的本机 MCP 服务。
	AgentToolSourceDelegation     AgentToolSource = "delegation"
	AgentToolSourceLocalAgent     AgentToolSource = "local_agent" // 本机 Agent 请求权限的操作。
)

// AgentPlanTaskStatus 定义运行任务清单中一项任务的状态。
type AgentPlanTaskStatus string

const (
	AgentPlanTaskPending    AgentPlanTaskStatus = "pending"
	AgentPlanTaskInProgress AgentPlanTaskStatus = "in_progress"
	AgentPlanTaskCompleted  AgentPlanTaskStatus = "completed"
)
