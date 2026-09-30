package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// ServiceSessionStatus 表示客服处理状态。
type ServiceSessionStatus string

const (
	ServiceSessionStatusOpen   ServiceSessionStatus = ServiceSessionStatus(domain.ServiceSessionStatusOpen)
	ServiceSessionStatusClosed ServiceSessionStatus = ServiceSessionStatus(domain.ServiceSessionStatusClosed)
)

// AgentRunStatus 表示会话中 Agent 最近一次运行状态。
type AgentRunStatus string

const (
	AgentRunStatusQueued    AgentRunStatus = AgentRunStatus(domain.AgentRunStatusQueued)
	AgentRunStatusRunning   AgentRunStatus = AgentRunStatus(domain.AgentRunStatusRunning)
	AgentRunStatusSucceeded AgentRunStatus = AgentRunStatus(domain.AgentRunStatusSucceeded)
	AgentRunStatusFailed    AgentRunStatus = AgentRunStatus(domain.AgentRunStatusFailed)
	AgentRunStatusCancelled AgentRunStatus = AgentRunStatus(domain.AgentRunStatusCancelled)
)

// InboxScope 表示会话列表读取范围：pending 为待处理服务会话，all 为全部服务会话，chat 为本人参与的群聊与单聊。
type InboxScope string

const (
	InboxScopePending InboxScope = InboxScope(domain.InboxScopePending)
	InboxScopeAll     InboxScope = InboxScope(domain.InboxScopeAll)
	InboxScopeChat    InboxScope = InboxScope(domain.InboxScopeChat)
)

// InboxPendingKind 表示待处理条目的类型：reply 为等我回复，queue 为待领取，mention 为内部备注提醒本人。
type InboxPendingKind string

const (
	InboxPendingKindReply   InboxPendingKind = InboxPendingKind(domain.InboxPendingKindReply)
	InboxPendingKindQueue   InboxPendingKind = InboxPendingKind(domain.InboxPendingKindQueue)
	InboxPendingKindMention InboxPendingKind = InboxPendingKind(domain.InboxPendingKindMention)
)

// InboxAssigneeFilter 表示服务会话的负责人筛选：all 为不限，unassigned 为未分配，identity 为指定企业身份。
type InboxAssigneeFilter string

const (
	InboxAssigneeFilterAll        InboxAssigneeFilter = InboxAssigneeFilter(domain.InboxAssigneeFilterAll)
	InboxAssigneeFilterUnassigned InboxAssigneeFilter = InboxAssigneeFilter(domain.InboxAssigneeFilterUnassigned)
	InboxAssigneeFilterIdentity   InboxAssigneeFilter = InboxAssigneeFilter(domain.InboxAssigneeFilterIdentity)
)

// ServiceAudience 表示服务对象：customer 为外部客户，employee 为本企业员工，partner 为伙伴。
type ServiceAudience string

const (
	ServiceAudienceCustomer ServiceAudience = ServiceAudience(domain.ServiceAudienceCustomer)
	ServiceAudienceEmployee ServiceAudience = ServiceAudience(domain.ServiceAudienceEmployee)
	ServiceAudiencePartner  ServiceAudience = ServiceAudience(domain.ServiceAudiencePartner)
)

// ServiceSource 表示服务会话来源：channel 为渠道，direct 为单聊。
type ServiceSource string

const (
	ServiceSourceChannel ServiceSource = ServiceSource(domain.ServiceSourceChannel)
	ServiceSourceDirect  ServiceSource = ServiceSource(domain.ServiceSourceDirect)
)

// InboxPartition 表示统一收件箱的置顶分区。
type InboxPartition string

const (
	InboxPartitionAll     InboxPartition = InboxPartition(domain.InboxPartitionAll)
	InboxPartitionPinned  InboxPartition = InboxPartition(domain.InboxPartitionPinned)
	InboxPartitionRegular InboxPartition = InboxPartition(domain.InboxPartitionRegular)
)

// ConversationPinPosition 表示置顶顺序中的落点：before 与 after 相对邻居会话，start 与 end 指整个置顶区的首尾。
type ConversationPinPosition string

const (
	ConversationPinPositionBefore ConversationPinPosition = ConversationPinPosition(domain.ConversationPinPositionBefore)
	ConversationPinPositionAfter  ConversationPinPosition = ConversationPinPosition(domain.ConversationPinPositionAfter)
	ConversationPinPositionStart  ConversationPinPosition = ConversationPinPosition(domain.ConversationPinPositionStart)
	ConversationPinPositionEnd    ConversationPinPosition = ConversationPinPosition(domain.ConversationPinPositionEnd)
)

