package domain

// ConversationSystemEventType 定义会话时间线中的系统事件类型。
type ConversationSystemEventType string

const (
	ConversationSystemEventGroupRenamed          ConversationSystemEventType = "group_renamed"
	ConversationSystemEventGroupMembersAdded     ConversationSystemEventType = "group_members_added"
	ConversationSystemEventGroupMemberRemoved    ConversationSystemEventType = "group_member_removed"
	ConversationSystemEventGroupMemberLeft       ConversationSystemEventType = "group_member_left"
	ConversationSystemEventGroupOwnerTransferred ConversationSystemEventType = "group_owner_transferred"
	ConversationSystemEventGroupDissolved        ConversationSystemEventType = "group_dissolved"
	// 表示 AI 员工把服务周期转交人工；客户会话事件统一使用 service_session_* 前缀。
	ConversationSystemEventServiceSessionHandedOff ConversationSystemEventType = "service_session_handed_off"
	// 以下事件表示成员领取、接管、转交、关闭与重开客服处理周期。
	ConversationSystemEventServiceSessionClaimed     ConversationSystemEventType = "service_session_claimed"
	ConversationSystemEventServiceSessionTakenOver   ConversationSystemEventType = "service_session_taken_over"
	ConversationSystemEventServiceSessionTransferred ConversationSystemEventType = "service_session_transferred"
	ConversationSystemEventServiceSessionClosed      ConversationSystemEventType = "service_session_closed"
	ConversationSystemEventServiceSessionReopened    ConversationSystemEventType = "service_session_reopened"
	// 表示失去接待资格的负责人所负责的服务周期退回队列。
	ConversationSystemEventServiceSessionReturned ConversationSystemEventType = "service_session_returned"
	// 表示队列中的服务周期自动分配给成员。
	ConversationSystemEventServiceSessionAssigned ConversationSystemEventType = "service_session_assigned"
	// 表示访客评价了已关闭的服务周期。
	ConversationSystemEventServiceSessionRated ConversationSystemEventType = "service_session_rated"
	// 表示访客留下了接收回复的邮箱。
	ConversationSystemEventServiceSessionEmailCollected ConversationSystemEventType = "service_session_email_collected"
	// 表示客服回复已通过邮件通知访客。
	ConversationSystemEventServiceSessionEmailNotified ConversationSystemEventType = "service_session_email_notified"
	// 表示企业成员发起人看到的服务进度变化。
	ConversationSystemEventServiceStatusChanged ConversationSystemEventType = "service_status_changed"
	// AI 员工提交了需要确认或审批的操作，以及该操作有了结果。
	ConversationSystemEventAgentToolCallPending ConversationSystemEventType = "agent_tool_call_pending"
	// 需要确认或审批的工具调用有了结果，作为事件输入唤醒提交它的 AI 员工。
	ConversationSystemEventAgentToolCallResolved ConversationSystemEventType = "agent_tool_call_resolved"
)

// AgentToolCallEvent 是 agent_tool_call_pending 与 agent_tool_call_resolved 事件的结构化内容：提交调用的 AI 员工快照与工具调用编号。
type AgentToolCallEvent struct {
	Actor      ConversationEventParticipant `json:"actor"`
	ToolCallID string                       `json:"toolCallId"`
}

// ConversationEventParticipant 是系统事件中成员的编号与名称快照。
type ConversationEventParticipant struct {
	IdentityID  string `json:"identityId"`
	DisplayName string `json:"displayName"`
}

// ServiceRequestStatus 定义发起人看到的服务进度：handed_off 已转交队列或团队，processing 由成员处理中，closed 本次服务已结束。
type ServiceRequestStatus string

const (
	ServiceRequestStatusHandedOff  ServiceRequestStatus = "handed_off"
	ServiceRequestStatusProcessing ServiceRequestStatus = "processing"
	ServiceRequestStatusClosed     ServiceRequestStatus = "closed"
)

// ServiceStatusChangedEvent 是 service_status_changed 事件的结构化内容：转交与处理中带去向快照，结束带结束方式。
type ServiceStatusChangedEvent struct {
	ServiceSessionID string                     `json:"serviceSessionId"`
	Status           ServiceRequestStatus       `json:"status"`
	Target           *ServiceSessionTarget      `json:"target,omitempty"`
	CloseReason      *ServiceSessionCloseReason `json:"closeReason,omitempty"`
}

