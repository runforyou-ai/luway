package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AgentRunBlockKind 定义思考区域中的内容类型。
type AgentRunBlockKind = domain.AgentRunBlockKind

// AgentToolCallStatus 定义工具执行状态。
type AgentToolCallStatus = domain.AgentToolCallStatus

// ToolIntervention 定义工具调用执行前需要的人工介入：confirmation 由发起人确认，approval 由 AI 员工负责人审批。
type ToolIntervention string

const (
	ToolInterventionConfirmation ToolIntervention = ToolIntervention(domain.ToolInterventionConfirmation)
	ToolInterventionApproval     ToolIntervention = ToolIntervention(domain.ToolInterventionApproval)
)

// NotificationView 定义打开会话的视图：服务会话在客服收件箱打开，其余会话按类型在消息中打开。
type NotificationView = domain.NotificationView

// AgentToolDecision 定义一次需要确认、审批或核对的 AI 员工操作的当前内容、当前成员可执行的处理与打开所在会话的位置。
type AgentToolDecision struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	BusinessSystemName *string `json:"businessSystemName"`
	// ComputerName 是电脑工具调用所派发电脑的名称，其他调用为空；LocalAgent 与 Permission 是本机 Agent 权限请求所属的本机 Agent 名称与请求内容，其他调用为空。
	ComputerName *string                    `json:"computerName"`
	LocalAgent   *string                    `json:"localAgent"`
	Permission   *AgentLocalAgentPermission `json:"permission"`
	Level        *OperationLevel            `json:"level"`
	// Intervention 是执行前需要的人工介入，自动执行的调用中断待核对时为空。
	Intervention *ToolIntervention `json:"intervention"`
	// Arguments 是 AI 员工给出的参数，不含服务端按可信上下文填入的参数；ArgumentTitles 是参数定义中声明了标题的参数名到标题。
	Arguments       string              `json:"arguments"`
	ArgumentTitles  map[string]string   `json:"argumentTitles"`
	Status          AgentToolCallStatus `json:"status"`
	Result          *string             `json:"result"` // 执行成功时的实际结果，其余状态为空。
	Error           *string             `json:"error"`
	AgentIdentityID string              `json:"agentIdentityId"`
	AgentName       string              `json:"agentName"`
	AssigneeName    *string             `json:"assigneeName"`
	DecidedByName   *string             `json:"decidedByName"`
	CreatedAt       time.Time           `json:"createdAt"`
	ExpiresAt       *time.Time          `json:"expiresAt"`
	DecidedAt       *time.Time          `json:"decidedAt"`
	// CanDecide 表示当前成员可以确认、批准或拒绝，CanReview 表示当前成员可以标记已核对。
	CanDecide bool `json:"canDecide"`
	CanReview bool `json:"canReview"`
	// ConversationID 与 View 是打开操作所在会话的位置，Copilot 线程中的操作打开其所属的服务会话；ConversationReadable 表示当前成员可以阅读该会话，负责人不一定能阅读成员与 AI 员工的私聊。
	ConversationID       string           `json:"conversationId"`
	View                 NotificationView `json:"view"`
	ConversationReadable bool             `json:"conversationReadable"`
}

// AgentToolDecisionList 定义待当前成员处理的 AI 员工操作，按提交时间倒序排列。
type AgentToolDecisionList struct {
	Items []AgentToolDecision `json:"items"`
}

// AgentToolDecisionInput 定义成员对 AI 员工操作的裁决：Approve 为 true 表示确认或批准，否则拒绝。
type AgentToolDecisionInput struct {
	Approve bool `json:"approve"`
}

// AgentPlanTaskStatus 定义任务清单中一项任务的状态。
type AgentPlanTaskStatus = domain.AgentPlanTaskStatus

// AgentRunOutcome 定义 Agent 运行的结束方式。
type AgentRunOutcome = domain.AgentRunOutcome

// AgentHandoffReason 定义 AI 客服转交人工的原因。
type AgentHandoffReason = domain.AgentHandoffReason

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