// ConversationPinInput 定义个人置顶写入；position 为空表示新置顶追加到末尾、已置顶保持原位，取消置顶不接受位置指令。
type ConversationPinInput struct {
	Pinned                  bool                    `json:"pinned"`
	NeighborID              string                  `json:"neighborId"`
	Position                ConversationPinPosition `json:"position"`
	ExpectedPinOrderVersion string                  `json:"expectedPinOrderVersion"`
}

// ConversationPinState 返回写入后的个人置顶事实与顺序版本。
type ConversationPinState struct {
	Pinned          bool   `json:"pinned"`
	PinOrderVersion string `json:"pinOrderVersion"`
}

// ArchivedConversationListInput 定义已归档聊天列表的类型、名称搜索与页码；kind 为空表示群聊、单聊与 AI 聊天都包含。
type ArchivedConversationListInput struct {
	Kind     ConversationType `json:"kind" query:"kind"`
	Search   string           `json:"search" query:"search"`
	Page     int              `json:"page" query:"page,default=1"`
	PageSize int              `json:"pageSize" query:"pageSize,default=50"`
}

// ArchivedConversationList 定义按最近活动倒序排列的一页已归档聊天。
type ArchivedConversationList struct {
	Conversations []InboxConversation `json:"conversations"`
	Page          PageInfo            `json:"page"`
}

// ServiceQueueFilter 表示待领取条目的队列筛选。
type ServiceQueueFilter string

const (
	ServiceQueueFilterAll    ServiceQueueFilter = ServiceQueueFilter(domain.ServiceQueueFilterAll)
	ServiceQueueFilterPublic ServiceQueueFilter = ServiceQueueFilter(domain.ServiceQueueFilterPublic)
	ServiceQueueFilterTeam   ServiceQueueFilter = ServiceQueueFilter(domain.ServiceQueueFilterTeam)
)

// InboxQuery 定义与分页边界无关的会话列表范围与筛选；pendingKind 与队列筛选只在待处理范围生效，pendingKind 为 mention 时包含同时等我回复或待领取但有提醒本人的条目，服务状态与负责人筛选只在全部范围生效，渠道、来源与服务对象在两个服务会话范围生效且渠道只与渠道来源组合，kinds 只在聊天范围生效；search 非空时按会话名称搜索，searchRange 为 list 时沿用列表范围，为 readable 时覆盖全部可读会话且不带范围与筛选。
type InboxQuery struct {
	Partition          InboxPartition       `json:"partition" query:"partition"`
	Scope              InboxScope           `json:"scope" query:"scope"`
	PendingKind        InboxPendingKind     `json:"pendingKind" query:"pendingKind"`
	QueueFilter        ServiceQueueFilter   `json:"queueFilter" query:"queueFilter"`
	QueueTeamID        string               `json:"queueTeamId" query:"queueTeamId"`
	ChannelID          string               `json:"channelId" query:"channelId"`
	Source             ServiceSource        `json:"source" query:"source"`
	Audience           ServiceAudience      `json:"audience" query:"audience"`
	ServiceStatus      ServiceSessionStatus `json:"serviceStatus" query:"serviceStatus"`
	AssigneeFilter     InboxAssigneeFilter  `json:"assigneeFilter" query:"assigneeFilter"`
	AssigneeIdentityID string               `json:"assigneeIdentityId" query:"assigneeIdentityId"`
	Kinds              []ConversationType   `json:"kinds" query:"kinds"`
	Search             string               `json:"search" query:"search"`
	SearchRange        InboxSearchRange     `json:"searchRange" query:"searchRange"`
}

