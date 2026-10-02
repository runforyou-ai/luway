package appservice

import "context"

//go:generate go run github.com/runforyou-ai/luway/internal/tools/appservicegen

// Backend 定义各运行平台都需要实现的业务调用。
//
// 每个方法必须携带一条 appservice:route 指令，格式为：
//
//	appservice:route <HTTP方法> <路径> [status=201] [query=<参数名>] [auth=public|account|admin] [manual=service,api,proxy]
//
// appservicegen 按指令生成 Service 委托、Gin 路由、API Proxy 转发和服务端认证分发；
// manual 标记的层由对应包手写实现。路径中的 :参数 依次对应签名中的 string 参数，
// GET 方法的结构体参数按 query 标签绑定查询参数，其余方法的结构体参数绑定 JSON 请求体。
//
// auth 默认为 member：服务端分发层先解析登录账号在请求目标工作区中的成员身份，再把身份
// 交给业务实现，业务实现不重复处理认证。只需要登录账号的方法标记 auth=account，
// 只允许部署管理员调用的部署级管理方法标记 auth=admin，无需登录的方法标记 auth=public。
type Backend interface {
	// InstallationStatus 返回部署名称、首次安装状态、是否开放注册和产品品牌。
	//appservice:route GET /installation/status auth=public manual=proxy
	InstallationStatus(context.Context, RequestMeta) (InstallationStatus, error)
	// GetProductDocPage 返回当前部署可见的产品文档页面正文，供应用内帮助显示。
	//appservice:route GET /product-docs/page auth=public
	GetProductDocPage(context.Context, RequestMeta, ProductDocPageInput) (ProductDocPage, error)
	// Login 校验账号密码并建立登录会话。
	//appservice:route POST /auth/login auth=public manual=service,proxy
	Login(context.Context, RequestMeta, LoginInput) (Auth, error)
	// Register 在部署开放注册或持有效邀请时注册本地账号并建立登录会话。
	//appservice:route POST /auth/register auth=public manual=service,proxy
	Register(context.Context, RequestMeta, RegisterInput) (Auth, error)
	// Logout 退出当前登录会话。
	//appservice:route POST /auth/logout auth=account manual=proxy
	Logout(context.Context, RequestMeta) error
	// LoadAccount 返回当前登录账号。
	//appservice:route GET /account auth=account
	LoadAccount(context.Context, RequestMeta) (Account, error)
	// ListWorkspaces 返回当前账号作为有效成员可进入的工作区，以及当前账号能否再创建工作区。
	//appservice:route GET /workspaces auth=account
	ListWorkspaces(context.Context, RequestMeta) (WorkspaceList, error)
	// ListWorkspaceAttention 返回当前账号在各工作区的提醒数量，工作区切换器与应用角标据此提示其他工作区的未读。
	//appservice:route GET /workspace-attention auth=account
	ListWorkspaceAttention(context.Context, RequestMeta) (WorkspaceAttentionList, error)
	// CreateWorkspace 创建工作区，当前账号成为首位管理员成员。
	//appservice:route POST /workspaces status=201 auth=account
	CreateWorkspace(context.Context, RequestMeta, WorkspaceInput) (Workspace, error)
	// LoadIdentity 返回当前账号在请求目标工作区中的成员身份。
	//appservice:route GET /auth/identity manual=service,proxy
	LoadIdentity(context.Context, RequestMeta) (Identity, error)
	// UpdateProfile 修改当前成员的头像和姓名，以及所属账号的邮箱。
	//appservice:route PATCH /profile
	UpdateProfile(context.Context, RequestMeta, ProfileInput) (CurrentUser, error)
	// CreateFileUpload 创建文件上传请求。
	//appservice:route POST /files/uploads status=201
	CreateFileUpload(context.Context, RequestMeta, FileUploadInput) (FileUpload, error)
	// CompleteFileUpload 核验并完成文件上传。
	//appservice:route POST /files/:fileID/complete
	CompleteFileUpload(context.Context, RequestMeta, string) (File, error)
	// CreateFilePartUpload 创建一个分片的直传请求。
	//appservice:route POST /files/:fileID/parts
	CreateFilePartUpload(context.Context, RequestMeta, string, FilePartUploadInput) (FileUploadRequest, error)
	// CancelFileUpload 将未发送的临时文件交给清理任务。
	//appservice:route DELETE /files/:fileID/upload
	CancelFileUpload(context.Context, RequestMeta, string) error
	// SendAttachmentMessage 发送已上传的单聊、群聊或 AI 聊天附件消息，首发时创建会话。
	//appservice:route POST /conversation-attachments status=201
	SendAttachmentMessage(context.Context, RequestMeta, AttachmentMessageInput) (AttachmentMessageResult, error)
	// GetAttachmentDownload 签发当前成员可见消息附件的下载地址。
	//appservice:route GET /conversations/:conversationID/messages/:messageID/attachment
	GetAttachmentDownload(context.Context, RequestMeta, string, string) (FileDownload, error)
	// ChangePassword 核验当前账号的密码并保存新密码。
	//appservice:route PATCH /password auth=account
	ChangePassword(context.Context, RequestMeta, ChangePasswordInput) error
	// UpdateUserPreferences 保存当前用户的偏好设置。
	//appservice:route PATCH /preferences manual=service
	UpdateUserPreferences(context.Context, RequestMeta, UserPreferencesInput) (CurrentUser, error)
	// UpdateUserWorkStatus 保存当前用户主动设置的工作状态。
	//appservice:route PATCH /work-status
	UpdateUserWorkStatus(context.Context, RequestMeta, UserWorkStatusInput) (CurrentUser, error)
	// LoadInbox 返回当前用户的统一收件箱。
	//appservice:route GET /inbox
	LoadInbox(context.Context, RequestMeta, LoadInboxInput) (Inbox, error)
	// GetInboxContext 返回会话锚点的当前资格和原位置邻域。
	//appservice:route POST /inbox/context/query
	GetInboxContext(context.Context, RequestMeta, InboxContextInput) (InboxContext, error)
	// ReadInboxWindow 重读已加载双向边界之间的完整列表范围。
	//appservice:route POST /inbox/window/query
	ReadInboxWindow(context.Context, RequestMeta, InboxWindowInput) (InboxWindow, error)
	// GetRequesterProfile 返回服务会话发起人的资料；发起人是客户时给出客户身份与当前周期访客上下文。
	//appservice:route GET /conversations/:conversationID/requester-profile
	GetRequesterProfile(context.Context, RequestMeta, string) (RequesterProfile, error)
	// ListServiceBusinessQueries 返回服务会话当前周期内 AI 员工查询业务系统的记录。
	//appservice:route GET /conversations/:conversationID/business-queries
	ListServiceBusinessQueries(context.Context, RequestMeta, string) (ServiceBusinessQueryList, error)
	// GetServiceSummaries 返回服务会话当前周期的交接摘要与同一发起人已关闭周期的小结。
	//appservice:route GET /conversations/:conversationID/service-summaries
	GetServiceSummaries(context.Context, RequestMeta, string) (ServiceSummaries, error)
	// UpdateServiceSessionSummary 修改已关闭服务周期的小结、是否解决与咨询分类。
	//appservice:route PUT /service-sessions/:serviceSessionID/summary
	UpdateServiceSessionSummary(context.Context, RequestMeta, string, ServiceSessionSummaryInput) (ServiceSessionSummary, error)
	// GetInboxConversation 返回当前用户有权阅读的独立会话摘要。
	//appservice:route GET /conversations/:conversationID/summary
	GetInboxConversation(context.Context, RequestMeta, string) (InboxConversation, error)
	// ReadInboxConversations 按 ID 批量返回会话摘要及当前筛选资格。
	//appservice:route POST /inbox/conversations/query
	ReadInboxConversations(context.Context, RequestMeta, ReadInboxConversationsInput) (InboxConversationResults, error)
	// ReadConversationAttention 返回会话摘要及已知消息之后计入本人提醒的未读消息，提醒口径与应用角标一致。
	//appservice:route GET /conversations/:conversationID/attention
	ReadConversationAttention(context.Context, RequestMeta, string, ConversationAttentionInput) (ConversationAttention, error)
	// SearchInbox 按范围检索会话名称、消息正文与附件文件名、成员和外部联系人。
	//appservice:route GET /inbox/search
	SearchInbox(context.Context, RequestMeta, InboxSearchInput) (InboxSearchResult, error)
	// ListServiceAssignees 返回可接待服务会话的有效真人和 AI 员工。
	//appservice:route GET /inbox/assignees
	ListServiceAssignees(context.Context, RequestMeta) (ServiceAssigneeList, error)
	// ListServiceQueueTeams 返回可作为客服队列的团队，本人所在团队排在前面。
	//appservice:route GET /inbox/queue-teams
	ListServiceQueueTeams(context.Context, RequestMeta) (ServiceQueueTeamList, error)
	// ListInboxChannels 返回收件箱渠道筛选候选，含已停用渠道。
	//appservice:route GET /inbox/channels
	ListInboxChannels(context.Context, RequestMeta) (InboxChannelList, error)
	// GetSyncHeads 返回当前用户可见会话与身份资料的同步探针值。
	//appservice:route GET /sync/heads
	GetSyncHeads(context.Context, RequestMeta) (SyncHeads, error)
	// ListConversationMessages 返回成员可见的会话消息。
	//appservice:route GET /conversations/:conversationID/messages
	ListConversationMessages(context.Context, RequestMeta, string, ConversationMessageListInput) (ConversationMessageList, error)
	// ReadConversationMessageWindow 重读已加载首尾游标之间的完整消息范围。
	//appservice:route GET /conversations/:conversationID/message-window
	ReadConversationMessageWindow(context.Context, RequestMeta, string, ConversationMessageWindowInput) (ConversationMessageList, error)
	// GetConversationMessageContext 返回目标消息及其前后上下文。
	//appservice:route GET /conversations/:conversationID/messages/:messageID/context
	GetConversationMessageContext(context.Context, RequestMeta, string, string) (ConversationMessageList, error)
	// GetConversationNavigationState 返回群聊提及进度和最新可见消息。
	//appservice:route GET /conversations/:conversationID/navigation
	GetConversationNavigationState(context.Context, RequestMeta, string) (ConversationNavigationState, error)
	// ListPendingConversationMentions 返回本轮待查看提及目标。
	//appservice:route GET /conversations/:conversationID/mentions/pending
	ListPendingConversationMentions(context.Context, RequestMeta, string) (PendingConversationMentions, error)
	// MarkConversationMentionReviewed 确认已查看的群聊提及。
	//appservice:route POST /conversations/:conversationID/mentions/review
	MarkConversationMentionReviewed(context.Context, RequestMeta, string, MarkConversationMentionReviewedInput) (ConversationMentionReview, error)
	// MarkConversationRead 单调推进当前用户的会话已读水位。
	//appservice:route POST /conversations/:conversationID/read
	MarkConversationRead(context.Context, RequestMeta, string, MarkConversationReadInput) (ConversationReadState, error)
	// ReportConversationTyping 发布当前用户的输入状态：单聊与群聊发给其他真人成员，网站渠道客户会话发给该线程访客。
	//appservice:route POST /conversations/:conversationID/typing
	ReportConversationTyping(context.Context, RequestMeta, string, ConversationTypingInput) error
	// UpdateConversationUnreadMark 保存当前用户独立于阅读水位的未读标记。
	//appservice:route PATCH /conversations/:conversationID/unread-mark
	UpdateConversationUnreadMark(context.Context, RequestMeta, string, ConversationUnreadMarkInput) error
	// UpdateConversationPin 保存当前用户的会话置顶事实与置顶顺序。
	//appservice:route PATCH /conversations/:conversationID/pin
	UpdateConversationPin(context.Context, RequestMeta, string, ConversationPinInput) (ConversationPinState, error)
	// ListArchivedConversations 按最近活动倒序返回当前用户已归档的群聊、单聊与 AI 聊天。
	//appservice:route GET /archived-conversations
	ListArchivedConversations(context.Context, RequestMeta, ArchivedConversationListInput) (ArchivedConversationList, error)
	// UpdateConversationArchive 保存当前用户对群聊、单聊或 AI 聊天的归档状态。
	//appservice:route PATCH /conversations/:conversationID/archive
	UpdateConversationArchive(context.Context, RequestMeta, string, ConversationArchiveInput) error
	// UpdateConversationNotificationSettings 保存当前用户的原生会话提醒设置。
	//appservice:route PATCH /conversations/:conversationID/notification-settings
	UpdateConversationNotificationSettings(context.Context, RequestMeta, string, ConversationNotificationSettingsInput) (ConversationNotificationSettings, error)
	// SendServiceTextMessage 在服务会话中发送回复或内部备注。
	//appservice:route POST /conversations/:conversationID/messages
	SendServiceTextMessage(context.Context, RequestMeta, string, ServiceTextMessageInput) (ConversationMessage, error)
	// SendServiceAttachmentMessage 在服务会话中发送附件回复。
	//appservice:route POST /conversations/:conversationID/attachment-messages
	SendServiceAttachmentMessage(context.Context, RequestMeta, string, ServiceAttachmentMessageInput) (ConversationMessage, error)
	// GetConversationTranslation 返回当前成员在客户会话中的翻译状态。
	//appservice:route GET /conversations/:conversationID/translation
	GetConversationTranslation(context.Context, RequestMeta, string) (ConversationTranslation, error)
	// TranslateConversationMessages 返回客户会话中指定对客消息面向当前成员语言的译文，尚无译文的消息即时翻译。
	//appservice:route POST /conversations/:conversationID/translations
	TranslateConversationMessages(context.Context, RequestMeta, string, TranslateConversationMessagesInput) (ConversationMessageTranslationList, error)
	// UpdateCustomerReplyLanguage 锁定或解除客户会话的对客回复语言。
	//appservice:route PUT /conversations/:conversationID/reply-language
	UpdateCustomerReplyLanguage(context.Context, RequestMeta, string, CustomerReplyLanguageInput) (ConversationTranslation, error)
	// PreviewCustomerReplyTranslation 把客服回复译为客户语言并回译为客服语言，供发送前核对。
	//appservice:route POST /conversations/:conversationID/reply-translation
	PreviewCustomerReplyTranslation(context.Context, RequestMeta, string, CustomerReplyTranslationInput) (CustomerReplyTranslationPreview, error)
	// ListServiceReplyAgents 返回可用于 AI 写回复的 AI 员工。
	//appservice:route GET /reply-suggestion-agents
	ListServiceReplyAgents(context.Context, RequestMeta) (ServiceReplyAgentList, error)
	// GenerateServiceReplySuggestions 使用 AI 员工为服务会话生成回复候选。
	//appservice:route POST /conversations/:conversationID/reply-suggestions
	GenerateServiceReplySuggestions(context.Context, RequestMeta, string, ServiceReplySuggestionsInput) (ServiceReplySuggestions, error)
	// ListServiceCopilotThreads 返回服务会话的全部 Copilot 线程。
	//appservice:route GET /conversations/:conversationID/copilot-threads
	ListServiceCopilotThreads(context.Context, RequestMeta, string) (ServiceCopilotThreadList, error)
	// SendFirstServiceCopilotMessage 以首条提问创建服务会话的 Copilot 线程。
	//appservice:route POST /conversations/:conversationID/copilot-threads
	SendFirstServiceCopilotMessage(context.Context, RequestMeta, string, FirstServiceCopilotMessageInput) (FirstServiceCopilotMessageResult, error)
	// SendServiceCopilotTextMessage 向 Copilot 线程发送提问。
	//appservice:route POST /copilot-threads/:threadID/messages
	SendServiceCopilotTextMessage(context.Context, RequestMeta, string, ServiceCopilotTextMessageInput) (ConversationMessage, error)
	// StopServiceCopilotReply 停止 Copilot 线程中指定的回复并返回实际运行状态。
	//appservice:route POST /copilot-threads/:threadID/runs/:runID/stop
	StopServiceCopilotReply(context.Context, RequestMeta, string, string) (AgentRunStatus, error)
	// ResolveCustomerMessageDelivery 人工处理失败或待确认的投递。
	//appservice:route POST /conversations/:conversationID/deliveries/:deliveryID/resolve
	ResolveCustomerMessageDelivery(context.Context, RequestMeta, string, string, CustomerDeliveryResolveInput) error
	// ClaimServiceSession 领取或接管服务会话的当前周期。
	//appservice:route POST /conversations/:conversationID/claim
	ClaimServiceSession(context.Context, RequestMeta, string) (ServiceSession, error)
	// TransferServiceSession 把当前负责的处理周期转给成员、团队队列或公共队列。
	//appservice:route POST /conversations/:conversationID/transfer
	TransferServiceSession(context.Context, RequestMeta, string, TransferServiceSessionInput) (ServiceSession, error)
	// CloseServiceSession 关闭服务会话的当前周期。
	//appservice:route POST /conversations/:conversationID/close
	CloseServiceSession(context.Context, RequestMeta, string) (ServiceSession, error)
	// ReopenServiceSession 重新打开服务会话的当前周期并分配给当前身份。
	//appservice:route POST /conversations/:conversationID/reopen
	ReopenServiceSession(context.Context, RequestMeta, string) (ServiceSession, error)
	// SendFirstDirectTextMessage 向目标身份发送首条单聊消息并按需创建长期会话。
	//appservice:route POST /direct-conversations/messages
	SendFirstDirectTextMessage(context.Context, RequestMeta, FirstDirectTextMessageInput) (FirstDirectTextMessageResult, error)
	// SendFirstAgentTextMessage 在首次发送时创建独立 AI 聊天。
	//appservice:route POST /agent-conversations/messages
	SendFirstAgentTextMessage(context.Context, RequestMeta, FirstAgentTextMessageInput) (FirstAgentTextMessageResult, error)
	// SendAgentTextMessage 向已有 AI 会话发送文本消息。
	//appservice:route POST /agent-conversations/:conversationID/messages
	SendAgentTextMessage(context.Context, RequestMeta, string, AgentTextMessageInput) (ConversationMessage, error)
	// StopAgentReply 停止独立 AI 会话中指定的回复并返回实际运行状态。
	//appservice:route POST /agent-conversations/:conversationID/runs/:runID/stop
	StopAgentReply(context.Context, RequestMeta, string, string) (AgentRunStatus, error)
	// FindDirectConversation 按目标身份查找当前成员的活跃单聊。
	//appservice:route GET /direct-conversations/by-target/:targetIdentityID
	FindDirectConversation(context.Context, RequestMeta, string) (DirectConversationLookup, error)
	// SendDirectTextMessage 发送内部单聊文本消息。
	//appservice:route POST /direct-conversations/:conversationID/messages
	SendDirectTextMessage(context.Context, RequestMeta, string, DirectTextMessageInput) (ConversationMessage, error)
	// CreateGroupConversation 创建企业内部群聊。
	//appservice:route POST /group-conversations status=201
	CreateGroupConversation(context.Context, RequestMeta, GroupConversationInput) (InboxConversation, error)
	// GetGroupConversation 返回当前成员可见的群聊资料。
	//appservice:route GET /group-conversations/:conversationID
	GetGroupConversation(context.Context, RequestMeta, string) (GroupConversation, error)
	// UpdateGroupConversation 修改群聊资料。
	//appservice:route PATCH /group-conversations/:conversationID
	UpdateGroupConversation(context.Context, RequestMeta, string, GroupConversationProfileInput) (GroupConversation, error)
	// AddGroupConversationMembers 批量增加群聊成员。
	//appservice:route POST /group-conversations/:conversationID/members
	AddGroupConversationMembers(context.Context, RequestMeta, string, GroupConversationMembersInput) (GroupConversation, error)
	// RemoveGroupConversationMember 移除单个群聊成员。
	//appservice:route POST /group-conversations/:conversationID/members/remove
	RemoveGroupConversationMember(context.Context, RequestMeta, string, GroupConversationMemberInput) (GroupConversation, error)
	// TransferGroupConversationOwner 转让群主。
	//appservice:route POST /group-conversations/:conversationID/owner/transfer
	TransferGroupConversationOwner(context.Context, RequestMeta, string, GroupConversationOwnerInput) (GroupConversation, error)
	// LeaveGroupConversation 退出普通成员参与的群聊。
	//appservice:route POST /group-conversations/:conversationID/leave
	LeaveGroupConversation(context.Context, RequestMeta, string) error
	// DissolveGroupConversation 解散群聊并保留当前成员的只读历史。
	//appservice:route POST /group-conversations/:conversationID/dissolve
	DissolveGroupConversation(context.Context, RequestMeta, string) (GroupConversation, error)
	// SendGroupTextMessage 发送企业内部群聊文本消息。
	//appservice:route POST /group-conversations/:conversationID/messages
	SendGroupTextMessage(context.Context, RequestMeta, string, GroupTextMessageInput) (ConversationMessage, error)
	// StopGroupAgentReply 停止群聊中指定的 AI 员工回复并返回实际运行状态。
	//appservice:route POST /group-conversations/:conversationID/runs/:runID/stop
	StopGroupAgentReply(context.Context, RequestMeta, string, string) (AgentRunStatus, error)
	// GetAgentRunProcess 返回一次已完成运行的有序过程内容和模型用量。
	//appservice:route GET /agent-runs/:runID/process
	GetAgentRunProcess(context.Context, RequestMeta, string) (AgentRunProcess, error)
	// AuthorizeAgentRunStreamAccess 校验当前成员对运行所属会话的阅读资格，原生端读取本机执行中运行的过程流前调用。
	//appservice:route GET /agent-runs/:runID/stream-access
	AuthorizeAgentRunStreamAccess(context.Context, RequestMeta, string) error
	// ListMessageChannels 返回消息渠道列表。
	//appservice:route GET /channels
	ListMessageChannels(context.Context, RequestMeta) (MessageChannelList, error)
	// GetWebsiteChannel 返回网站渠道详情。
	//appservice:route GET /channels/website/:channelID
	GetWebsiteChannel(context.Context, RequestMeta, string) (WebsiteChannel, error)
	// GetTelegramChannel 返回 Telegram 渠道详情。
	//appservice:route GET /channels/telegram/:channelID
	GetTelegramChannel(context.Context, RequestMeta, string) (TelegramChannel, error)
	// TestTelegramChannelConnection 测试 Telegram 草稿 Token。
	//appservice:route POST /channels/telegram/:channelID/connection/test
	TestTelegramChannelConnection(context.Context, RequestMeta, string, TelegramChannelConnectionTestInput) error
	// SaveTelegramChannelConnection 保存 Telegram 机器人和 Webhook 设置。
	//appservice:route PUT /channels/telegram/:channelID/connection
	SaveTelegramChannelConnection(context.Context, RequestMeta, string, TelegramChannelConnectionInput) (TelegramChannel, error)
	// GetMessageChannel 返回消息渠道基础信息。
	//appservice:route GET /channels/:channelID
	GetMessageChannel(context.Context, RequestMeta, string) (MessageChannelSummary, error)
	// CreateMessageChannel 创建消息渠道。
	//appservice:route POST /channels status=201
	CreateMessageChannel(context.Context, RequestMeta, CreateMessageChannelInput) (MessageChannelSummary, error)
	// UpdateMessageChannel 修改消息渠道基础信息。
	//appservice:route PUT /channels/:channelID
	UpdateMessageChannel(context.Context, RequestMeta, string, MessageChannelBasicsInput) (MessageChannelSummary, error)
	// UpdateMessageChannelReception 修改消息渠道接待设置。
	//appservice:route PUT /channels/:channelID/reception
	UpdateMessageChannelReception(context.Context, RequestMeta, string, MessageChannelReceptionInput) (MessageChannelSummary, error)
	// UpdateWebsiteChannelChatInterface 修改网站渠道聊天窗口外观与对话功能。
	//appservice:route PUT /channels/website/:channelID/chat-interface
	UpdateWebsiteChannelChatInterface(context.Context, RequestMeta, string, WebsiteChannelChatInterfaceInput) (WebsiteChannelChatInterface, error)
	// UpdateWebsiteChannelAccess 修改网站渠道允许使用的网站。
	//appservice:route PUT /channels/website/:channelID/access
	UpdateWebsiteChannelAccess(context.Context, RequestMeta, string, WebsiteChannelAccessInput) (WebsiteChannelAccess, error)
	// UpdateWebsiteChannelHome 修改网站渠道 Messenger 首页。
	//appservice:route PUT /channels/website/:channelID/home
	UpdateWebsiteChannelHome(context.Context, RequestMeta, string, WebsiteChannelHomeInput) (WebsiteChannelHome, error)
	// UpdateWebsiteChannelHelpCenter 修改网站渠道帮助页签开关与发布的知识库。
	//appservice:route PUT /channels/website/:channelID/help-center
	UpdateWebsiteChannelHelpCenter(context.Context, RequestMeta, string, WebsiteChannelHelpCenterInput) (WebsiteChannelHelpCenter, error)
	// DeactivateMessageChannel 停用消息渠道。
	//appservice:route POST /channels/:channelID/deactivate
	DeactivateMessageChannel(context.Context, RequestMeta, string) (MessageChannelSummary, error)
	// ActivateMessageChannel 启用消息渠道。
	//appservice:route POST /channels/:channelID/activate
	ActivateMessageChannel(context.Context, RequestMeta, string) (MessageChannelSummary, error)
	// ListChannelOptions 返回当前企业的渠道选择项。
	//appservice:route GET /channels/options
	ListChannelOptions(context.Context, RequestMeta) (ChannelOptionList, error)
	// ListMemberOptions 返回可分配的企业成员和 AI 员工。
	//appservice:route GET /members/options
	ListMemberOptions(context.Context, RequestMeta, MemberOptionListInput) (MemberOptionList, error)
	// ListColleagues 返回通讯录同事目录，服务台排在成员之前。
	//appservice:route GET /colleagues
	ListColleagues(context.Context, RequestMeta, ColleagueListInput) (ColleagueList, error)
	// ListAgentMCPServerOptions 返回当前企业可配置的 MCP 服务。
	//appservice:route GET /agents/mcp-server-options
	ListAgentMCPServerOptions(context.Context, RequestMeta) (AgentMCPServerOptionList, error)
	// CreateAgent 创建企业 AI 员工。
	//appservice:route POST /agents status=201
	CreateAgent(context.Context, RequestMeta, CreateAgentInput) (Agent, error)
	// ListAgents 返回企业 AI 员工目录。
	//appservice:route GET /agents
	ListAgents(context.Context, RequestMeta, AgentListInput) (AgentList, error)
	// GetAgent 返回企业 AI 员工详情。
	//appservice:route GET /agents/:agentID
	GetAgent(context.Context, RequestMeta, string) (Agent, error)
	// UpdateAgent 修改企业 AI 员工。
	//appservice:route PUT /agents/:agentID
	UpdateAgent(context.Context, RequestMeta, string, UpdateAgentInput) (Agent, error)
	// UpdateAgentExecution 修改企业 AI 员工的执行配置。
	//appservice:route PUT /agents/:agentID/execution
	UpdateAgentExecution(context.Context, RequestMeta, string, UpdateAgentExecutionInput) (Agent, error)
	// DeactivateAgent 禁用企业 AI 员工账号。
	//appservice:route POST /agents/:agentID/deactivate
	DeactivateAgent(context.Context, RequestMeta, string) (Agent, error)
	// ReactivateAgent 恢复企业 AI 员工。
	//appservice:route POST /agents/:agentID/reactivate
	ReactivateAgent(context.Context, RequestMeta, string) (Agent, error)
	// GetAgentEvaluation 返回 AI 员工评测页的最近两次运行与全部用例。
	//appservice:route GET /agents/:agentID/evaluation
	GetAgentEvaluation(context.Context, RequestMeta, string) (AgentEvaluation, error)
	// StartAgentEvaluationRun 用 AI 员工当前生效的配置对全部用例发起一次评测运行。
	//appservice:route POST /agents/:agentID/evaluation/runs status=201
	StartAgentEvaluationRun(context.Context, RequestMeta, string) error
	// CreateAgentEvaluationCase 为 AI 员工新建手动评测用例。
	//appservice:route POST /agents/:agentID/evaluation/cases status=201
	CreateAgentEvaluationCase(context.Context, RequestMeta, string, AgentEvaluationCaseInput) (AgentEvaluationCase, error)
	// GetAgentEvaluationCase 返回评测用例与它在最近一次运行中的全部尝试。
	//appservice:route GET /agents/:agentID/evaluation/cases/:caseID
	GetAgentEvaluationCase(context.Context, RequestMeta, string, string) (AgentEvaluationCaseDetail, error)
	// UpdateAgentEvaluationCase 修改评测用例。
	//appservice:route PUT /agents/:agentID/evaluation/cases/:caseID
	UpdateAgentEvaluationCase(context.Context, RequestMeta, string, string, AgentEvaluationCaseInput) (AgentEvaluationCase, error)
	// DeleteAgentEvaluationCase 删除评测用例。
	//appservice:route DELETE /agents/:agentID/evaluation/cases/:caseID
	DeleteAgentEvaluationCase(context.Context, RequestMeta, string, string) error
	// RerunAgentEvaluationCase 在最近一次运行中重新运行一条用例。
	//appservice:route POST /agents/:agentID/evaluation/cases/:caseID/rerun
	RerunAgentEvaluationCase(context.Context, RequestMeta, string, string) error
	// ListPersonalAgents 返回当前成员负责的个人 AI 员工。
	//appservice:route GET /personal-agents
	ListPersonalAgents(context.Context, RequestMeta) (PersonalAgentList, error)
	// ListMemberPersonalAgents 返回指定成员负责的个人 AI 员工。
	//appservice:route GET /users/:userID/personal-agents
	ListMemberPersonalAgents(context.Context, RequestMeta, string) (PersonalAgentList, error)
	// GetPersonalAgent 返回当前成员负责的个人 AI 员工详情。
	//appservice:route GET /personal-agents/:agentID
	GetPersonalAgent(context.Context, RequestMeta, string) (PersonalAgentDetail, error)
	// CreatePersonalAgent 在当前成员的电脑上创建个人 AI 员工。
	//appservice:route POST /personal-agents status=201
	CreatePersonalAgent(context.Context, RequestMeta, CreatePersonalAgentInput) (PersonalAgent, error)
	// UpdatePersonalAgent 修改当前成员负责的个人 AI 员工。
	//appservice:route PUT /personal-agents/:agentID
	UpdatePersonalAgent(context.Context, RequestMeta, string, PersonalAgentInput) (PersonalAgent, error)
	// PausePersonalAgent 暂停当前成员负责的个人 AI 员工。
	//appservice:route POST /personal-agents/:agentID/pause
	PausePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// ResumePersonalAgent 恢复当前成员负责的已暂停个人 AI 员工。
	//appservice:route POST /personal-agents/:agentID/resume
	ResumePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// MovePersonalAgent 把当前成员负责的个人 AI 员工换到指定电脑。
	//appservice:route PUT /personal-agents/:agentID/device
	MovePersonalAgent(context.Context, RequestMeta, string, PersonalAgentDeviceInput) (PersonalAgent, error)
	// DeactivatePersonalAgent 停用个人 AI 员工。
	//appservice:route POST /personal-agents/:agentID/deactivate
	DeactivatePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// ReactivatePersonalAgent 启用已停用的个人 AI 员工。
	//appservice:route POST /personal-agents/:agentID/reactivate
	ReactivatePersonalAgent(context.Context, RequestMeta, string) (PersonalAgent, error)
	// ListAgentMemories 返回当前成员负责的个人 AI 员工的记忆，按最近更新排列。
	//appservice:route GET /personal-agents/:agentID/memories
	ListAgentMemories(context.Context, RequestMeta, string) (AgentMemoryList, error)
	// UpdateAgentMemory 修改当前成员负责的个人 AI 员工的一条记忆。
	//appservice:route PUT /personal-agents/:agentID/memories/:memoryID
	UpdateAgentMemory(context.Context, RequestMeta, string, string, AgentMemoryInput) (AgentMemory, error)
	// DeleteAgentMemory 删除当前成员负责的个人 AI 员工的一条记忆。
	//appservice:route DELETE /personal-agents/:agentID/memories/:memoryID
	DeleteAgentMemory(context.Context, RequestMeta, string, string) error
	// ListUsers 返回企业成员列表。
	//appservice:route GET /users
	ListUsers(context.Context, RequestMeta, UserListInput) (UserList, error)
	// GetUser 返回企业成员详情。
	//appservice:route GET /users/:userID
	GetUser(context.Context, RequestMeta, string) (User, error)
	// ListInvitations 返回当前工作区待接受的成员邀请。
	//appservice:route GET /invitations
	ListInvitations(context.Context, RequestMeta) (InvitationList, error)
	// CreateInvitation 邀请账号加入当前工作区，返回只展示一次的邀请链接。
	//appservice:route POST /invitations status=201
	CreateInvitation(context.Context, RequestMeta, InvitationInput) (InvitationCreated, error)
	// RegenerateInvitation 撤销原邀请并以相同内容重新生成邀请链接。
	//appservice:route POST /invitations/:invitationID/regenerate
	RegenerateInvitation(context.Context, RequestMeta, string) (InvitationCreated, error)
	// RevokeInvitation 撤销待接受的邀请。
	//appservice:route DELETE /invitations/:invitationID
	RevokeInvitation(context.Context, RequestMeta, string) error
	// PreviewInvitation 按邀请令牌返回工作区名称、邀请人和掩码后的受邀邮箱。
	//appservice:route POST /invitation-previews auth=public
	PreviewInvitation(context.Context, RequestMeta, InvitationTokenInput) (InvitationPreview, error)
	// AcceptInvitation 由当前账号接受邀请并加入工作区。
	//appservice:route POST /invitation-acceptances auth=account
	AcceptInvitation(context.Context, RequestMeta, InvitationTokenInput) (Workspace, error)
	// GetDeploymentOverview 返回实例标识、服务端版本、账号与工作区数量和实例能力。
	//appservice:route GET /deployment/overview auth=admin
	GetDeploymentOverview(context.Context, RequestMeta) (DeploymentOverview, error)
	// GetDeploymentSettings 返回部署注册策略和工作区创建策略。
	//appservice:route GET /deployment/settings auth=admin
	GetDeploymentSettings(context.Context, RequestMeta) (DeploymentSettings, error)
	// UpdateDeploymentSettings 修改部署注册策略和工作区创建策略。
	//appservice:route PUT /deployment/settings auth=admin
	UpdateDeploymentSettings(context.Context, RequestMeta, DeploymentSettings) (DeploymentSettings, error)
	// ListDeploymentAccounts 返回部署内的账号。
	//appservice:route GET /deployment/accounts auth=admin
	ListDeploymentAccounts(context.Context, RequestMeta, DeploymentAccountListInput) (DeploymentAccountList, error)
	// DeactivateDeploymentAccount 停用其他账号并使其登录会话失效。
	//appservice:route POST /deployment/accounts/:accountID/deactivate auth=admin
	DeactivateDeploymentAccount(context.Context, RequestMeta, string) (DeploymentAccount, error)
	// ReactivateDeploymentAccount 恢复已停用的其他账号。
	//appservice:route POST /deployment/accounts/:accountID/reactivate auth=admin
	ReactivateDeploymentAccount(context.Context, RequestMeta, string) (DeploymentAccount, error)
	// GrantDeploymentAdmin 把其他账号设为部署管理员。
	//appservice:route POST /deployment/accounts/:accountID/admin auth=admin
	GrantDeploymentAdmin(context.Context, RequestMeta, string) (DeploymentAccount, error)
	// RevokeDeploymentAdmin 撤销其他账号的部署管理员身份。
	//appservice:route DELETE /deployment/accounts/:accountID/admin auth=admin
	RevokeDeploymentAdmin(context.Context, RequestMeta, string) (DeploymentAccount, error)
	// ListDeploymentWorkspaces 返回部署内的全部工作区。
	//appservice:route GET /deployment/workspaces auth=admin
	ListDeploymentWorkspaces(context.Context, RequestMeta, DeploymentWorkspaceListInput) (DeploymentWorkspaceList, error)
	// UpdateUser 修改企业成员头像、资料、角色和所属团队。
	//appservice:route PUT /users/:userID
	UpdateUser(context.Context, RequestMeta, string, UpdateUserInput) (User, error)
	// UpdateRoleAssignments 在一个事务中批量调整成员角色。
	//appservice:route PATCH /roles/assignments
	UpdateRoleAssignments(context.Context, RequestMeta, RoleAssignmentsInput) error
	// DeactivateUser 禁用企业成员账号。
	//appservice:route POST /users/:userID/deactivate
	DeactivateUser(context.Context, RequestMeta, string) (User, error)
	// ReactivateUser 恢复企业成员账号。
	//appservice:route POST /users/:userID/reactivate
	ReactivateUser(context.Context, RequestMeta, string) (User, error)
	// ListTeams 返回企业团队列表。
	//appservice:route GET /teams
	ListTeams(context.Context, RequestMeta, TeamListInput) (TeamList, error)
	// GetTeam 返回团队详情。
	//appservice:route GET /teams/:teamID
	GetTeam(context.Context, RequestMeta, string) (Team, error)
	// CreateTeam 创建企业团队。
	//appservice:route POST /teams status=201
	CreateTeam(context.Context, RequestMeta, TeamInput) (Team, error)
	// UpdateTeam 修改企业团队。
	//appservice:route PUT /teams/:teamID
	UpdateTeam(context.Context, RequestMeta, string, TeamInput) (Team, error)
	// DeleteTeam 删除企业团队及其成员关系。
	//appservice:route DELETE /teams/:teamID
	DeleteTeam(context.Context, RequestMeta, string) error
	// ListTeamMembers 返回团队成员列表。
	//appservice:route GET /teams/:teamID/members
	ListTeamMembers(context.Context, RequestMeta, string, TeamMemberListInput) (TeamMemberList, error)
	// ListTeamMemberCandidates 返回尚未加入团队的企业身份。
	//appservice:route GET /teams/:teamID/member-candidates
	ListTeamMemberCandidates(context.Context, RequestMeta, string, TeamMemberCandidateInput) (TeamMemberCandidateList, error)
	// AddTeamMembers 将企业身份批量加入团队。
	//appservice:route POST /teams/:teamID/members
	AddTeamMembers(context.Context, RequestMeta, string, TeamMemberInput) (Team, error)
	// RemoveTeamMembers 将企业身份批量移出团队。
	//appservice:route POST /teams/:teamID/members/remove
	RemoveTeamMembers(context.Context, RequestMeta, string, TeamMemberInput) (Team, error)
	// RetryKnowledgeDocument 按当前配置重新处理文档。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/documents/:documentID/retry
	RetryKnowledgeDocument(context.Context, RequestMeta, string, string) error
	// ListKnowledgeDocumentSegments 返回固定批次的分段页或锚点所在页。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/documents/:documentID/segments
	ListKnowledgeDocumentSegments(context.Context, RequestMeta, string, string, KnowledgeDocumentSegmentInput) (KnowledgeDocumentSegmentPage, error)
	// RetrieveKnowledgeBase 在指定知识库中执行检索测试，返回混合召回与重排后的分段。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/retrieval
	RetrieveKnowledgeBase(context.Context, RequestMeta, string, KnowledgeRetrievalInput) (KnowledgeRetrievalResult, error)
	// ListKnowledgeDocuments 返回当前分组的文档列表。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/documents
	ListKnowledgeDocuments(context.Context, RequestMeta, string, KnowledgeDocumentListInput) (KnowledgeDocumentList, error)
	// GetKnowledgeDocument 返回文档详情。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/documents/:documentID
	GetKnowledgeDocument(context.Context, RequestMeta, string, string) (KnowledgeDocument, error)
	// CreateKnowledgeDocuments 保存最多十个已上传的文档原件。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/documents status=201
	CreateKnowledgeDocuments(context.Context, RequestMeta, string, KnowledgeDocumentBatchInput) (KnowledgeDocumentBatch, error)
	// DeleteKnowledgeDocument 删除文档并释放原件。
	//appservice:route DELETE /knowledge-bases/:knowledgeBaseID/documents/:documentID
	DeleteKnowledgeDocument(context.Context, RequestMeta, string, string) error
	// GetKnowledgeDocumentPreview 签发当前文档的原件预览请求。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/documents/:documentID/preview
	GetKnowledgeDocumentPreview(context.Context, RequestMeta, string, string) (KnowledgeDocumentPreviewRequest, error)
	// CreateKnowledgeTextDocument 创建在线编写的文档并安排索引。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/text-documents status=201
	CreateKnowledgeTextDocument(context.Context, RequestMeta, string, KnowledgeTextDocumentInput) (KnowledgeDocument, error)
	// GetKnowledgeDocumentContent 返回在线文档正文或网页抓取快照。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/documents/:documentID/content
	GetKnowledgeDocumentContent(context.Context, RequestMeta, string, string) (KnowledgeDocumentContent, error)
	// UpdateKnowledgeDocumentContent 修改在线文档的名称与正文并安排索引。
	//appservice:route PUT /knowledge-bases/:knowledgeBaseID/documents/:documentID/content
	UpdateKnowledgeDocumentContent(context.Context, RequestMeta, string, string, KnowledgeDocumentContentInput) (KnowledgeDocument, error)
	// RenameKnowledgeDocument 修改在线文档或网页文档的名称。
	//appservice:route PUT /knowledge-bases/:knowledgeBaseID/documents/:documentID
	RenameKnowledgeDocument(context.Context, RequestMeta, string, string, KnowledgeDocumentRenameInput) (KnowledgeDocument, error)
	// CreateKnowledgeWebDocument 导入网页并安排首次抓取。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/web-documents status=201
	CreateKnowledgeWebDocument(context.Context, RequestMeta, string, KnowledgeWebDocumentInput) (KnowledgeDocument, error)
	// RefetchKnowledgeDocument 重新抓取网页文档并重新索引。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/documents/:documentID/refetch
	RefetchKnowledgeDocument(context.Context, RequestMeta, string, string, KnowledgeDocumentRefetchInput) error

	// ListKnowledgeQAEntries 返回分组中的本地问答列表。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/qa-entries
	ListKnowledgeQAEntries(context.Context, RequestMeta, string, KnowledgeQAListInput) (KnowledgeQAList, error)
	// GetKnowledgeQAEntry 返回完整的本地问答。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/qa-entries/:entryID
	GetKnowledgeQAEntry(context.Context, RequestMeta, string, string) (KnowledgeQAEntry, error)
	// CreateKnowledgeQAEntry 创建本地问答。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/qa-entries status=201
	CreateKnowledgeQAEntry(context.Context, RequestMeta, string, KnowledgeQAInput) (KnowledgeQAEntry, error)
	// UpdateKnowledgeQAEntry 修改本地问答。
	//appservice:route PUT /knowledge-bases/:knowledgeBaseID/qa-entries/:entryID
	UpdateKnowledgeQAEntry(context.Context, RequestMeta, string, string, KnowledgeQAInput) (KnowledgeQAEntry, error)
	// DeleteKnowledgeQAEntry 删除本地问答。
	//appservice:route DELETE /knowledge-bases/:knowledgeBaseID/qa-entries/:entryID
	DeleteKnowledgeQAEntry(context.Context, RequestMeta, string, string) error
	// RetryKnowledgeQAEntry 按当前配置重新索引问答。
	//appservice:route POST /knowledge-bases/:knowledgeBaseID/qa-entries/:entryID/retry
	RetryKnowledgeQAEntry(context.Context, RequestMeta, string, string) error
	// ListKnowledgeBases 返回当前企业的知识库列表。
	//appservice:route GET /knowledge-bases
	ListKnowledgeBases(context.Context, RequestMeta) (KnowledgeBaseList, error)
	// GetKnowledgeBase 返回当前企业中的知识库详情。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID
	GetKnowledgeBase(context.Context, RequestMeta, string) (KnowledgeBase, error)
	// ListKnowledgeBaseAgents 返回当前配置版本绑定知识库的 AI 员工。
	//appservice:route GET /knowledge-bases/:knowledgeBaseID/agents
	ListKnowledgeBaseAgents(context.Context, RequestMeta, string) (KnowledgeBaseAgentList, error)
	// CreateKnowledgeBase 创建企业知识库。
	//appservice:route POST /knowledge-bases status=201
	CreateKnowledgeBase(context.Context, RequestMeta, KnowledgeBaseInput) (KnowledgeBase, error)
	// UpdateKnowledgeBase 修改企业知识库。
	//appservice:route PUT /knowledge-bases/:knowledgeBaseID
	UpdateKnowledgeBase(context.Context, RequestMeta, string, KnowledgeBaseInput) (KnowledgeBase, error)
	// DeleteKnowledgeBase 删除企业知识库。
	//appservice:route DELETE /knowledge-bases/:knowledgeBaseID
	DeleteKnowledgeBase(context.Context, RequestMeta, string) error
	// ListContacts 返回联系人列表。
	//appservice:route GET /contacts
	ListContacts(context.Context, RequestMeta, ContactListInput) (ContactList, error)
	// GetContact 返回联系人详情。
	//appservice:route GET /contacts/:contactID
	GetContact(context.Context, RequestMeta, string) (Contact, error)
	// CreateContact 创建联系人。
	//appservice:route POST /contacts status=201
	CreateContact(context.Context, RequestMeta, ContactInput) (Contact, error)
	// UpdateContact 修改联系人。
	//appservice:route PUT /contacts/:contactID
	UpdateContact(context.Context, RequestMeta, string, ContactInput) (Contact, error)
	// DeleteContact 将联系人移入回收站。
	//appservice:route DELETE /contacts/:contactID
	DeleteContact(context.Context, RequestMeta, string) error
	// RestoreContact 恢复联系人。
	//appservice:route POST /contacts/:contactID/restore
	RestoreContact(context.Context, RequestMeta, string) (Contact, error)
	// SetContactFieldValue 由客服填写或清空联系人字段。
	//appservice:route PUT /contacts/:contactID/fields/:fieldID
	SetContactFieldValue(context.Context, RequestMeta, string, string, ContactFieldValueInput) error
	// AddContactTag 由客服给联系人添加标签。
	//appservice:route PUT /contacts/:contactID/tags/:tagID
	AddContactTag(context.Context, RequestMeta, string, string) error
	// RemoveContactTag 由客服移除联系人上的标签。
	//appservice:route DELETE /contacts/:contactID/tags/:tagID
	RemoveContactTag(context.Context, RequestMeta, string, string) error
	// ListRoles 返回当前企业的角色和预定义权限目录。
	//appservice:route GET /settings/roles
	ListRoles(context.Context, RequestMeta) (RoleList, error)
	// GetRole 返回当前企业的角色详情。
	//appservice:route GET /settings/roles/:roleID
	GetRole(context.Context, RequestMeta, string) (Role, error)
	// CreateRole 创建自定义角色。
	//appservice:route POST /settings/roles status=201
	CreateRole(context.Context, RequestMeta, RoleInput) (Role, error)
	// UpdateRole 修改角色信息和权限。
	//appservice:route PUT /settings/roles/:roleID
	UpdateRole(context.Context, RequestMeta, string, RoleInput) (Role, error)
	// DeleteRole 删除自定义角色。
	//appservice:route DELETE /settings/roles/:roleID
	DeleteRole(context.Context, RequestMeta, string) error
	// ListAIModelOptions 返回当前工作区满足指定用途的模型。
	//appservice:route GET /ai-models query=usage
	ListAIModelOptions(context.Context, RequestMeta, AIModelUsage) (AIModelOptionList, error)
	// ListAIProviders 返回当前企业的模型服务供应商列表。
	//appservice:route GET /settings/model-services
	ListAIProviders(context.Context, RequestMeta) (AIProviderList, error)
	// GetAIProvider 返回当前企业中的模型服务供应商详情。
	//appservice:route GET /settings/model-services/:providerID
	GetAIProvider(context.Context, RequestMeta, string) (AIProvider, error)
	// ListAvailableAIModels 返回指定品牌的预设模型目录。
	//appservice:route GET /settings/model-services/models query=brand
	ListAvailableAIModels(context.Context, RequestMeta, AIProviderBrand) (AIProviderModelList, error)
	// DiscoverAIProviderModels 读取模型服务实例当前可用的模型目录。
	//appservice:route POST /settings/model-services/discover-models
	DiscoverAIProviderModels(context.Context, RequestMeta, AIProviderConnectionInput) (AIProviderModelList, error)
	// TestAIProviderConnection 测试模型服务供应商草稿配置。
	//appservice:route POST /settings/model-services/test
	TestAIProviderConnection(context.Context, RequestMeta, AIProviderConnectionInput) error
	// CreateAIProvider 创建模型服务供应商。
	//appservice:route POST /settings/model-services status=201
	CreateAIProvider(context.Context, RequestMeta, AIProviderInput) (AIProvider, error)
	// UpdateAIProvider 修改模型服务供应商。
	//appservice:route PUT /settings/model-services/:providerID
	UpdateAIProvider(context.Context, RequestMeta, string, AIProviderUpdateInput) (AIProvider, error)
	// DeleteAIProvider 删除模型服务供应商。
	//appservice:route DELETE /settings/model-services/:providerID
	DeleteAIProvider(context.Context, RequestMeta, string) error
	// GetWebSearchSettings 读取当前企业的联网搜索设置。
	//appservice:route GET /settings/web-search
	GetWebSearchSettings(context.Context, RequestMeta) (WebSearchSettings, error)
	// UpdateWebSearchSettings 修改当前企业的联网搜索设置。
	//appservice:route PUT /settings/web-search
	UpdateWebSearchSettings(context.Context, RequestMeta, WebSearchSettings) (WebSearchSettings, error)
	// TestWebSearchService 用草稿配置执行一次搜索，验证搜索服务可用。
	//appservice:route POST /settings/web-search/test
	TestWebSearchService(context.Context, RequestMeta, WebSearchService) error
	// ListMCPServers 返回当前企业配置的 MCP 服务。
	//appservice:route GET /settings/mcp-servers
	ListMCPServers(context.Context, RequestMeta) (MCPServerList, error)
	// GetMCPServer 返回当前企业中的 MCP 服务详情。
	//appservice:route GET /settings/mcp-servers/:mcpServerID
	GetMCPServer(context.Context, RequestMeta, string) (MCPServer, error)
	// TestMCPServerConnection 测试 MCP 草稿连接配置。
	//appservice:route POST /settings/mcp-servers/test-connection
	TestMCPServerConnection(context.Context, RequestMeta, MCPServerConnectionInput) error
	// TestSavedMCPServerConnection 测试已保存的 MCP 服务。
	//appservice:route POST /settings/mcp-servers/:mcpServerID/test-connection
	TestSavedMCPServerConnection(context.Context, RequestMeta, string) error
	// RefreshMCPServerTools 提交当前企业的 MCP 工具更新任务。
	//appservice:route POST /settings/mcp-servers/refresh-tools
	RefreshMCPServerTools(context.Context, RequestMeta) error

	// CreateMCPServer 创建 MCP 服务。
	//appservice:route POST /settings/mcp-servers status=201
	CreateMCPServer(context.Context, RequestMeta, MCPServerInput) (MCPServer, error)
	// UpdateMCPServer 修改 MCP 服务。
	//appservice:route PUT /settings/mcp-servers/:mcpServerID
	UpdateMCPServer(context.Context, RequestMeta, string, MCPServerInput) (MCPServer, error)
	// UpdateMCPToolPurpose 标记 MCP 服务中一个工具的用途。
	//appservice:route PUT /settings/mcp-servers/:mcpServerID/tool-purpose
	UpdateMCPToolPurpose(context.Context, RequestMeta, string, MCPToolPurposeInput) (MCPServer, error)
	// DeleteMCPServer 删除 MCP 服务。
	//appservice:route DELETE /settings/mcp-servers/:mcpServerID
	DeleteMCPServer(context.Context, RequestMeta, string) error
	// UpdateOrganization 修改当前工作区的名称。
	//appservice:route PUT /settings/organization
	UpdateOrganization(context.Context, RequestMeta, OrganizationInput) (Organization, error)
	// GetCustomerIdentitySecret 读取当前企业的客户身份密钥，未生成时为空。
	//appservice:route GET /settings/customer-service/identity-secret
	GetCustomerIdentitySecret(context.Context, RequestMeta) (CustomerIdentitySecret, error)
	// RegenerateCustomerIdentitySecret 生成或重新生成当前企业的客户身份密钥，旧密钥立即失效。
	//appservice:route POST /settings/customer-service/identity-secret
	RegenerateCustomerIdentitySecret(context.Context, RequestMeta) (CustomerIdentitySecret, error)
	// GetBusinessHours 读取当前企业的客服工作时间。
	//appservice:route GET /settings/customer-service/business-hours
	GetBusinessHours(context.Context, RequestMeta) (BusinessHours, error)
	// UpdateBusinessHours 修改当前企业的客服工作时间。
	//appservice:route PUT /settings/customer-service/business-hours
	UpdateBusinessHours(context.Context, RequestMeta, BusinessHours) (BusinessHours, error)
	// GetServiceTimeouts 读取当前企业的客服超时时长。
	//appservice:route GET /settings/customer-service/timeouts
	GetServiceTimeouts(context.Context, RequestMeta) (ServiceTimeouts, error)
	// UpdateServiceTimeouts 修改当前企业的客服超时时长。
	//appservice:route PUT /settings/customer-service/timeouts
	UpdateServiceTimeouts(context.Context, RequestMeta, ServiceTimeouts) (ServiceTimeouts, error)
	// GetServiceSummarySettings 读取当前企业的周期小结设置。
	//appservice:route GET /settings/customer-service/summary
	GetServiceSummarySettings(context.Context, RequestMeta) (ServiceSummarySettings, error)
	// UpdateServiceSummarySettings 修改当前企业的周期小结设置。
	//appservice:route PUT /settings/customer-service/summary
	UpdateServiceSummarySettings(context.Context, RequestMeta, ServiceSummarySettings) (ServiceSummarySettings, error)
	// GetTranslationSettings 读取当前企业的翻译设置。
	//appservice:route GET /settings/customer-service/translation
	GetTranslationSettings(context.Context, RequestMeta) (TranslationSettings, error)
	// UpdateTranslationSettings 修改当前企业的翻译设置。
	//appservice:route PUT /settings/customer-service/translation
	UpdateTranslationSettings(context.Context, RequestMeta, TranslationSettings) (TranslationSettings, error)
	// ListServiceCategories 返回当前企业的咨询分类目录。
	//appservice:route GET /settings/customer-service/categories
	ListServiceCategories(context.Context, RequestMeta) (ServiceCategoryList, error)
	// CreateServiceCategory 新增咨询分类。
	//appservice:route POST /settings/customer-service/categories status=201
	CreateServiceCategory(context.Context, RequestMeta, ServiceCategoryInput) (ServiceCategory, error)
	// UpdateServiceCategory 修改咨询分类。
	//appservice:route PUT /settings/customer-service/categories/:categoryID
	UpdateServiceCategory(context.Context, RequestMeta, string, ServiceCategoryInput) (ServiceCategory, error)
	// DeleteServiceCategory 删除咨询分类，历史记录保留分类名称。
	//appservice:route DELETE /settings/customer-service/categories/:categoryID
	DeleteServiceCategory(context.Context, RequestMeta, string) error
	// ListContactFields 返回当前企业的联系人字段。
	//appservice:route GET /settings/customer-service/contact-fields
	ListContactFields(context.Context, RequestMeta) (ContactFieldList, error)
	// CreateContactField 新增联系人字段。
	//appservice:route POST /settings/customer-service/contact-fields status=201
	CreateContactField(context.Context, RequestMeta, ContactFieldInput) (ContactField, error)
	// UpdateContactField 修改联系人字段，被移除的单选选项对应的取值随之清空。
	//appservice:route PUT /settings/customer-service/contact-fields/:fieldID
	UpdateContactField(context.Context, RequestMeta, string, ContactFieldInput) (ContactField, error)
	// DeleteContactField 删除联系人字段及其全部取值。
	//appservice:route DELETE /settings/customer-service/contact-fields/:fieldID
	DeleteContactField(context.Context, RequestMeta, string) error
	// ListContactTags 返回当前企业的联系人标签。
	//appservice:route GET /settings/customer-service/contact-tags
	ListContactTags(context.Context, RequestMeta) (ContactTagList, error)
	// CreateContactTag 新增联系人标签。
	//appservice:route POST /settings/customer-service/contact-tags status=201
	CreateContactTag(context.Context, RequestMeta, ContactTagInput) (ContactTag, error)
	// UpdateContactTag 修改联系人标签。
	//appservice:route PUT /settings/customer-service/contact-tags/:tagID
	UpdateContactTag(context.Context, RequestMeta, string, ContactTagInput) (ContactTag, error)
	// DeleteContactTag 删除联系人标签并从所有联系人上移除。
	//appservice:route DELETE /settings/customer-service/contact-tags/:tagID
	DeleteContactTag(context.Context, RequestMeta, string) error
	// GetAIPerformanceReport 返回当前企业指定范围内的 AI 客服表现概览。
	//appservice:route GET /reports/ai-performance
	GetAIPerformanceReport(context.Context, RequestMeta, AIPerformanceReportInput) (AIPerformanceReport, error)
	// ListAIPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页 AI 客服表现。
	//appservice:route GET /reports/ai-performance/breakdowns
	ListAIPerformanceBreakdowns(context.Context, RequestMeta, AIPerformanceBreakdownInput) (AIPerformanceBreakdownList, error)
	// ListAIPerformanceIssues 返回一页指定类型的 AI 表现问题会话。
	//appservice:route GET /reports/ai-performance/issues
	ListAIPerformanceIssues(context.Context, RequestMeta, AIPerformanceIssueListInput) (ServiceIssueList, error)
	// GetTeamPerformanceReport 返回当前企业指定范围内的真人客服表现概览。
	//appservice:route GET /reports/team-performance
	GetTeamPerformanceReport(context.Context, RequestMeta, TeamPerformanceReportInput) (TeamPerformanceReport, error)
	// ListTeamPerformanceMembers 返回按客服拆分的一页真人客服表现。
	//appservice:route GET /reports/team-performance/members
	ListTeamPerformanceMembers(context.Context, RequestMeta, TeamPerformanceMemberListInput) (TeamPerformanceMemberList, error)
	// ListTeamPerformanceBreakdowns 返回按渠道或咨询分类拆分的一页真人客服表现。
	//appservice:route GET /reports/team-performance/breakdowns
	ListTeamPerformanceBreakdowns(context.Context, RequestMeta, TeamPerformanceBreakdownInput) (TeamPerformanceBreakdownList, error)
	// ListTeamPerformanceIssues 返回一页指定类型的真人接待问题会话。
	//appservice:route GET /reports/team-performance/issues
	ListTeamPerformanceIssues(context.Context, RequestMeta, TeamPerformanceIssueListInput) (ServiceIssueList, error)
	// GetServiceIssue 返回客服周期的质检结论与对客沟通。
	//appservice:route GET /reports/issues/:serviceSessionID
	GetServiceIssue(context.Context, RequestMeta, string) (ServiceIssueDetail, error)
	// ListAgentServiceSessions 返回 AI 员工接待的一页服务周期。
	//appservice:route GET /agents/:agentID/service-sessions
	ListAgentServiceSessions(context.Context, RequestMeta, string, AgentServiceSessionListInput) (AgentServiceSessionList, error)
	// ListKnowledgeGaps 返回一页指定处理状态的待补知识。
	//appservice:route GET /knowledge-gaps
	ListKnowledgeGaps(context.Context, RequestMeta, KnowledgeGapListInput) (KnowledgeGapList, error)
	// GetKnowledgeGap 返回待补知识详情。
	//appservice:route GET /knowledge-gaps/:gapID
	GetKnowledgeGap(context.Context, RequestMeta, string) (KnowledgeGap, error)
	// AcceptKnowledgeGap 把待补知识整理的问答加入知识库。
	//appservice:route POST /knowledge-gaps/:gapID/accept
	AcceptKnowledgeGap(context.Context, RequestMeta, string, KnowledgeGapAcceptInput) error
	// AddServiceIssueToEvaluation 把应转人工未转的问题会话以选定的客户消息为提问加入负责 AI 员工的评测。
	//appservice:route POST /reports/issues/:serviceSessionID/evaluation status=201
	AddServiceIssueToEvaluation(context.Context, RequestMeta, string, ServiceIssueEvaluationInput) (AgentEvaluationCase, error)
	// DismissKnowledgeGap 忽略待补知识。
	//appservice:route POST /knowledge-gaps/:gapID/dismiss
	DismissKnowledgeGap(context.Context, RequestMeta, string) error

	// RegisterDevice 注册当前用户的本机设备。
	//appservice:route POST /devices
	RegisterDevice(context.Context, RequestMeta, DeviceRegistrationInput) (Device, error)
	// ListDevices 返回当前用户已注册的设备。
	//appservice:route GET /devices
	ListDevices(context.Context, RequestMeta) (DeviceList, error)
	// RevokeDevice 撤销当前用户的设备。
	//appservice:route DELETE /devices/:deviceID
	RevokeDevice(context.Context, RequestMeta, string) error
}