// AgentToolCall 定义完整工具参数、结果和错误：Arguments 是模型给出的参数，BoundArguments 是服务端按可信上下文填入的参数，没有时为空。
type AgentToolCall struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Arguments      string              `json:"arguments"`
	BoundArguments map[string]string   `json:"boundArguments"`
	Result         *string             `json:"result"`
	Error          *string             `json:"error"`
	Status         AgentToolCallStatus `json:"status"`
}

// ConversationAgentRun 定义消息窗口中尚未由结果消息表达的运行状态，取消运行携带自身过程引用。
type ConversationAgentRun struct {
	AgentName string `json:"agentName"`
	// AgentPersonalResponsibleName 是执行者为个人 AI 员工时其负责人的名称，其他 AI 员工为空。
	AgentPersonalResponsibleName *string                   `json:"agentPersonalResponsibleName"`
	AgentAvatarURL               string                    `json:"agentAvatarUrl"`
	ID                           string                    `json:"id"`
	AgentIdentityID              string                    `json:"agentIdentityId"`
	Status                       AgentRunStatus            `json:"status"`
	ErrorCode                    *string                   `json:"errorCode"`
	LastError                    *string                   `json:"lastError"`
	Process                      *ConversationAgentProcess `json:"process"`
}

// AgentLocalAgentPermission 定义本机 Agent 执行一个步骤前请求的权限。
type AgentLocalAgentPermission struct {
	Step AgentToolCallStep `json:"step"`
}

// AgentToolCallProcessInput 定义读取工具调用过程的起点：只返回序号不小于 From 的更新，序号从 1 开始。
type AgentToolCallProcessInput struct {
	From int `json:"from" query:"from,default=1"`
}

// AgentToolCallProcess 定义电脑执行的工具调用的当前状态、错误与过程更新；LocalAgent 是委派的本机 Agent 名称，其他调用为空。
type AgentToolCallProcess struct {
	Status     AgentToolCallStatus   `json:"status"`
	Error      *string               `json:"error"`
	LocalAgent *string               `json:"localAgent"`
	Updates    []AgentToolCallUpdate `json:"updates"`
}

// AgentToolCallUpdateKind 定义过程更新的类型。
type AgentToolCallUpdateKind string

const (
	AgentToolCallUpdateMessage AgentToolCallUpdateKind = AgentToolCallUpdateKind(domain.ToolCallUpdateMessage)
	AgentToolCallUpdateThought AgentToolCallUpdateKind = AgentToolCallUpdateKind(domain.ToolCallUpdateThought)
	AgentToolCallUpdateStep    AgentToolCallUpdateKind = AgentToolCallUpdateKind(domain.ToolCallUpdateStep)
	AgentToolCallUpdatePlan    AgentToolCallUpdateKind = AgentToolCallUpdateKind(domain.ToolCallUpdatePlan)
)

// AgentToolCallUpdate 定义一条过程更新：回复或思考的文本片段按序号拼接，同一步骤的更新整体替换此前的状态，任务清单整体替换此前的清单。
type AgentToolCallUpdate struct {
	Seq  int                     `json:"seq"`
	Kind AgentToolCallUpdateKind `json:"kind"`
	Text string                  `json:"text"`
	Step *AgentToolCallStep      `json:"step"`
	Plan []AgentPlanStep         `json:"plan"`
}

// AgentToolCallStep 定义本机 Agent 执行的一个步骤；Kind 与 Status 取 ACP 的工具类型与状态。
type AgentToolCallStep struct {
	ID        string                     `json:"id"`
	Title     string                     `json:"title"`
	Kind      string                     `json:"kind"`
	Status    string                     `json:"status"`
	Locations []string                   `json:"locations"`
	Content   []AgentToolCallStepContent `json:"content"`
}

// AgentToolCallStepContent 定义步骤产出的一段内容：文本或文件差异。
type AgentToolCallStepContent struct {
	Text string         `json:"text"`
	Diff *AgentFileDiff `json:"diff"`
}

// AgentFileDiff 定义本机 Agent 对一个文件的改动，OldText 为空表示新建文件。
type AgentFileDiff struct {
	Path    string  `json:"path"`
	OldText *string `json:"oldText"`
	NewText string  `json:"newText"`
}

// AgentPlanStep 定义任务清单中的一项，Status 取 pending、in_progress、completed。
type AgentPlanStep struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}