// LoadInboxInput 定义会话列表范围、筛选、会话名称搜索和分页边界。
type LoadInboxInput struct {
	Partition          InboxPartition       `json:"partition" query:"partition"`
	Scope              InboxScope           `json:"scope" query:"scope"`
	PendingKind        InboxPendingKind     `json:"pendingKind" query:"pendingKind"`
	QueueFilter        ServiceQueueFilter   `json:"queueFilter" query:"queueFilter"`
	QueueTeamID        string               `json:"queueTeamId" query:"queueTeamId"`
	ChannelID          string               `json:"channelId" query:"channelId"`
	Source             ServiceSource        `json:"source" query:"source"`
	Audience           ServiceAudience      `json:"audience" query:"audience"`
	ServiceStatus      ServiceSessionStatus `json:"serviceStatus" query:"serviceStatus"`
	AssigneeFilter     InboxAssigneeFilter  `json:"assigneeFilter" query:"assigneeFilter"`
	AssigneeIdentityID string               `json:"assigneeIdentityId" query:"assigneeIdentityId"`
	Kinds              []ConversationType   `json:"kinds" query:"kinds"`
	Search             string               `json:"search" query:"search"`
	SearchRange        InboxSearchRange     `json:"searchRange" query:"searchRange"`
	Cursor             string               `json:"cursor" query:"cursor"`
	BeforeCursor       string               `json:"beforeCursor" query:"beforeCursor"`
	Limit              int                  `json:"limit" query:"limit,default=50"`
}

// InboxAssignee 定义客户会话负责人摘要。
type InboxAssignee struct {
	IdentityID  string                   `json:"identityId"`
	Type        OrganizationIdentityType `json:"type"`
	DisplayName string                   `json:"displayName"`
	AvatarURL   string                   `json:"avatarUrl"`
}

// ServiceAssigneeList 定义客服筛选候选列表。
type ServiceAssigneeList struct {
	Assignees []InboxAssignee `json:"assignees"`
}

// ServiceQueueTeam 定义可作为客服队列的团队。
type ServiceQueueTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Mine 表示当前成员属于该团队。
	Mine bool `json:"mine"`
	// Available 表示团队内有开启接待的真人成员，可以承接队列会话。
	Available bool `json:"available"`
}

// ServiceQueueTeamList 定义客服队列团队列表。
type ServiceQueueTeamList struct {
	Teams []ServiceQueueTeam `json:"teams"`
}

// InboxChannel 定义收件箱渠道筛选候选。
type InboxChannel struct {
	ID      string      `json:"id"`
	Type    ChannelType `json:"type"`
	Name    string      `json:"name"`
	Enabled bool        `json:"enabled"`
}

// InboxChannelList 定义收件箱渠道筛选候选列表。
type InboxChannelList struct {
	Channels []InboxChannel `json:"channels"`
}

// ConversationType 表示会话类型，Copilot 线程只在所属客户会话的 AI 助手中出现，不进入统一收件箱。
type ConversationType string

const (
	ConversationTypeChannel ConversationType = ConversationType(domain.ConversationTypeChannel)
	ConversationTypeDirect  ConversationType = ConversationType(domain.ConversationTypeDirect)
	ConversationTypeAgent   ConversationType = ConversationType(domain.ConversationTypeAgent)
	ConversationTypeGroup   ConversationType = ConversationType(domain.ConversationTypeGroup)
	ConversationTypeCopilot ConversationType = ConversationType(domain.ConversationTypeCopilot)
)

// ServiceInboxChannel 定义服务会话的来源渠道及其外发附件能力。
type ServiceInboxChannel struct {
	Type ChannelType `json:"type"`
	Name string      `json:"name"`
	// AttachmentSupported 表示来源渠道当前支持向发起人发送附件。
	AttachmentSupported bool `json:"attachmentSupported"`
	// AttachmentByteLimit 是来源渠道单个外发附件的字节上限。
	AttachmentByteLimit int64 `json:"attachmentByteLimit"`
	// AttachmentCaptionLimit 是来源渠道附件说明的字符上限。
	AttachmentCaptionLimit int `json:"attachmentCaptionLimit"`
}

// ServiceInboxConversation 定义服务会话摘要；渠道只对渠道来源存在。
type ServiceInboxConversation struct {
	Title                  string          `json:"title"`
	Source                 ServiceSource   `json:"source"`
	Audience               ServiceAudience `json:"audience"`
	RequesterName          *string         `json:"requesterName"`
	RequesterContactNumber *int64          `json:"requesterContactNumber"`
	RequesterAvatarURL     string          `json:"requesterAvatarUrl"`
	// RequesterChatSubjectID 是发起人在会话中的聊天主体编号。
	RequesterChatSubjectID string `json:"requesterChatSubjectId"`
	// AssigneeChatSubjectID 是当前负责人的聊天主体编号，负责人尚未参与聊天时为空。
	AssigneeChatSubjectID *string              `json:"assigneeChatSubjectId"`
	Channel               *ServiceInboxChannel `json:"channel"`
	// AgentIdentityID 与 AgentName 是单聊中接待发起人的 AI 员工，其他来源为空。
	AgentIdentityID *string `json:"agentIdentityId"`
	AgentName       *string `json:"agentName"`
	// Preview 是末条消息的单行纯文本摘要。
	Preview *string `json:"preview"`
	// PreviewVisibility 标明摘要取自对客消息还是内部备注。
	PreviewVisibility    *MessageVisibility   `json:"previewVisibility"`
	LastMessageAt        *time.Time           `json:"lastMessageAt"`
	ServiceSessionStatus ServiceSessionStatus `json:"serviceSessionStatus"`
	ServiceSessionID     string               `json:"serviceSessionId"`
	Assignee             *InboxAssignee       `json:"assignee"`
	// TeamID 与 TeamName 是处理周期所属的团队队列，为空表示公共队列。
	TeamID   *string `json:"teamId"`
	TeamName *string `json:"teamName"`
	// UnansweredMentionCount 是当前客服周期内被提醒成员尚未在会话中发言的内部提醒数。
	UnansweredMentionCount int `json:"unansweredMentionCount"`
}

