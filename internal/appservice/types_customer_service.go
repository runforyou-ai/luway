package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// BusinessHoursPeriod 定义一天内的一个工作时段，起止为 HH:mm，结束可取 24:00。
type BusinessHoursPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// BusinessHoursOverride 定义按日期覆盖的工作时段，Periods 为空表示当天休息。
type BusinessHoursOverride struct {
	Date    string                `json:"date"`
	Periods []BusinessHoursPeriod `json:"periods"`
}

// BusinessHours 定义企业客服工作时间；Weekly 固定 7 项，从周一到周日排列。
type BusinessHours struct {
	Enabled   bool                    `json:"enabled"`
	TimeZone  string                  `json:"timeZone"`
	Weekly    [][]BusinessHoursPeriod `json:"weekly"`
	Overrides []BusinessHoursOverride `json:"overrides"`
}

// ServiceTimeouts 定义企业客服的超时时长，单位为分钟：负责人未回复的提醒与回收时长、队列等待提醒时长，以及 AI 负责时客户未回复的跟进与关单时长。
type ServiceTimeouts struct {
	ResponseReminderMinutes int `json:"responseReminderMinutes"`
	ResponseReclaimMinutes  int `json:"responseReclaimMinutes"`
	QueueReminderMinutes    int `json:"queueReminderMinutes"`
	AIFollowUpMinutes       int `json:"aiFollowUpMinutes"`
	AICloseMinutes          int `json:"aiCloseMinutes"`
}

// AIModelReference 指向模型服务中的一个模型。
type AIModelReference struct {
	ProviderID      string `json:"providerId"`
	ModelIdentifier string `json:"modelIdentifier"`
}

// ServiceSummarySettings 定义周期小结使用的判断模型、小结模型与小结语言；模型为空表示不使用。
type ServiceSummarySettings struct {
	Decision *AIModelReference `json:"decision"`
	Summary  *AIModelReference `json:"summary"`
	Locale   Locale            `json:"locale"`
}

// TranslationSettings 定义企业翻译客户会话消息使用的模型，为空时不提供翻译。
type TranslationSettings struct {
	Model *AIModelReference `json:"model"`
}

// ServiceSummaryStatus 表示服务周期小结的生成状态。
type ServiceSummaryStatus string

const (
	ServiceSummaryPending   ServiceSummaryStatus = ServiceSummaryStatus(domain.ServiceSessionSummaryPending)
	ServiceSummaryReady     ServiceSummaryStatus = ServiceSummaryStatus(domain.ServiceSessionSummaryReady)
	ServiceSummaryNoRequest ServiceSummaryStatus = ServiceSummaryStatus(domain.ServiceSessionSummaryNoRequest)
	ServiceSummaryFailed    ServiceSummaryStatus = ServiceSummaryStatus(domain.ServiceSessionSummaryFailed)
)

// HandoffSummary 定义 AI 转人工时交给承接客服的摘要：客户诉求、AI 已完成的处理与需要人工处理的卡点；MessageID 为对应的转人工事件消息编号。
type HandoffSummary struct {
	Request   string `json:"request"`
	Progress  string `json:"progress"`
	Blocker   string `json:"blocker"`
	MessageID string `json:"messageId"`
}

// ServiceSessionSummary 定义一个已关闭服务周期的结束方式与小结；Status 为空表示不生成小结，EditedBy 非空表示由客服修改。
type ServiceSessionSummary struct {
	ServiceSessionID string                    `json:"serviceSessionId"`
	ConversationID   string                    `json:"conversationId"`
	Source           ServiceSource             `json:"source"`
	ChannelType      *ChannelType              `json:"channelType"`
	ChannelName      *string                   `json:"channelName"`
	ClosedAt         time.Time                 `json:"closedAt"`
	CloseReason      ServiceSessionCloseReason `json:"closeReason"`
	Status           *ServiceSummaryStatus     `json:"status"`
	Summary          *string                   `json:"summary"`
	Resolved         *bool                     `json:"resolved"`
	CategoryID       *string                   `json:"categoryId"`
	CategoryName     *string                   `json:"categoryName"`
	EditedAt         *time.Time                `json:"editedAt"`
	EditedBy         *string                   `json:"editedBy"`
}

