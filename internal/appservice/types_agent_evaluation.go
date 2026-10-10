package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AgentEvaluationCaseSource 定义评测用例的来源。
type AgentEvaluationCaseSource = domain.AgentEvaluationCaseSource

// AgentEvaluationRunStatus 定义评测运行的状态。
type AgentEvaluationRunStatus = domain.AgentEvaluationRunStatus

// AgentEvaluationResultStatus 定义一次评测尝试的结果：error 为评测异常，不计通过与未通过。
type AgentEvaluationResultStatus = domain.AgentEvaluationResultStatus

// AgentEvaluationErrorCode 定义评测异常的原因。
type AgentEvaluationErrorCode = domain.AgentEvaluationErrorCode

// AgentEvaluationContextSender 定义用例前文中消息的发送方：customer 提问人、ai AI 员工、staff 真人处理人。
type AgentEvaluationContextSender string

const (
	AgentEvaluationContextSenderCustomer AgentEvaluationContextSender = "customer"
	AgentEvaluationContextSenderAI       AgentEvaluationContextSender = "ai"
	AgentEvaluationContextSenderStaff    AgentEvaluationContextSender = "staff"
)

// AgentEvaluationContextMessage 定义用例前文中的一条消息。
type AgentEvaluationContextMessage struct {
	Sender AgentEvaluationContextSender `json:"sender"`
	Body   string                       `json:"body"`
}

// ServiceIssueEvaluationInput 定义问题会话加入评测时选定的提问消息。
type ServiceIssueEvaluationInput struct {
	QuestionMessageID string `json:"questionMessageId"`
}

// AgentEvaluationCaseInput 定义手动填写的评测用例：期望转人工时标准答案为空。
type AgentEvaluationCaseInput struct {
	Audience       ServiceAudience `json:"audience"`
	Question       string          `json:"question" validate:"notblank" msg:"field.agent_evaluation_question_required"`
	ExpectedAction AgentRunOutcome `json:"expectedAction" validate:"oneof=reply ask_customer resolve handoff" msg:"field.agent_evaluation_expected_action_invalid"`
	ExpectedAnswer string          `json:"expectedAnswer"`
}

// AgentEvaluationCase 定义一条评测用例，Version 每次修改加一；Messages 是从服务周期加入时保存的前文，手动用例为空数组。
type AgentEvaluationCase struct {
	ID             string                          `json:"id"`
	AgentID        string                          `json:"agentId"`
	Source         AgentEvaluationCaseSource       `json:"source"`
	Messages       []AgentEvaluationContextMessage `json:"messages"`
	Version        int                             `json:"version"`
	Audience       ServiceAudience                 `json:"audience"`
	Question       string                          `json:"question"`
	ExpectedAction AgentRunOutcome                 `json:"expectedAction"`
	ExpectedAnswer string                          `json:"expectedAnswer"`
	CreatedAt      time.Time                       `json:"createdAt"`
	UpdatedAt      time.Time                       `json:"updatedAt"`
}

// AgentEvaluationRunSummary 定义一次评测运行的进度与首次尝试计数，Finished 是已结束的首次尝试数。
type AgentEvaluationRunSummary struct {
	ID          string                   `json:"id"`
	Status      AgentEvaluationRunStatus `json:"status"`
	CaseCount   int                      `json:"caseCount"`
	Finished    int                      `json:"finished"`
	Passed      int                      `json:"passed"`
	Failed      int                      `json:"failed"`
	Errors      int                      `json:"errors"`
	CreatedAt   time.Time                `json:"createdAt"`
	CompletedAt *time.Time               `json:"completedAt"`
}

// AgentEvaluationCaseRow 定义评测页的一条用例：LatestStatus 是它在最近一次运行中首次尝试的结果，未参与时为空；RerunStatus 是该运行中最近一次单条重跑的结果，没有重跑时为空；Modified 表示用例在最近一次运行后已修改；NewFailure 表示同一用例版本由上次通过变为本次未通过。
type AgentEvaluationCaseRow struct {
	AgentEvaluationCase
	LatestStatus *AgentEvaluationResultStatus `json:"latestStatus"`
	RerunStatus  *AgentEvaluationResultStatus `json:"rerunStatus"`
	Modified     bool                         `json:"modified"`
	NewFailure   bool                         `json:"newFailure"`
}

// AgentEvaluation 定义 AI 员工评测页的数据：最近两次运行、配置是否已在最近一次运行后变更、判断模型是否可用与全部用例。
type AgentEvaluation struct {
	Latest               *AgentEvaluationRunSummary `json:"latest"`
	Previous             *AgentEvaluationRunSummary `json:"previous"`
	ConfigurationChanged bool                       `json:"configurationChanged"`
	DecisionModelReady   bool                       `json:"decisionModelReady"`
	Cases                []AgentEvaluationCaseRow   `json:"cases"`
}

// AgentEvaluationSnapshot 定义运行发起时冻结的用例内容。
type AgentEvaluationSnapshot struct {
	Audience       ServiceAudience                 `json:"audience"`
	Messages       []AgentEvaluationContextMessage `json:"messages"`
	Question       string                          `json:"question"`
	ExpectedAction AgentRunOutcome                 `json:"expectedAction"`
	ExpectedAnswer string                          `json:"expectedAnswer"`
}

// AgentEvaluationAttempt 定义用例在一次运行中的一次尝试，Attempt 为 1 的是随运行发起的首次尝试；Snapshot 是运行发起时冻结的用例内容，CaseVersion 是其版本号。
type AgentEvaluationAttempt struct {
	ID                 string                      `json:"id"`
	Attempt            int                         `json:"attempt"`
	CaseVersion        int                         `json:"caseVersion"`
	Snapshot           AgentEvaluationSnapshot     `json:"snapshot"`
	Status             AgentEvaluationResultStatus `json:"status"`
	ActualAction       *AgentRunOutcome            `json:"actualAction"`
	ActualReason       *AgentHandoffReason         `json:"actualReason"`
	Answer             string                      `json:"answer"`
	Blocks             []AgentRunContentBlock      `json:"blocks"`
	InputTokens        int                         `json:"inputTokens"`
	OutputTokens       int                         `json:"outputTokens"`
	CorrectProbability *float64                    `json:"correctProbability"`
	ErrorCode          *AgentEvaluationErrorCode   `json:"errorCode"`
	CreatedAt          time.Time                   `json:"createdAt"`
	CompletedAt        *time.Time                  `json:"completedAt"`
}

// AgentEvaluationCaseDetail 定义用例的当前内容与它在最近一次运行中的全部尝试，按尝试序号排列。
type AgentEvaluationCaseDetail struct {
	Case     AgentEvaluationCase      `json:"case"`
	Attempts []AgentEvaluationAttempt `json:"attempts"`
}