// DirectInboxConversation 定义内部单聊摘要。
type DirectInboxConversation struct {
	PeerIdentityID string                   `json:"peerIdentityId"`
	PeerType       OrganizationIdentityType `json:"peerType"`
	PeerName       string                   `json:"peerName"`
	PeerAvatarURL  string                   `json:"peerAvatarUrl"`
	PeerStatus     UserStatus               `json:"peerStatus"`
	PeerWorkStatus WorkStatus               `json:"peerWorkStatus"`
	// Preview 是末条消息的单行纯文本摘要。
	Preview       *string    `json:"preview"`
	LastMessageAt *time.Time `json:"lastMessageAt"`
}

// AgentInboxConversation 定义 AI 聊天摘要。
type AgentInboxConversation struct {
	Title             string                   `json:"title"`
	AgentIdentityID   string                   `json:"agentIdentityId"`
	AgentName         string                   `json:"agentName"`
	AgentAvatarURL    string                   `json:"agentAvatarUrl"`
	AgentStatus       UserStatus               `json:"agentStatus"`
	AgentType         OrganizationIdentityType `json:"agentType"`
	AssistantPresence *AssistantPresence       `json:"assistantPresence"`
	// Preview 是末条消息的单行纯文本摘要。
	Preview        *string         `json:"preview"`
	LastMessageAt  *time.Time      `json:"lastMessageAt"`
	AgentRunStatus *AgentRunStatus `json:"agentRunStatus"`
	// ServiceOpen 表示该 AI 聊天有进行中的服务周期，AI 员工停用后发起人仍可继续发言。
	ServiceOpen bool `json:"serviceOpen"`
}

// GroupInboxConversation 定义企业群聊摘要。
type GroupInboxConversation struct {
	Title    string             `json:"title"`
	ImageURL string             `json:"imageUrl"`
	Status   ConversationStatus `json:"status"`
	// Preview 是末条消息的单行纯文本摘要。
	Preview       *string    `json:"preview"`
	LastMessageAt *time.Time `json:"lastMessageAt"`
	MemberCount   int        `json:"memberCount"`
	// MemberPreviewNames 是除查看者外按入群先后排列的前几名在群成员名称，用于显示未命名的群。
	MemberPreviewNames []string `json:"memberPreviewNames"`
}

// InboxPendingItem 定义待处理条目的类型、等待起点，以及当前周期内是否有提醒本人且尚未回应的内部备注。
type InboxPendingItem struct {
	Kind      InboxPendingKind `json:"kind"`
	Since     time.Time        `json:"since"`
	Mentioned bool             `json:"mentioned"`
}

// InboxConversation 定义成员统一收件箱列表项。
type InboxConversation struct {
	// PositionCursor 保存列表窗口和匹配锚点的查询位置。
	PositionCursor       string           `json:"positionCursor"`
	LastActivityAt       *time.Time       `json:"lastActivityAt"`
	LastMessageType      *MessageType     `json:"lastMessageType"`
	ID                   string           `json:"id"`
	Type                 ConversationType `json:"type"`
	UnreadCount          int              `json:"unreadCount"`
	MentionedUnreadCount int              `json:"mentionedUnreadCount"`
	MarkedUnread         bool             `json:"markedUnread"`
	Muted                bool             `json:"muted"`
	// Pinned 表示当前用户已把该会话放入个人置顶区。
	Pinned bool `json:"pinned"`
	// ArchivedAt 是当前用户归档群聊、单聊或 AI 聊天的时间，未归档时为空。
	ArchivedAt        *time.Time                `json:"archivedAt"`
	LastMessageID     *string                   `json:"lastMessageId"`
	LastReadMessageID *string                   `json:"lastReadMessageId"`
	Agent             *AgentInboxConversation   `json:"agent"`
	Service           *ServiceInboxConversation `json:"service"`
	Direct            *DirectInboxConversation  `json:"direct"`
	Group             *GroupInboxConversation   `json:"group"`
	// Pending 只在待处理范围的列表项中返回。
	Pending *InboxPendingItem `json:"pending"`
}

