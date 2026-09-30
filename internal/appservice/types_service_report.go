package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// ServiceReportDimension 定义客服报表的拆分维度。
type ServiceReportDimension string

const (
	ServiceReportDimensionChannel  ServiceReportDimension = ServiceReportDimension(domain.ServiceReportDimensionChannel)
	ServiceReportDimensionCategory ServiceReportDimension = ServiceReportDimension(domain.ServiceReportDimensionCategory)
)

// ServiceIssueType 定义问题会话的筛选类型：AI 表现适用 AI 质检类型，团队表现适用真人质检类型，全部与不满意两者都适用。
type ServiceIssueType string

const (
	ServiceIssueTypeAll               ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeAll)
	ServiceIssueTypeDissatisfied      ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeDissatisfied)
	ServiceIssueTypeAIIncorrect       ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeAIIncorrect)
	ServiceIssueTypeAIMissedHandoff   ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeAIMissedHandoff)
	ServiceIssueTypeAIPoorAttitude    ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeAIPoorAttitude)
	ServiceIssueTypeHumanIncorrect    ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeHumanIncorrect)
	ServiceIssueTypeHumanPoorAttitude ServiceIssueType = ServiceIssueType(domain.ServiceIssueTypeHumanPoorAttitude)
)

// ServiceSessionSatisfaction 定义判断模型推断的客户满意度。
type ServiceSessionSatisfaction string

const (
	ServiceSessionSatisfactionSatisfied    ServiceSessionSatisfaction = ServiceSessionSatisfaction(domain.ServiceSessionSatisfactionSatisfied)
	ServiceSessionSatisfactionNeutral      ServiceSessionSatisfaction = ServiceSessionSatisfaction(domain.ServiceSessionSatisfactionNeutral)
	ServiceSessionSatisfactionDissatisfied ServiceSessionSatisfaction = ServiceSessionSatisfaction(domain.ServiceSessionSatisfactionDissatisfied)
)

// ServiceIssue 定义一个问题会话：推断满意度为不满意或任一质检标记成立的已关闭周期；OpeningMessageID 为周期首条消息，Summary 只在小结已生成时有值，Preview 为周期首条消息摘要，渠道字段只在渠道来源时有值。
type ServiceIssue struct {
	ServiceSessionID       string                      `json:"serviceSessionId"`
	ConversationID         string                      `json:"conversationId"`
	OpeningMessageID       string                      `json:"openingMessageId"`
	ChannelType            *ChannelType                `json:"channelType"`
	ChannelName            *string                     `json:"channelName"`
	RequesterName          string                      `json:"requesterName"`
	RequesterContactNumber *int64                      `json:"requesterContactNumber"`
	RequesterAvatarURL     string                      `json:"requesterAvatarUrl"`
	ClosedAt               time.Time                   `json:"closedAt"`
	Summary                *string                     `json:"summary"`
	Preview                string                      `json:"preview"`
	Satisfaction           *ServiceSessionSatisfaction `json:"satisfaction"`
	AIIncorrect            bool                        `json:"aiIncorrect"`
	AIMissedHandoff        bool                        `json:"aiMissedHandoff"`
	AIPoorAttitude         bool                        `json:"aiPoorAttitude"`
	HumanIncorrect         bool                        `json:"humanIncorrect"`
	HumanPoorAttitude      bool                        `json:"humanPoorAttitude"`
}

// ServiceIssueList 定义一页问题会话，按关闭时间倒序排列。
type ServiceIssueList struct {
	Issues []ServiceIssue `json:"issues"`
	Page   PageInfo       `json:"page"`
}

// ServiceIssueDetail 定义问题会话详情：质检结论与周期内的对客沟通。
type ServiceIssueDetail struct {
	Issue    ServiceIssue               `json:"issue"`
	Messages []ServiceTranscriptMessage `json:"messages"`
}
