package domain

// AgentEvaluationCaseSource 定义评测用例的来源。
type AgentEvaluationCaseSource string

const (
	AgentEvaluationCaseSourceManual         AgentEvaluationCaseSource = "manual"
	AgentEvaluationCaseSourceKnowledgeGap   AgentEvaluationCaseSource = "knowledge_gap"
	AgentEvaluationCaseSourceServiceSession AgentEvaluationCaseSource = "service_session"
)

// AgentEvaluationRunStatus 定义评测运行的状态。
type AgentEvaluationRunStatus string

const (
	AgentEvaluationRunStatusRunning   AgentEvaluationRunStatus = "running"
	AgentEvaluationRunStatusCompleted AgentEvaluationRunStatus = "completed"
)

// AgentEvaluationResultStatus 定义一次评测尝试的结果：error 表示模型调用失败或运行超时，不计通过与未通过。
type AgentEvaluationResultStatus string

const (
	AgentEvaluationResultStatusPending AgentEvaluationResultStatus = "pending"
	AgentEvaluationResultStatusPassed  AgentEvaluationResultStatus = "passed"
	AgentEvaluationResultStatusFailed  AgentEvaluationResultStatus = "failed"
	AgentEvaluationResultStatusError   AgentEvaluationResultStatus = "error"
)

// AgentEvaluationErrorCode 定义评测异常的语言无关原因。
type AgentEvaluationErrorCode string

const (
	AgentEvaluationErrorRuntimeFailed            AgentEvaluationErrorCode = "runtime_failed"
	AgentEvaluationErrorTimeout                  AgentEvaluationErrorCode = "timeout"
	AgentEvaluationErrorConfigurationUnavailable AgentEvaluationErrorCode = "configuration_unavailable"
	AgentEvaluationErrorDecisionModelUnavailable AgentEvaluationErrorCode = "decision_model_unavailable"
	AgentEvaluationErrorDecisionFailed           AgentEvaluationErrorCode = "decision_failed"
)
