//go:build server

package inbox

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support"
)

// PendingSummary 定义待处理条目的类型、等待起点，以及当前周期内是否有提醒本人且尚未回应的内部备注。
type PendingSummary struct {
	Kind      domain.InboxPendingKind
	Since     time.Time
	Mentioned bool
}

// AssigneeSummary 定义客户会话负责人摘要。
type AssigneeSummary struct {
	IdentityID   string
	Type         domain.WorkspaceIdentityType
	DisplayName  string
	AvatarFileID *string
}

// ServiceConversationSummary 定义收件箱中的服务会话详情；渠道只对渠道来源存在。
type ServiceConversationSummary struct {
	Title                  string
	Source                 domain.ServiceSource
	Audience               domain.ServiceAudience
	RequesterName          *string
	RequesterContactNumber *int64
	RequesterAvatarFileID  *string
	RequesterChatSubjectID string
	AssigneeChatSubjectID  *string
	Channel                *ServiceChannelSummary
	// AgentIdentityID 与 AgentName 是单聊中接待发起人的 AI 员工，其他来源为空。
	AgentIdentityID           *string
	AgentName                 *string
	Preview                   *string
	PreviewSenderIdentityType *domain.WorkspaceIdentityType
	PreviewVisibility         *domain.MessageVisibility
	LastMessageAt             *time.Time
	ServiceSessionStatus      domain.ServiceSessionStatus
	ServiceSessionID          string
	Assignee                  *AssigneeSummary
	// TeamID 与 TeamName 是处理周期所属的团队队列，为空表示公共队列。
	TeamID   *string
	TeamName *string
	// UnansweredMentionCount 是当前客服周期内被提醒成员尚未在会话中发言的内部提醒数。
	UnansweredMentionCount int
	// ReplyWindow 是发起人渠道身份当前可用的回复窗口，没有可用窗口时为空。
	ReplyWindow *ReplyWindowSummary
}

// ReplyWindowSummary 定义渠道身份当前可用的回复窗口中到期最晚的一个。
type ReplyWindowSummary struct {
	ExpiresAt time.Time
	// Remaining 是窗口剩余可发送的请求数，不限条数时为空。
	Remaining *int
}

// ServiceChannelSummary 定义服务会话的来源渠道。
type ServiceChannelSummary struct {
	Type domain.ChannelType
	Name string
}

// DirectConversationSummary 定义收件箱中的内部单聊详情。
type DirectConversationSummary struct {
	PeerIdentityID            string
	PeerType                  domain.WorkspaceIdentityType
	PeerName                  string
	PeerAvatarFileID          *string
	PeerStatus                domain.IdentityStatus
	PeerWorkStatus            domain.WorkStatus
	Preview                   *string
	PreviewSenderIdentityType *domain.WorkspaceIdentityType
	LastMessageAt             *time.Time
}

// AgentConversationSummary 定义收件箱中的 AI 聊天详情。
type AgentConversationSummary struct {
	Title             string
	AgentIdentityID   string
	AgentName         string
	AgentAvatarFileID *string
	AgentStatus       domain.IdentityStatus
	// PersonalPresence 是个人 AI 员工的在线状态，其他 AI 员工为空。
	PersonalPresence          domain.PersonalAgentPresence
	Preview                   *string
	PreviewSenderIdentityType *domain.WorkspaceIdentityType
	LastMessageAt             *time.Time
	AgentRunStatus            *domain.AgentRunStatus
	// ServiceOpen 表示该 AI 聊天有进行中的服务周期，AI 员工停用后发起人仍可继续发言。
	ServiceOpen bool
}

// GroupConversationSummary 定义收件箱中的企业群聊详情。
type GroupConversationSummary struct {
	Title                     string
	ImageFileID               *string
	Status                    domain.ConversationStatus
	Preview                   *string
	PreviewSenderIdentityType *domain.WorkspaceIdentityType
	LastMessageAt             *time.Time
	MemberCount               int
	// MemberPreviewNames 是除查看者外按入群先后排列的前几名在群成员名称。
	MemberPreviewNames []string
}

