package domain

// ServiceSessionStatus 定义客服处理状态。
type ServiceSessionStatus string

const (
	ServiceSessionStatusOpen   ServiceSessionStatus = "open"
	ServiceSessionStatusClosed ServiceSessionStatus = "closed"
)

// ServiceAudience 定义服务对象：customer 为外部客户，employee 为本企业员工，partner 为伙伴，personal 为仅负责人本人，服务对象为 personal 的 AI 员工称为个人 AI 员工。
type ServiceAudience string

const (
	ServiceAudienceCustomer ServiceAudience = "customer"
	ServiceAudienceEmployee ServiceAudience = "employee"
	ServiceAudiencePartner  ServiceAudience = "partner"
	ServiceAudiencePersonal ServiceAudience = "personal"
)

// AgentServiceAudiences 是服务客户或员工的 AI 员工可组合选择的服务对象，按展示顺序排列；personal 只能单独使用且创建后不可更改。
var AgentServiceAudiences = []ServiceAudience{ServiceAudienceCustomer, ServiceAudienceEmployee}

// ServiceSource 定义服务会话来源：channel 为渠道，direct 为单聊。
type ServiceSource string

const (
	ServiceSourceChannel ServiceSource = "channel"
	ServiceSourceDirect  ServiceSource = "direct"
)

// ServiceSessionCloseReason 定义服务周期的结束方式。
type ServiceSessionCloseReason string

const (
	// ServiceSessionCloseAIResolved 表示客户确认问题已解决后由 AI 负责人关闭。
	ServiceSessionCloseAIResolved ServiceSessionCloseReason = "ai_resolved"
	// ServiceSessionCloseCustomerUnresponsive 表示 AI 跟进或请求确认后客户超时未回复而关闭。
	ServiceSessionCloseCustomerUnresponsive ServiceSessionCloseReason = "customer_unresponsive"
	// ServiceSessionCloseManual 表示由客服人工关闭。
	ServiceSessionCloseManual ServiceSessionCloseReason = "manual"
)

// DefaultMaxServiceSessions 是成员默认的最大接待量。
const DefaultMaxServiceSessions = 10

// ServiceAttentionReason 定义提醒成员处理服务周期的原因。
type ServiceAttentionReason string

const (
	// ServiceAttentionAssigned 提醒成员处理新分配给本人的周期。
	ServiceAttentionAssigned ServiceAttentionReason = "assigned"
	// ServiceAttentionResponseOverdue 提醒负责人客户等待回复已超时。
	ServiceAttentionResponseOverdue ServiceAttentionReason = "response_overdue"
	// ServiceAttentionQueueWaiting 提醒队列对应的客服有周期等待超时。
	ServiceAttentionQueueWaiting ServiceAttentionReason = "queue_waiting"
	// ServiceAttentionReturned 告知原负责人其超时未回复的周期已退回队列。
	ServiceAttentionReturned ServiceAttentionReason = "returned"
)

// ServiceSessionSummaryStatus 定义服务周期小结的生成状态。
type ServiceSessionSummaryStatus string

const (
	// ServiceSessionSummaryPending 表示周期已关闭，等待生成小结。
	ServiceSessionSummaryPending ServiceSessionSummaryStatus = "pending"
	// ServiceSessionSummaryReady 表示小结已生成或由客服填写。
	ServiceSessionSummaryReady ServiceSessionSummaryStatus = "ready"
	// ServiceSessionSummaryNoRequest 表示客户在本周期没有提出需要处理的问题或诉求。
	ServiceSessionSummaryNoRequest ServiceSessionSummaryStatus = "no_request"
	// ServiceSessionSummaryFailed 表示小结生成失败。
	ServiceSessionSummaryFailed ServiceSessionSummaryStatus = "failed"
)

// ServiceSessionSatisfaction 定义判断模型推断的客户满意度。
type ServiceSessionSatisfaction string

const (
	// ServiceSessionSatisfactionSatisfied 表示客户满意。
	ServiceSessionSatisfactionSatisfied ServiceSessionSatisfaction = "satisfied"
	// ServiceSessionSatisfactionNeutral 表示客户态度一般。
	ServiceSessionSatisfactionNeutral ServiceSessionSatisfaction = "neutral"
	// ServiceSessionSatisfactionDissatisfied 表示客户不满意。
	ServiceSessionSatisfactionDissatisfied ServiceSessionSatisfaction = "dissatisfied"
)

// HandoffSummary 是 AI 转人工时交给承接客服的摘要。
type HandoffSummary struct {
	Request  string `json:"request"`
	Progress string `json:"progress"`
	Blocker  string `json:"blocker"`
}