// ServiceSummaries 定义服务会话当前开放周期的交接摘要与同一发起人已关闭周期的小结，小结按关闭时间从新到旧排列。
type ServiceSummaries struct {
	Handoff  *HandoffSummary         `json:"handoff"`
	Sessions []ServiceSessionSummary `json:"sessions"`
}

// ServiceSessionSummaryInput 定义客服修改的小结、是否解决与咨询分类；Resolved 与 CategoryID 为空表示不标注。
type ServiceSessionSummaryInput struct {
	Summary    string  `json:"summary"`
	Resolved   *bool   `json:"resolved"`
	CategoryID *string `json:"categoryId"`
}

// ServiceCategoryInput 定义咨询分类可编辑字段；TeamID 为空表示转人工时按渠道失败路由。
type ServiceCategoryInput struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	TeamID      *string `json:"teamId"`
}

// ServiceCategory 定义咨询分类及其承接团队。
type ServiceCategory struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Team        *TeamSummary `json:"team"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

// ServiceCategoryList 定义企业咨询分类目录。
type ServiceCategoryList struct {
	Categories []ServiceCategory `json:"categories"`
}

// CustomerIdentitySecret 定义企业客户身份密钥，未生成时 Secret 为空。
type CustomerIdentitySecret struct {
	Secret string `json:"secret"`
}

// RequesterProfile 定义服务会话发起人的资料，按发起人类别给出对应分支。
type RequesterProfile struct {
	Customer *CustomerProfile `json:"customer"`
}

// CustomerProfile 定义客户会话的联系人、客户身份与当前周期访客上下文；未验证身份时企业用户编号为空。
type CustomerProfile struct {
	ContactID        string         `json:"contactId"`
	IdentityVerified bool           `json:"identityVerified"`
	ExternalUserID   string         `json:"externalUserId"`
	Email            string         `json:"email"`
	Visit            *CustomerVisit `json:"visit"`
}

// CustomerVisit 定义网站访客在当前周期的来源页、当前页与浏览器环境，页面地址不含查询串与 hash。
type CustomerVisit struct {
	ReferrerURL string `json:"referrerUrl"`
	PageURL     string `json:"pageUrl"`
	PageTitle   string `json:"pageTitle"`
	Browser     string `json:"browser"`
	OS          string `json:"os"`
	DeviceType  string `json:"deviceType"`
	Language    string `json:"language"`
	TimeZone    string `json:"timeZone"`
	Country     string `json:"country"`
}

// ServiceBusinessQuery 定义 AI 客服在当前服务周期内调用业务查询工具的一次记录。
type ServiceBusinessQuery struct {
	ID        string              `json:"id"`
	MCPServer string              `json:"mcpServer"`
	ToolName  string              `json:"toolName"`
	Arguments string              `json:"arguments"`
	Result    *string             `json:"result"`
	Error     *string             `json:"error"`
	Status    AgentToolCallStatus `json:"status"`
	Evidence  bool                `json:"evidence"`
	CalledAt  time.Time           `json:"calledAt"`
}

// ServiceBusinessQueryList 定义服务会话当前服务周期的业务查询记录，按调用时间倒序。
type ServiceBusinessQueryList struct {
	Queries []ServiceBusinessQuery `json:"queries"`
}

// ServiceTranscriptSender 定义客服周期沟通记录中的发送方。
type ServiceTranscriptSender string

const (
	ServiceTranscriptSenderCustomer ServiceTranscriptSender = "customer"
	ServiceTranscriptSenderAI       ServiceTranscriptSender = "ai"
	ServiceTranscriptSenderStaff    ServiceTranscriptSender = "staff"
)

// ServiceTranscriptMessage 定义客服周期沟通记录中的一条对客消息，客户的 SenderName 为空，Type 为文字或附件。
type ServiceTranscriptMessage struct {
	ID         string                  `json:"id"`
	Type       MessageType             `json:"type"`
	Sender     ServiceTranscriptSender `json:"sender"`
	SenderName string                  `json:"senderName"`
	Body       string                  `json:"body"`
	CreatedAt  time.Time               `json:"createdAt"`
}