// ConversationSummary 定义统一收件箱会话信封。
type ConversationSummary struct {
	PositionCursor  string
	LastActivityAt  *time.Time
	LastMessageType *domain.MessageType
	// LastSystemEventType 是末条消息为系统事件时的事件类型，其余为空。
	LastSystemEventType  *domain.ConversationSystemEventType
	ID                   string
	Type                 domain.ConversationType
	UnreadCount          int
	MentionedUnreadCount int
	Muted                bool
	MarkedUnread         bool
	Pinned               bool
	// ArchivedAt 是本人归档群聊、单聊或 AI 聊天的时间，未归档时为空。
	ArchivedAt        *time.Time
	LastMessageID     *string
	LastReadMessageID *string
	Service           *ServiceConversationSummary
	Agent             *AgentConversationSummary
	Direct            *DirectConversationSummary
	Group             *GroupConversationSummary
	// Pending 只在待处理范围内返回，给出条目类型与等待起点。
	Pending *PendingSummary
}

// serviceConversationRow 是服务会话摘要查询的一行。
type serviceConversationRow struct {
	ID                        string                              `bun:"id"`
	Type                      domain.ConversationType             `bun:"type"`
	Title                     string                              `bun:"title"`
	Source                    domain.ServiceSource                `bun:"source"`
	Audience                  domain.ServiceAudience              `bun:"audience"`
	RequesterName             *string                             `bun:"requester_name"`
	RequesterContactNumber    *int64                              `bun:"requester_contact_number"`
	RequesterAvatarFileID     *string                             `bun:"requester_avatar_file_id"`
	RequesterChatSubjectID    string                              `bun:"requester_chat_subject_id"`
	AssigneeChatSubjectID     *string                             `bun:"assignee_chat_subject_id"`
	ChannelType               *string                             `bun:"channel_type"`
	ChannelName               *string                             `bun:"channel_name"`
	ReplyWindowExpiresAt      *time.Time                          `bun:"reply_window_expires_at"`
	ReplyWindowRemaining      *int                                `bun:"reply_window_remaining"`
	AgentIdentityID           *string                             `bun:"service_agent_identity_id"`
	AgentName                 *string                             `bun:"service_agent_name"`
	Preview                   *string                             `bun:"preview"`
	PreviewSenderIdentityType *domain.WorkspaceIdentityType       `bun:"preview_sender_identity_type"`
	PreviewVisibility         *domain.MessageVisibility           `bun:"preview_visibility"`
	LastMessageAt             *time.Time                          `bun:"last_message_at"`
	ServiceSessionStatus      string                              `bun:"service_session_status"`
	ServiceSessionID          string                              `bun:"service_session_id"`
	AssigneeIdentityID        *string                             `bun:"assignee_identity_id"`
	AssigneeType              *string                             `bun:"assignee_type"`
	AssigneeDisplayName       *string                             `bun:"assignee_display_name"`
	AssigneeAvatarFileID      *string                             `bun:"assignee_avatar_file_id"`
	TeamID                    *string                             `bun:"team_id"`
	TeamName                  *string                             `bun:"team_name"`
	LastActivityAt            *time.Time                          `bun:"last_activity_at"`
	UnreadCount               int                                 `bun:"unread_count"`
	MentionedUnreadCount      int                                 `bun:"mentioned_unread_count"`
	UnansweredMentionCount    int                                 `bun:"unanswered_mention_count"`
	LastReadMessageID         *string                             `bun:"last_read_message_id"`
	LastMessageID             *string                             `bun:"last_message_id"`
	LastMessageType           *domain.MessageType                 `bun:"last_message_type"`
	LastSystemEventType       *domain.ConversationSystemEventType `bun:"last_system_event_type"`
	Pinned                    bool                                `bun:"pinned"`
}