// ServiceSessionReturnReason 定义客服处理周期退回队列的原因。
type ServiceSessionReturnReason string

const (
	ServiceSessionReturnAssigneeUnavailable ServiceSessionReturnReason = "assignee_unavailable"
	ServiceSessionReturnResponseTimeout     ServiceSessionReturnReason = "response_timeout"
)

// ServiceSessionTargetKind 定义客服处理周期流转去向的类型。
type ServiceSessionTargetKind string

const (
	ServiceSessionTargetPublicQueue ServiceSessionTargetKind = "public_queue"
	ServiceSessionTargetTeam        ServiceSessionTargetKind = "team"
	ServiceSessionTargetMember      ServiceSessionTargetKind = "member"
)

// ServiceSessionTarget 记录客服处理周期流转的去向及名称快照。
type ServiceSessionTarget struct {
	Kind        ServiceSessionTargetKind `json:"kind"`
	TeamID      *string                  `json:"teamId,omitempty"`
	TeamName    *string                  `json:"teamName,omitempty"`
	IdentityID  *string                  `json:"identityId,omitempty"`
	DisplayName *string                  `json:"displayName,omitempty"`
}

// ServiceSessionHandedOffEvent 是 service_session_handed_off 事件的结构化内容；ReasonText 只向成员展示。
type ServiceSessionHandedOffEvent struct {
	ServiceSessionID string               `json:"serviceSessionId"`
	FromIdentityID   string               `json:"fromIdentityId"`
	FromDisplayName  string               `json:"fromDisplayName"`
	Target           ServiceSessionTarget `json:"target"`
	Reason           AgentHandoffReason   `json:"reason"`
	ReasonText       string               `json:"reasonText"`
	CategoryName     *string              `json:"categoryName,omitempty"` // 转人工时 AI 选择的咨询分类名称快照。
	AgentRunID       *string              `json:"agentRunId"`
}

// ServiceSessionOperatedEvent 是领取、接管、转交、关闭、重开客服处理周期事件的结构化内容：操作人、原负责人、转交目标，关闭事件另带结束方式。
type ServiceSessionOperatedEvent struct {
	ServiceSessionID string                     `json:"serviceSessionId"`
	ActorIdentityID  string                     `json:"actorIdentityId"`
	ActorDisplayName string                     `json:"actorDisplayName"`
	FromIdentityID   *string                    `json:"fromIdentityId,omitempty"`
	FromDisplayName  *string                    `json:"fromDisplayName,omitempty"`
	Target           *ServiceSessionTarget      `json:"target,omitempty"`
	CloseReason      *ServiceSessionCloseReason `json:"closeReason,omitempty"`
}

// ServiceSessionReturnedEvent 是 service_session_returned 事件的结构化内容：原负责人、退回的队列与原因。
type ServiceSessionReturnedEvent struct {
	ServiceSessionID string                     `json:"serviceSessionId"`
	FromIdentityID   string                     `json:"fromIdentityId"`
	FromDisplayName  string                     `json:"fromDisplayName"`
	Target           ServiceSessionTarget       `json:"target"`
	Reason           ServiceSessionReturnReason `json:"returnReason"`
}

// ServiceSessionAssignedEvent 是 service_session_assigned 事件的结构化内容：自动分配的承接成员与来源队列，没有操作人。
type ServiceSessionAssignedEvent struct {
	ServiceSessionID string               `json:"serviceSessionId"`
	Target           ServiceSessionTarget `json:"target"`
	Source           ServiceSessionTarget `json:"source"`
}

// ServiceSessionEmailEvent 是 service_session_email_collected 与 service_session_email_notified 事件的结构化内容：接收回复的邮箱。
type ServiceSessionEmailEvent struct {
	ServiceSessionID string `json:"serviceSessionId"`
	Email            string `json:"email"`
}

// ServiceSessionRatedEvent 是 service_session_rated 事件的结构化内容：访客对已关闭客服处理周期的是否解决与评语。
type ServiceSessionRatedEvent struct {
	ServiceSessionID string `json:"serviceSessionId"`
	Resolved         bool   `json:"resolved"`
	Comment          string `json:"comment"`
}
