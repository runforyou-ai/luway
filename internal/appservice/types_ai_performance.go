package appservice

import "time"

// AIPerformanceReportInput 定义 AI 表现报表的统计范围：最近 Days 天内结束、由 AI 员工接待过的会话，ChannelID 为空表示全部渠道；AgentID 限定周期的接待 AI 员工，Mine 限定为当前成员负责的 AI 员工。
type AIPerformanceReportInput struct {
	Days      int    `json:"days" query:"days,default=30"`
	ChannelID string `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	AgentID   string `json:"agentId" query:"agentId" validate:"omitempty,uuid" msg:"error.agent_not_found"`
	Mine      bool   `json:"mine" query:"mine"`
}

// AIPerformanceSummary 定义统计范围内 AI 员工接待过的已关闭周期的计数，排除小结状态为无实质诉求的周期：Resolved 与 Unresolved 按小结的是否解决计数，其余为未判定；AIOnly 为 AI 员工独立处理并关闭的周期数，AIResolved 与 AIUnresolved 为其中的已解决与未解决数；HandedOff 为发生过转人工的周期数，CloseAIResolved 等为按结束方式的周期数；Satisfied、Neutral 与 Dissatisfied 按推断满意度计数，其余为未判定；AIIncorrect 等为质检标记成立的周期数，对应的 Reviewed 为该项已质检且适用的周期数。
type AIPerformanceSummary struct {
	Closed                  int `json:"closed"`
	Resolved                int `json:"resolved"`
	Unresolved              int `json:"unresolved"`
	AIOnly                  int `json:"aiOnly"`
	AIResolved              int `json:"aiResolved"`
	AIUnresolved            int `json:"aiUnresolved"`
	HandedOff               int `json:"handedOff"`
	CloseAIResolved         int `json:"closeAiResolved"`
	CustomerUnresponsive    int `json:"customerUnresponsive"`
	Manual                  int `json:"manual"`
	Rated                   int `json:"rated"`
	RatedResolved           int `json:"ratedResolved"`
	Satisfied               int `json:"satisfied"`
	Neutral                 int `json:"neutral"`
	Dissatisfied            int `json:"dissatisfied"`
	AIIncorrect             int `json:"aiIncorrect"`
	AIIncorrectReviewed     int `json:"aiIncorrectReviewed"`
	AIMissedHandoff         int `json:"aiMissedHandoff"`
	AIMissedHandoffReviewed int `json:"aiMissedHandoffReviewed"`
	AIPoorAttitude          int `json:"aiPoorAttitude"`
	AIPoorAttitudeReviewed  int `json:"aiPoorAttitudeReviewed"`
}

// AIHandoffReasonCount 定义一种转人工原因在统计范围内出现的次数。
type AIHandoffReasonCount struct {
	Reason AgentHandoffReason `json:"reason"`
	Count  int                `json:"count"`
}

// AIPerformanceReport 定义 AI 表现报表概览：整体计数、转人工原因分布，以及所选渠道和 AI 员工范围下全部待处理的待补知识条数，该条数不受统计天数限制。
type AIPerformanceReport struct {
	Summary           AIPerformanceSummary   `json:"summary"`
	HandoffReasons    []AIHandoffReasonCount `json:"handoffReasons"`
	KnowledgeGapTotal int                    `json:"knowledgeGapTotal"`
}

// AIPerformanceBreakdownInput 定义按维度拆分的统计范围与分页。
type AIPerformanceBreakdownInput struct {
	Days      int                    `json:"days" query:"days,default=30"`
	ChannelID string                 `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	AgentID   string                 `json:"agentId" query:"agentId" validate:"omitempty,uuid" msg:"error.agent_not_found"`
	Mine      bool                   `json:"mine" query:"mine"`
	Dimension ServiceReportDimension `json:"dimension" query:"dimension"`
	Page      int                    `json:"page" query:"page,default=1"`
	PageSize  int                    `json:"pageSize" query:"pageSize,default=50"`
}

// AIPerformanceBreakdown 定义按渠道或咨询分类拆分的已关闭周期数、已解决数与 AI 独立解决数；ID 为空表示未分类。
type AIPerformanceBreakdown struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Closed     int    `json:"closed"`
	Resolved   int    `json:"resolved"`
	AIResolved int    `json:"aiResolved"`
}

// AIPerformanceBreakdownList 定义一页拆分结果。
type AIPerformanceBreakdownList struct {
	Rows []AIPerformanceBreakdown `json:"rows"`
	Page PageInfo                 `json:"page"`
}

// AIPerformanceIssueListInput 定义问题会话的统计范围、问题类型与分页。
type AIPerformanceIssueListInput struct {
	Days      int              `json:"days" query:"days,default=30"`
	ChannelID string           `json:"channelId" query:"channelId" validate:"omitempty,uuid" msg:"error.channel_not_found"`
	AgentID   string           `json:"agentId" query:"agentId" validate:"omitempty,uuid" msg:"error.agent_not_found"`
	Mine      bool             `json:"mine" query:"mine"`
	Issue     ServiceIssueType `json:"issue" query:"issue,default=all"`
	Page      int              `json:"page" query:"page,default=1"`
	PageSize  int              `json:"pageSize" query:"pageSize,default=50"`
}

// AgentServiceSessionListInput 定义 AI 员工服务记录的分页。
type AgentServiceSessionListInput struct {
	Page     int `json:"page" query:"page,default=1"`
	PageSize int `json:"pageSize" query:"pageSize,default=50"`
}

// AgentServiceSession 定义 AI 员工接待的一个服务周期，即该 AI 员工在周期开启时或之后首次负责该周期；Preview 为周期首条消息摘要，Summary 只在小结已生成时有值，渠道字段只在渠道来源时有值。
type AgentServiceSession struct {
	ServiceSessionID       string                     `json:"serviceSessionId"`
	ConversationID         string                     `json:"conversationId"`
	OpeningMessageID       string                     `json:"openingMessageId"`
	Source                 ServiceSource              `json:"source"`
	Audience               ServiceAudience            `json:"audience"`
	ChannelType            *ChannelType               `json:"channelType"`
	ChannelName            *string                    `json:"channelName"`
	RequesterName          string                     `json:"requesterName"`
	RequesterContactNumber *int64                     `json:"requesterContactNumber"`
	RequesterAvatarURL     string                     `json:"requesterAvatarUrl"`
	Status                 ServiceSessionStatus       `json:"status"`
	OpenedAt               time.Time                  `json:"openedAt"`
	ClosedAt               *time.Time                 `json:"closedAt"`
	CloseReason            *ServiceSessionCloseReason `json:"closeReason"`
	Preview                string                     `json:"preview"`
	Summary                *string                    `json:"summary"`
	Resolved               *bool                      `json:"resolved"`
}

// AgentServiceSessionList 定义一页 AI 员工服务记录，按开启时间倒序排列。
type AgentServiceSessionList struct {
	Sessions []AgentServiceSession `json:"sessions"`
	Page     PageInfo              `json:"page"`
}