// WorkspaceInstaller 由服务端 Backend 实现，用于首次安装。
type WorkspaceInstaller interface {
	InstallWorkspace(context.Context, RequestMeta, InstallWorkspaceInput) (Auth, error)
}

// ServerConnector 由原生端 Backend 实现，用于企业服务器地址。
type ServerConnector interface {
	ServerURL(context.Context, RequestMeta) (string, error)
	ProbeServer(context.Context, RequestMeta, string) (InstallationStatus, error)
	ConnectServer(context.Context, RequestMeta, string) error
}

// RealtimeConnector 由持有企业服务器实时事件流的原生端后端实现。
type RealtimeConnector interface {
	ConnectRealtime(context.Context, RequestMeta) (RealtimeConnection, error)
	DisconnectRealtime(context.Context, RequestMeta, string) error
	ConnectAgentRunStream(context.Context, RequestMeta, string) (RealtimeConnection, error)
	DisconnectAgentRunStream(context.Context, RequestMeta, string) error
	ConnectWorkspaceActivity(context.Context, RequestMeta) (RealtimeConnection, error)
	DisconnectWorkspaceActivity(context.Context, RequestMeta, string) error
}

// ImageSelector 由支持原生文件对话框的平台实现。
type ImageSelector interface {
	SelectImage(context.Context, RequestMeta) (ImageFile, error)
}

