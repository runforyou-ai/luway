package domain

// InboxScope 定义会话列表读取范围：pending 为需要本人处理的服务会话，all 为全部服务会话，chat 为本人参与的群聊与单聊。
type InboxScope string

const (
	InboxScopePending InboxScope = "pending"
	InboxScopeAll     InboxScope = "all"
	InboxScopeChat    InboxScope = "chat"
)

// InboxPendingKind 定义待处理条目的类型：reply 为等我回复，queue 为待领取，mention 为内部备注提醒本人。
type InboxPendingKind string

const (
	InboxPendingKindReply   InboxPendingKind = "reply"
	InboxPendingKindQueue   InboxPendingKind = "queue"
	InboxPendingKindMention InboxPendingKind = "mention"
)

// InboxAssigneeFilter 定义服务会话的负责人筛选：all 为不限，unassigned 为未分配，identity 为指定企业身份。
type InboxAssigneeFilter string

const (
	InboxAssigneeFilterAll        InboxAssigneeFilter = "all"
	InboxAssigneeFilterUnassigned InboxAssigneeFilter = "unassigned"
	InboxAssigneeFilterIdentity   InboxAssigneeFilter = "identity"
)

// ServiceQueueFilter 定义待领取条目的队列筛选：all 为本人可领取的全部队列，public 为公共队列，team 为指定团队。
type ServiceQueueFilter string

const (
	ServiceQueueFilterAll    ServiceQueueFilter = "all"
	ServiceQueueFilterPublic ServiceQueueFilter = "public"
	ServiceQueueFilterTeam   ServiceQueueFilter = "team"
)

// InboxPartition 定义统一收件箱的分区：pinned 为置顶区，regular 为未置顶区，all 为按完整活动序排列的全部会话；archived 为本人已归档的聊天，只适用于聊天范围，聊天范围的其余分区不含这些聊天。
type InboxPartition string

const (
	InboxPartitionAll      InboxPartition = "all"
	InboxPartitionPinned   InboxPartition = "pinned"
	InboxPartitionRegular  InboxPartition = "regular"
	InboxPartitionArchived InboxPartition = "archived"
)

// ConversationPinPosition 定义置顶顺序中的落点：before 与 after 相对邻居会话，start 与 end 指整个置顶区的首尾。
type ConversationPinPosition string

const (
	ConversationPinPositionBefore ConversationPinPosition = "before"
	ConversationPinPositionAfter  ConversationPinPosition = "after"
	ConversationPinPositionStart  ConversationPinPosition = "start"
	ConversationPinPositionEnd    ConversationPinPosition = "end"
)