// Inbox 定义成员收件箱查询结果。
type Inbox struct {
	StartCursor string `json:"startCursor"`
	EndCursor   string `json:"endCursor"`
	// PinOrderVersion 是本人置顶顺序的当前版本，置顶写入以它作为并发校验依据。
	PinOrderVersion      string              `json:"pinOrderVersion"`
	HasBefore            bool                `json:"hasBefore"`
	Conversations        []InboxConversation `json:"conversations"`
	NextCursor           string              `json:"nextCursor"`
	HasMore              bool                `json:"hasMore"`
	UnreadCount          int                 `json:"unreadCount"`
	AttentionUnreadCount int                 `json:"attentionUnreadCount"`
	// PendingCount 是本人全部待处理条目数，不受当前筛选影响。
	PendingCount int `json:"pendingCount"`
	// PendingUnreadCount 是本人待处理条目中的未读消息总数，不受当前筛选影响。
	PendingUnreadCount int `json:"pendingUnreadCount"`
}

// InboxConversationAvailability 表示指定会话的阅读和列表资格。
type InboxConversationAvailability string

const (
	InboxConversationMatching     InboxConversationAvailability = "matching"
	InboxConversationOutsideQuery InboxConversationAvailability = "outside_query"
	InboxConversationUnavailable  InboxConversationAvailability = "unavailable"
)

// ReadInboxConversationsInput 指定待核对的会话及完整列表筛选；未指定范围与搜索词时只核对阅读资格。
type ReadInboxConversationsInput struct {
	ConversationIDs []string   `json:"conversationIds"`
	Query           InboxQuery `json:"query"`
}

// ConversationAttentionInput 定义调用方已知的最后一条消息，为空时只判断会话最新一条消息是否计入提醒。
type ConversationAttentionInput struct {
	AfterMessageID string `json:"afterMessageId" query:"afterMessageId"`
}

// ConversationAttentionMessage 定义计入本人提醒的一条未读消息的通知摘要。
type ConversationAttentionMessage struct {
	ID         string            `json:"id"`
	Type       MessageType       `json:"type"`
	Visibility MessageVisibility `json:"visibility"`
	// Preview 是消息正文的单行纯文本摘要。
	Preview        string  `json:"preview"`
	AttachmentName *string `json:"attachmentName"`
	SenderName     *string `json:"senderName"`
}

// ConversationAttention 返回会话摘要与其中计入本人提醒的未读消息，消息按会话顺序排列。
type ConversationAttention struct {
	Conversation InboxConversation              `json:"conversation"`
	Messages     []ConversationAttentionMessage `json:"messages"`
}

// InboxConversationResult 返回匹配状态，不可用时保留请求 ID 和资格。
type InboxConversationResult struct {
	ID           string                        `json:"id"`
	Availability InboxConversationAvailability `json:"availability"`
	Conversation *InboxConversation            `json:"conversation"`
}

// InboxConversationResults 按请求顺序返回每项结果。
type InboxConversationResults struct {
	Results []InboxConversationResult `json:"results"`
}

// InboxContextInput 按会话当前位置或原查询位置读取邻域，前后数量零值均为二十五。
type InboxContextInput struct {
	Query        InboxQuery `json:"query"`
	AnchorID     string     `json:"anchorId"`
	AnchorCursor string     `json:"anchorCursor"`
	BeforeLimit  int        `json:"beforeLimit"`
	AfterLimit   int        `json:"afterLimit"`
}

// InboxWindowInput 指定同一查询已加载范围的首尾游标，包含两侧边界。
type InboxWindowInput struct {
	Query       InboxQuery `json:"query"`
	StartCursor string     `json:"startCursor"`
	EndCursor   string     `json:"endCursor"`
}

