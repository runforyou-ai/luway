package appservice

import "context"

// ConversationBackend 定义会话消息、服务周期流转、单聊、群聊、AI 聊天、Copilot 与 Agent 运行过程的业务调用。
type ConversationBackend interface {
	// SendAttachmentMessage 发送已上传的单聊、群聊或 AI 聊天附件消息，首发时创建会话。
	//appservice:route POST /conversation-attachments status=201 perm=none
	SendAttachmentMessage(context.Context, RequestMeta, AttachmentMessageInput) (AttachmentMessageResult, error)
	// GetAttachmentDownload 签发当前成员可见消息附件的下载地址。
	//appservice:route GET /conversations/{conversationID:uuid}/messages/{messageID:uuid}/attachment perm=none
	GetAttachmentDownload(context.Context, RequestMeta, string, string) (FileDownload, error)
	// ListConversationMessages 返回成员可见的会话消息。
	//appservice:route GET /conversations/{conversationID:uuid}/messages perm=none
	ListConversationMessages(context.Context, RequestMeta, string, ConversationMessageListInput) (ConversationMessageList, error)
	// ReadConversationMessageWindow 重读已加载首尾游标之间的完整消息范围。
	//appservice:route GET /conversations/{conversationID:uuid}/message-window perm=none
	ReadConversationMessageWindow(context.Context, RequestMeta, string, ConversationMessageWindowInput) (ConversationMessageList, error)
	// GetConversationMessageContext 返回目标消息及其前后上下文。
	//appservice:route GET /conversations/{conversationID:uuid}/messages/{messageID:uuid}/context perm=none
	GetConversationMessageContext(context.Context, RequestMeta, string, string) (ConversationMessageList, error)
	// GetConversationNavigationState 返回群聊提及进度和最新可见消息。
	//appservice:route GET /conversations/{conversationID:uuid}/navigation perm=none
	GetConversationNavigationState(context.Context, RequestMeta, string) (ConversationNavigationState, error)
	// ListPendingConversationMentions 返回本轮待查看提及目标。
	//appservice:route GET /conversations/{conversationID:uuid}/mentions/pending perm=none
	ListPendingConversationMentions(context.Context, RequestMeta, string) (PendingConversationMentions, error)
	// MarkConversationMentionReviewed 确认已查看的群聊提及。
	//appservice:route POST /conversations/{conversationID:uuid}/mentions/review perm=none
	MarkConversationMentionReviewed(context.Context, RequestMeta, string, MarkConversationMentionReviewedInput) (ConversationMentionReview, error)
	// MarkConversationRead 单调推进当前用户的会话已读水位。
	//appservice:route POST /conversations/{conversationID:uuid}/read perm=none
	MarkConversationRead(context.Context, RequestMeta, string, MarkConversationReadInput) (ConversationReadState, error)
	// ReportConversationTyping 发布当前用户的输入状态：单聊与群聊发给其他真人成员，网站渠道客户会话发给该线程访客。
	//appservice:route POST /conversations/{conversationID:uuid}/typing perm=none
	ReportConversationTyping(context.Context, RequestMeta, string, ConversationTypingInput) error
	// UpdateConversationUnreadMark 保存当前用户独立于阅读水位的未读标记。
	//appservice:route PATCH /conversations/{conversationID:uuid}/unread-mark perm=none
	UpdateConversationUnreadMark(context.Context, RequestMeta, string, ConversationUnreadMarkInput) error
	// UpdateConversationPin 保存当前用户的会话置顶事实与置顶顺序。
	//appservice:route PATCH /conversations/{conversationID:uuid}/pin perm=none
	UpdateConversationPin(context.Context, RequestMeta, string, ConversationPinInput) (ConversationPinState, error)
	// UpdateConversationArchive 保存当前用户对群聊、单聊或 AI 聊天的归档状态。
	//appservice:route PATCH /conversations/{conversationID:uuid}/archive perm=none
	UpdateConversationArchive(context.Context, RequestMeta, string, ConversationArchiveInput) error
	// UpdateConversationNotificationSettings 保存当前用户的原生会话提醒设置。
	//appservice:route PATCH /conversations/{conversationID:uuid}/notification-settings perm=none
	UpdateConversationNotificationSettings(context.Context, RequestMeta, string, ConversationNotificationSettingsInput) (ConversationNotificationSettings, error)
	// SendServiceTextMessage 在服务会话中发送回复或内部备注。
	//appservice:route POST /conversations/{conversationID:uuid}/messages perm=none
	SendServiceTextMessage(context.Context, RequestMeta, string, ServiceTextMessageInput) (ConversationMessage, error)
	// SendServiceAttachmentMessage 在服务会话中发送附件回复。
	//appservice:route POST /conversations/{conversationID:uuid}/attachment-messages perm=none
	SendServiceAttachmentMessage(context.Context, RequestMeta, string, ServiceAttachmentMessageInput) (ConversationMessage, error)
	// ListServiceCopilotThreads 返回服务会话的全部 Copilot 线程。
	//appservice:route GET /conversations/{conversationID:uuid}/copilot-threads perm=none
	ListServiceCopilotThreads(context.Context, RequestMeta, string) (ServiceCopilotThreadList, error)
	// SendFirstServiceCopilotMessage 以首条提问创建服务会话的 Copilot 线程。
	//appservice:route POST /conversations/{conversationID:uuid}/copilot-threads perm=none
	SendFirstServiceCopilotMessage(context.Context, RequestMeta, string, FirstServiceCopilotMessageInput) (FirstServiceCopilotMessageResult, error)
	// SendServiceCopilotTextMessage 向 Copilot 线程发送提问。
	//appservice:route POST /copilot-threads/{threadID:uuid}/messages perm=none
	SendServiceCopilotTextMessage(context.Context, RequestMeta, string, ServiceCopilotTextMessageInput) (ConversationMessage, error)
	// StopServiceCopilotReply 停止 Copilot 线程中指定的回复并返回实际运行状态。
	//appservice:route POST /copilot-threads/{threadID:uuid}/runs/{runID:uuid}/stop perm=none
	StopServiceCopilotReply(context.Context, RequestMeta, string, string) (AgentRunStatus, error)
	// ClaimServiceSession 领取或接管服务会话的当前周期。
	//appservice:route POST /conversations/{conversationID:uuid}/claim perm=none
	ClaimServiceSession(context.Context, RequestMeta, string) (ServiceSession, error)
	// TransferServiceSession 把当前负责的处理周期转给成员、团队队列或公共队列。
	//appservice:route POST /conversations/{conversationID:uuid}/transfer perm=none
	TransferServiceSession(context.Context, RequestMeta, string, TransferServiceSessionInput) (ServiceSession, error)
	// CloseServiceSession 关闭服务会话的当前周期。
	//appservice:route POST /conversations/{conversationID:uuid}/close perm=none
	CloseServiceSession(context.Context, RequestMeta, string) (ServiceSession, error)
	// ReopenServiceSession 重新打开服务会话的当前周期并分配给当前身份。
	//appservice:route POST /conversations/{conversationID:uuid}/reopen perm=none
	ReopenServiceSession(context.Context, RequestMeta, string) (ServiceSession, error)
	// SendFirstDirectTextMessage 向目标身份发送首条单聊消息并按需创建长期会话。
	//appservice:route POST /direct-conversations/messages perm=none
	SendFirstDirectTextMessage(context.Context, RequestMeta, FirstDirectTextMessageInput) (FirstDirectTextMessageResult, error)
	// SendFirstAgentTextMessage 在首次发送时创建独立 AI 聊天。
	//appservice:route POST /agent-conversations/messages perm=none
	SendFirstAgentTextMessage(context.Context, RequestMeta, FirstAgentTextMessageInput) (FirstAgentTextMessageResult, error)
	// SendAgentTextMessage 向已有 AI 会话发送文本消息。
	//appservice:route POST /agent-conversations/{conversationID:uuid}/messages perm=none
	SendAgentTextMessage(context.Context, RequestMeta, string, AgentTextMessageInput) (ConversationMessage, error)
	// FindDirectConversation 按目标身份查找当前成员的活跃单聊。
	//appservice:route GET /direct-conversations/by-target/{targetIdentityID:uuid} perm=none
	FindDirectConversation(context.Context, RequestMeta, string) (DirectConversationLookup, error)
	// SendDirectTextMessage 发送内部单聊文本消息。
	//appservice:route POST /direct-conversations/{conversationID:uuid}/messages perm=none
	SendDirectTextMessage(context.Context, RequestMeta, string, DirectTextMessageInput) (ConversationMessage, error)
	// CreateGroupConversation 创建企业内部群聊。
	//appservice:route POST /group-conversations status=201 perm=none
	CreateGroupConversation(context.Context, RequestMeta, GroupConversationInput) (InboxConversation, error)
	// GetGroupConversation 返回当前成员可见的群聊资料。
	//appservice:route GET /group-conversations/{conversationID:uuid} perm=none
	GetGroupConversation(context.Context, RequestMeta, string) (GroupConversation, error)
	// UpdateGroupConversation 修改群聊资料。
	//appservice:route PATCH /group-conversations/{conversationID:uuid} perm=none
	UpdateGroupConversation(context.Context, RequestMeta, string, GroupConversationProfileInput) (GroupConversation, error)
	// AddGroupConversationMembers 批量增加群聊成员。
	//appservice:route POST /group-conversations/{conversationID:uuid}/members perm=none
	AddGroupConversationMembers(context.Context, RequestMeta, string, GroupConversationMembersInput) (GroupConversation, error)
	// RemoveGroupConversationMember 移除单个群聊成员。
	//appservice:route POST /group-conversations/{conversationID:uuid}/members/remove perm=none
	RemoveGroupConversationMember(context.Context, RequestMeta, string, GroupConversationMemberInput) (GroupConversation, error)
	// TransferGroupConversationOwner 转让群主。
	//appservice:route POST /group-conversations/{conversationID:uuid}/owner/transfer perm=none
	TransferGroupConversationOwner(context.Context, RequestMeta, string, GroupConversationOwnerInput) (GroupConversation, error)
	// LeaveGroupConversation 退出普通成员参与的群聊。
	//appservice:route POST /group-conversations/{conversationID:uuid}/leave perm=none
	LeaveGroupConversation(context.Context, RequestMeta, string) error
	// DissolveGroupConversation 解散群聊并保留当前成员的只读历史。
	//appservice:route POST /group-conversations/{conversationID:uuid}/dissolve perm=none
	DissolveGroupConversation(context.Context, RequestMeta, string) (GroupConversation, error)
	// SendGroupTextMessage 发送企业内部群聊文本消息。
	//appservice:route POST /group-conversations/{conversationID:uuid}/messages perm=none
	SendGroupTextMessage(context.Context, RequestMeta, string, GroupTextMessageInput) (ConversationMessage, error)
	// GetAgentRunProcess 返回一次已完成运行的有序过程内容和模型用量。
	//appservice:route GET /agent-runs/{runID:uuid}/process perm=none
	GetAgentRunProcess(context.Context, RequestMeta, string) (AgentRunProcess, error)
	// GetAgentToolCallProcess 返回电脑执行的工具调用的当前状态与给定序号之后的过程更新。
	//appservice:route GET /agent-tool-calls/{toolCallID:uuid}/process perm=none
	GetAgentToolCallProcess(context.Context, RequestMeta, string, AgentToolCallProcessInput) (AgentToolCallProcess, error)
	// StopLocalAgent 停止委派给本机 Agent 的一轮并释放它所在的本机 Agent 会话。
	//appservice:route POST /agent-tool-calls/{toolCallID:uuid}/stop-local-agent perm=none
	StopLocalAgent(context.Context, RequestMeta, string) error
	// GetAgentRunStreamState 读取持久运行状态与当前执行快照。
	//appservice:route GET /agent-runs/{runID:uuid}/stream-state perm=none
	GetAgentRunStreamState(context.Context, RequestMeta, string) (AgentRunStreamState, error)
}