// directConversationRow 是单聊摘要查询的一行。
type directConversationRow struct {
	ID                        string                              `bun:"id"`
	PeerIdentityID            string                              `bun:"peer_identity_id"`
	PeerType                  string                              `bun:"peer_type"`
	PeerName                  string                              `bun:"peer_name"`
	PeerAvatarFileID          *string                             `bun:"peer_avatar_file_id"`
	PeerStatus                domain.IdentityStatus               `bun:"peer_status"`
	PeerWorkStatus            domain.WorkStatus                   `bun:"peer_work_status"`
	Preview                   *string                             `bun:"preview"`
	PreviewSenderIdentityType *domain.WorkspaceIdentityType       `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                          `bun:"last_message_at"`
	LastActivityAt            *time.Time                          `bun:"last_activity_at"`
	UnreadCount               int                                 `bun:"unread_count"`
	LastMessageID             *string                             `bun:"last_message_id"`
	LastMessageType           *domain.MessageType                 `bun:"last_message_type"`
	LastSystemEventType       *domain.ConversationSystemEventType `bun:"last_system_event_type"`
	LastReadMessageID         *string                             `bun:"last_read_message_id"`
	Muted                     bool                                `bun:"muted"`
	MarkedUnread              bool                                `bun:"marked_unread"`
	Pinned                    bool                                `bun:"pinned"`
	ArchivedAt                *time.Time                          `bun:"archived_at"`
}

// agentConversationRow 是AI 聊天摘要查询的一行。
type agentConversationRow struct {
	Title                     string                              `bun:"title"`
	ID                        string                              `bun:"id"`
	AgentIdentityID           string                              `bun:"agent_identity_id"`
	AgentName                 string                              `bun:"agent_name"`
	AgentAvatarFileID         *string                             `bun:"agent_avatar_file_id"`
	AgentStatus               domain.IdentityStatus               `bun:"agent_status"`
	AgentPersonal             bool                                `bun:"agent_personal"`
	AgentPaused               bool                                `bun:"agent_paused"`
	ServiceOpen               bool                                `bun:"service_open"`
	AgentComputerRevoked      bool                                `bun:"agent_computer_revoked"`
	AgentComputerOnline       bool                                `bun:"agent_computer_online"`
	Preview                   *string                             `bun:"preview"`
	PreviewSenderIdentityType *domain.WorkspaceIdentityType       `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                          `bun:"last_message_at"`
	AgentRunStatus            *string                             `bun:"agent_run_status"`
	LastActivityAt            *time.Time                          `bun:"last_activity_at"`
	UnreadCount               int                                 `bun:"unread_count"`
	LastMessageID             *string                             `bun:"last_message_id"`
	LastMessageType           *domain.MessageType                 `bun:"last_message_type"`
	LastSystemEventType       *domain.ConversationSystemEventType `bun:"last_system_event_type"`
	LastReadMessageID         *string                             `bun:"last_read_message_id"`
	Muted                     bool                                `bun:"muted"`
	MarkedUnread              bool                                `bun:"marked_unread"`
	Pinned                    bool                                `bun:"pinned"`
	ArchivedAt                *time.Time                          `bun:"archived_at"`
}