// InboxWindow 保存连续范围和双向续读位置，空范围仍可保留原边界。
type InboxWindow struct {
	Conversations   []InboxConversation `json:"conversations"`
	StartCursor     string              `json:"startCursor"`
	EndCursor       string              `json:"endCursor"`
	PinOrderVersion string              `json:"pinOrderVersion"`
	HasBefore       bool                `json:"hasBefore"`
	HasAfter        bool                `json:"hasAfter"`
}

// InboxContext 独立返回锚点资格，列表只渲染 Window，Anchor 可与窗口行重叠。
type InboxContext struct {
	Anchor InboxConversationResult `json:"anchor"`
	Window InboxWindow             `json:"window"`
}

// SyncHeads 保存同步探针的不透明比较值，客户端只判断与上次返回是否相同。
type SyncHeads struct {
	ConversationCount      int    `json:"conversationCount"`
	ConversationChecksum   string `json:"conversationChecksum"`
	IdentityProfileVersion string `json:"identityProfileVersion"`
	PinOrderVersion        string `json:"pinOrderVersion"`
}

// InboxSearchRange 表示收件箱检索范围。
type InboxSearchRange string

const (
	InboxSearchRangeList         InboxSearchRange = "list"
	InboxSearchRangeReadable     InboxSearchRange = "readable"
	InboxSearchRangeConversation InboxSearchRange = "conversation"
)

// InboxSearchInput 定义检索文本与范围；列表范围与筛选只在 list 范围生效，会话编号只在 conversation 范围生效。
type InboxSearchInput struct {
	Query              string               `json:"query" query:"query"`
	Range              InboxSearchRange     `json:"range" query:"range"`
	ConversationID     string               `json:"conversationId" query:"conversationId"`
	Scope              InboxScope           `json:"scope" query:"scope"`
	PendingKind        InboxPendingKind     `json:"pendingKind" query:"pendingKind"`
	QueueFilter        ServiceQueueFilter   `json:"queueFilter" query:"queueFilter"`
	QueueTeamID        string               `json:"queueTeamId" query:"queueTeamId"`
	ChannelID          string               `json:"channelId" query:"channelId"`
	Source             ServiceSource        `json:"source" query:"source"`
	Audience           ServiceAudience      `json:"audience" query:"audience"`
	ServiceStatus      ServiceSessionStatus `json:"serviceStatus" query:"serviceStatus"`
	AssigneeFilter     InboxAssigneeFilter  `json:"assigneeFilter" query:"assigneeFilter"`
	AssigneeIdentityID string               `json:"assigneeIdentityId" query:"assigneeIdentityId"`
	Kinds              []ConversationType   `json:"kinds" query:"kinds"`
}

// InboxSearchSegment 表示摘要中的一段文字及其是否命中。
type InboxSearchSegment struct {
	Text  string `json:"text"`
	Match bool   `json:"match"`
}

// InboxSearchMessage 表示命中的消息、所在会话和高亮摘要。
type InboxSearchMessage struct {
	ID                  string               `json:"id"`
	Type                MessageType          `json:"type"`
	SenderName          *string              `json:"senderName"`
	SenderContactNumber *int64               `json:"senderContactNumber"`
	OriginatedAt        time.Time            `json:"originatedAt"`
	Excerpt             []InboxSearchSegment `json:"excerpt"`
	Conversation        InboxConversation    `json:"conversation"`
}

// InboxSearchPersonKind 表示人员检索结果的来源。
type InboxSearchPersonKind string

const (
	InboxSearchPersonMember  InboxSearchPersonKind = "member"
	InboxSearchPersonContact InboxSearchPersonKind = "contact"
)

// InboxSearchPerson 表示命中的企业成员或外部联系人；真人成员携带 userId，AI 员工携带 agentId，外部联系人没有客户会话时 conversationId 为空。
type InboxSearchPerson struct {
	Kind           InboxSearchPersonKind     `json:"kind"`
	ID             string                    `json:"id"`
	UserID         *string                   `json:"userId"`
	AgentID        *string                   `json:"agentId"`
	IdentityType   *OrganizationIdentityType `json:"identityType"`
	DisplayName    string                    `json:"displayName"`
	ContactNumber  *int64                    `json:"contactNumber"`
	AvatarURL      string                    `json:"avatarUrl"`
	ConversationID *string                   `json:"conversationId"`
}

// InboxSearchResult 返回会话、消息和人员三组检索结果，每组最多六条。
type InboxSearchResult struct {
	Conversations []InboxConversation  `json:"conversations"`
	Messages      []InboxSearchMessage `json:"messages"`
	People        []InboxSearchPerson  `json:"people"`
}
