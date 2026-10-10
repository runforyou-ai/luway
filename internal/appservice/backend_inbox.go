package appservice

import "context"

// InboxBackend 定义统一收件箱、会话检索、同步探针与消息投递处理的业务调用。
type InboxBackend interface {
	// LoadInbox 返回当前用户的统一收件箱。
	//appservice:route GET /inbox perm=none
	LoadInbox(context.Context, RequestMeta, LoadInboxInput) (Inbox, error)
	// GetInboxContext 返回会话锚点的当前资格和原位置邻域。
	//appservice:route POST /inbox/context/query perm=none
	GetInboxContext(context.Context, RequestMeta, InboxContextInput) (InboxContext, error)
	// ReadInboxWindow 重读已加载双向边界之间的完整列表范围。
	//appservice:route POST /inbox/window/query perm=none
	ReadInboxWindow(context.Context, RequestMeta, InboxWindowInput) (InboxWindow, error)
	// GetInboxConversation 返回当前用户有权阅读的独立会话摘要。
	//appservice:route GET /conversations/{conversationID:uuid}/summary perm=none
	GetInboxConversation(context.Context, RequestMeta, string) (InboxConversation, error)
	// ReadInboxConversations 按 ID 批量返回会话摘要及当前筛选资格。
	//appservice:route POST /inbox/conversations/query perm=none
	ReadInboxConversations(context.Context, RequestMeta, ReadInboxConversationsInput) (InboxConversationResults, error)
	// SearchInbox 按范围检索会话名称、消息正文与附件文件名、成员和外部联系人。
	//appservice:route GET /inbox/search perm=none
	SearchInbox(context.Context, RequestMeta, InboxSearchInput) (InboxSearchResult, error)
	// ListServiceAssignees 返回可接待服务会话的有效真人和 AI 员工。
	//appservice:route GET /inbox/assignees perm=none
	ListServiceAssignees(context.Context, RequestMeta) (ServiceAssigneeList, error)
	// ListServiceQueueTeams 返回可作为客服队列的团队，本人所在团队排在前面。
	//appservice:route GET /inbox/queue-teams perm=none
	ListServiceQueueTeams(context.Context, RequestMeta) (ServiceQueueTeamList, error)
	// ListInboxChannels 返回收件箱渠道筛选候选，含已停用渠道。
	//appservice:route GET /inbox/channels perm=none
	ListInboxChannels(context.Context, RequestMeta) (InboxChannelList, error)
	// GetSyncHeads 返回当前用户可见会话与身份资料的同步探针值。
	//appservice:route GET /sync/heads perm=none
	GetSyncHeads(context.Context, RequestMeta) (SyncHeads, error)
	// ListArchivedConversations 按最近活动倒序返回当前用户已归档的群聊、单聊与 AI 聊天。
	//appservice:route GET /archived-conversations perm=none
	ListArchivedConversations(context.Context, RequestMeta, ArchivedConversationListInput) (ArchivedConversationList, error)
	// ResolveChannelMessageDelivery 人工处理失败或待确认的投递。
	//appservice:route POST /conversations/{conversationID:uuid}/deliveries/{deliveryID:uuid}/resolve perm=none
	ResolveChannelMessageDelivery(context.Context, RequestMeta, string, string, ChannelDeliveryResolveInput) error
}