// groupConversationRow 是群聊摘要查询的一行。
type groupConversationRow struct {
	ID                        string                              `bun:"id"`
	Title                     string                              `bun:"title"`
	ImageFileID               *string                             `bun:"image_file_id"`
	Status                    string                              `bun:"status"`
	Preview                   *string                             `bun:"preview"`
	PreviewSenderIdentityType *domain.WorkspaceIdentityType       `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                          `bun:"last_message_at"`
	MemberCount               int                                 `bun:"member_count"`
	MemberPreviewNames        []string                            `bun:"member_preview_names,array"`
	LastActivityAt            *time.Time                          `bun:"last_activity_at"`
	UnreadCount               int                                 `bun:"unread_count"`
	MentionedUnreadCount      int                                 `bun:"mentioned_unread_count"`
	LastMessageID             *string                             `bun:"last_message_id"`
	LastMessageType           *domain.MessageType                 `bun:"last_message_type"`
	LastSystemEventType       *domain.ConversationSystemEventType `bun:"last_system_event_type"`
	LastReadMessageID         *string                             `bun:"last_read_message_id"`
	Muted                     bool                                `bun:"muted"`
	MarkedUnread              bool                                `bun:"marked_unread"`
	Pinned                    bool                                `bun:"pinned"`
	ArchivedAt                *time.Time                          `bun:"archived_at"`
}

// summary 将 AI 会话查询结果转换为统一摘要，对象为个人 AI 员工时给出其在线状态。
func (row agentConversationRow) summary() ConversationSummary {
	var personalPresence domain.PersonalAgentPresence
	if row.AgentPersonal {
		personalPresence = domain.ResolvePersonalAgentPresence(row.AgentStatus, row.AgentPaused, row.AgentComputerRevoked, row.AgentComputerOnline)
	}
	agentRunStatus := support.MapPtr(row.AgentRunStatus, func(status string) domain.AgentRunStatus { return domain.AgentRunStatus(status) })
	return ConversationSummary{
		ID: row.ID, Type: domain.ConversationTypeAgent, UnreadCount: row.UnreadCount, Muted: row.Muted, MarkedUnread: row.MarkedUnread, Pinned: row.Pinned, ArchivedAt: row.ArchivedAt, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastSystemEventType: row.LastSystemEventType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Agent: &AgentConversationSummary{
			Title: row.Title, AgentIdentityID: row.AgentIdentityID, AgentName: row.AgentName, AgentAvatarFileID: row.AgentAvatarFileID, AgentStatus: row.AgentStatus,
			PersonalPresence: personalPresence,
			Preview:          row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, LastMessageAt: row.LastMessageAt, AgentRunStatus: agentRunStatus,
			ServiceOpen: row.ServiceOpen,
		},
	}
}

// summary 转换服务会话的统一摘要。
func (row serviceConversationRow) summary() ConversationSummary {
	var assignee *AssigneeSummary
	if row.AssigneeIdentityID != nil && row.AssigneeType != nil && row.AssigneeDisplayName != nil {
		assignee = &AssigneeSummary{IdentityID: *row.AssigneeIdentityID, Type: domain.WorkspaceIdentityType(*row.AssigneeType), DisplayName: *row.AssigneeDisplayName, AvatarFileID: row.AssigneeAvatarFileID}
	}
	var channel *ServiceChannelSummary
	if row.ChannelType != nil && row.ChannelName != nil {
		channel = &ServiceChannelSummary{Type: domain.ChannelType(*row.ChannelType), Name: *row.ChannelName}
	}
	replyWindow := support.MapPtr(row.ReplyWindowExpiresAt, func(expiresAt time.Time) ReplyWindowSummary {
		return ReplyWindowSummary{ExpiresAt: expiresAt, Remaining: row.ReplyWindowRemaining}
	})
	return ConversationSummary{
		ID: row.ID, Type: row.Type, UnreadCount: row.UnreadCount, MentionedUnreadCount: row.MentionedUnreadCount, Pinned: row.Pinned, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastSystemEventType: row.LastSystemEventType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Service: &ServiceConversationSummary{
			Title: row.Title, Source: row.Source, Audience: row.Audience,
			RequesterName: row.RequesterName, RequesterContactNumber: row.RequesterContactNumber, RequesterAvatarFileID: row.RequesterAvatarFileID, RequesterChatSubjectID: row.RequesterChatSubjectID, AssigneeChatSubjectID: row.AssigneeChatSubjectID,
			Channel: channel, AgentIdentityID: row.AgentIdentityID, AgentName: row.AgentName,
			Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, PreviewVisibility: row.PreviewVisibility, LastMessageAt: row.LastMessageAt,
			ServiceSessionID: row.ServiceSessionID, ServiceSessionStatus: domain.ServiceSessionStatus(row.ServiceSessionStatus), Assignee: assignee,
			TeamID: row.TeamID, TeamName: row.TeamName,
			UnansweredMentionCount: row.UnansweredMentionCount, ReplyWindow: replyWindow,
		},
	}
}

// summary 转换真人单聊会话的统一摘要。
func (row directConversationRow) summary() ConversationSummary {
	return ConversationSummary{
		ID: row.ID, Type: domain.ConversationTypeDirect, UnreadCount: row.UnreadCount, Muted: row.Muted, MarkedUnread: row.MarkedUnread, Pinned: row.Pinned, ArchivedAt: row.ArchivedAt, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastSystemEventType: row.LastSystemEventType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Direct: &DirectConversationSummary{
			PeerIdentityID: row.PeerIdentityID, PeerType: domain.WorkspaceIdentityType(row.PeerType), PeerName: row.PeerName, PeerAvatarFileID: row.PeerAvatarFileID, PeerStatus: row.PeerStatus, PeerWorkStatus: row.PeerWorkStatus,
			Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, LastMessageAt: row.LastMessageAt,
		},
	}
}

// summary 转换群聊会话的统一摘要。
func (row groupConversationRow) summary() ConversationSummary {
	return ConversationSummary{
		ID: row.ID, Type: domain.ConversationTypeGroup, UnreadCount: row.UnreadCount, MentionedUnreadCount: row.MentionedUnreadCount, Muted: row.Muted, MarkedUnread: row.MarkedUnread, Pinned: row.Pinned, ArchivedAt: row.ArchivedAt, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastSystemEventType: row.LastSystemEventType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Group: &GroupConversationSummary{
			Title: row.Title, ImageFileID: row.ImageFileID, Status: domain.ConversationStatus(row.Status), Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType,
			LastMessageAt: row.LastMessageAt, MemberCount: row.MemberCount, MemberPreviewNames: row.MemberPreviewNames,
		},
	}
}