// ConversationWindowOpener 由支持多窗口的平台实现，在独立窗口打开指定会话。
type ConversationWindowOpener interface {
	OpenConversationWindow(context.Context, RequestMeta, ConversationWindowInput) error
}

// LocalDeviceReporter 由把本机注册为设备的原生端实现，报告设备注册状态与 Agent 运行环境的准备状态。
type LocalDeviceReporter interface {
	CurrentDevice(context.Context, RequestMeta) (LocalDevice, error)
}

// LocalEnvironmentManager 由为个人 AI 员工提供本机运行环境、本地 MCP 服务与技能的原生端实现。
type LocalEnvironmentManager interface {
	LocalEnvironment(context.Context, RequestMeta) (LocalEnvironment, error)
	UpdateLocalToolchain(context.Context, RequestMeta) (LocalToolchainUpdate, error)
	UninstallLocalToolchain(context.Context, RequestMeta) error
	InstallLocalToolchain(context.Context, RequestMeta) error
	OpenLocalToolchainFolder(context.Context, RequestMeta) error
	RemoveLocalMCPServer(context.Context, RequestMeta, string) error
	RemoveLocalSkill(context.Context, RequestMeta, string) error
}

// NativeLocaleUpdater 同步当前设备上的原生界面语言。
type NativeLocaleUpdater interface {
	SetLocale(Locale)
}

// NativeServerLink 由原生端实现连接链接的接收。
type NativeServerLink interface {
	// TakeOpenedServerLink 返回并清除最近一次唤起应用的连接链接携带的部署地址，没有时返回空串。
	TakeOpenedServerLink(context.Context, RequestMeta) (string, error)
}

// NativeNotification 由原生端实现系统通知权限和消息投递。
type NativeNotification interface {
	CheckNotificationPermission(context.Context, RequestMeta) (NotificationPermissionStatus, error)
	RequestNotificationPermission(context.Context, RequestMeta) (NotificationPermissionStatus, error)
	SendMessageNotification(context.Context, RequestMeta, MessageNotificationInput) error
	// TakeOpenedNotificationPath 返回并清除最近一次被点击的通知要打开的页面地址，没有时返回空串。
	TakeOpenedNotificationPath(context.Context, RequestMeta) (string, error)
}
