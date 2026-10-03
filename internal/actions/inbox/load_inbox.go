//go:build server

// Package inbox 实现统一收件箱领域的应用查询。
package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"golang.org/x/text/unicode/norm"
)

const defaultInboxPageSize = 50

// LoadInput 定义会话列表范围、筛选、会话名称搜索、页大小和分页边界；Search 规范化后为空表示不搜索，SearchRange 只在搜索时生效。
type LoadInput struct {
	Cursor             string
	BeforeCursor       string
	Limit              int
	Partition          domain.InboxPartition
	Scope              domain.InboxScope
	PendingKind        domain.InboxPendingKind
	QueueFilter        domain.ServiceQueueFilter
	QueueTeamID        string
	ChannelID          string
	Source             domain.ServiceSource
	Audience           domain.ServiceAudience
	ServiceStatus      domain.ServiceSessionStatus
	AssigneeFilter     domain.InboxAssigneeFilter
	AssigneeIdentityID string
	Kinds              []domain.ConversationType
	Search             string
	SearchRange        SearchRange
}

// serviceView 判断当前范围是否按处理方查看服务会话。
func (input LoadInput) serviceView() bool {
	return input.Scope == domain.InboxScopePending || input.Scope == domain.InboxScopeAll
}

// includesKind 判断会话类型是否属于当前筛选，未选类型表示不限类型。
func (input LoadInput) includesKind(kind domain.ConversationType) bool {
	return len(input.Kinds) == 0 || slices.Contains(input.Kinds, kind)
}

// PendingSummary 定义待处理条目的类型、等待起点，以及当前周期内是否有提醒本人且尚未回应的内部备注。
type PendingSummary struct {
	Kind      domain.InboxPendingKind
	Since     time.Time
	Mentioned bool
}

// AssigneeSummary 定义客户会话负责人摘要。
type AssigneeSummary struct {
	IdentityID   string
	Type         domain.OrganizationIdentityType
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
	PreviewSenderIdentityType *domain.OrganizationIdentityType
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
}

// ServiceChannelSummary 定义服务会话的来源渠道。
type ServiceChannelSummary struct {
	Type domain.ChannelType
	Name string
}

// DirectConversationSummary 定义收件箱中的内部单聊详情。
type DirectConversationSummary struct {
	PeerIdentityID            string
	PeerType                  domain.OrganizationIdentityType
	PeerName                  string
	PeerAvatarFileID          *string
	PeerStatus                domain.IdentityStatus
	PeerWorkStatus            domain.WorkStatus
	Preview                   *string
	PreviewSenderIdentityType *domain.OrganizationIdentityType
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
	PreviewSenderIdentityType *domain.OrganizationIdentityType
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
	PreviewSenderIdentityType *domain.OrganizationIdentityType
	LastMessageAt             *time.Time
	MemberCount               int
	// MemberPreviewNames 是除查看者外按入群先后排列的前几名在群成员名称。
	MemberPreviewNames []string
}

// ConversationSummary 定义统一收件箱会话信封。
type ConversationSummary struct {
	PositionCursor       string
	LastActivityAt       *time.Time
	LastMessageType      *domain.MessageType
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

// LoadInboxQuery 读取当前企业的统一收件箱。
type LoadInboxQuery struct {
	db bun.IDB
}

// UnreadCounts 定义内部会话的客观未读和提醒未读总数，以及本人待处理的服务会话数与这些会话中的未读消息总数。
type UnreadCounts struct {
	Unread        int `bun:"unread_count"`
	Attention     int `bun:"attention_unread_count"`
	Pending       int `bun:"-"`
	PendingUnread int `bun:"-"`
}

type serviceConversationRow struct {
	ID                        string                           `bun:"id"`
	Type                      domain.ConversationType          `bun:"type"`
	Title                     string                           `bun:"title"`
	Source                    domain.ServiceSource             `bun:"source"`
	Audience                  domain.ServiceAudience           `bun:"audience"`
	RequesterName             *string                          `bun:"requester_name"`
	RequesterContactNumber    *int64                           `bun:"requester_contact_number"`
	RequesterAvatarFileID     *string                          `bun:"requester_avatar_file_id"`
	RequesterChatSubjectID    string                           `bun:"requester_chat_subject_id"`
	AssigneeChatSubjectID     *string                          `bun:"assignee_chat_subject_id"`
	ChannelType               *string                          `bun:"channel_type"`
	ChannelName               *string                          `bun:"channel_name"`
	AgentIdentityID           *string                          `bun:"service_agent_identity_id"`
	AgentName                 *string                          `bun:"service_agent_name"`
	Preview                   *string                          `bun:"preview"`
	PreviewSenderIdentityType *domain.OrganizationIdentityType `bun:"preview_sender_identity_type"`
	PreviewVisibility         *domain.MessageVisibility        `bun:"preview_visibility"`
	LastMessageAt             *time.Time                       `bun:"last_message_at"`
	ServiceSessionStatus      string                           `bun:"service_session_status"`
	ServiceSessionID          string                           `bun:"service_session_id"`
	AssigneeIdentityID        *string                          `bun:"assignee_identity_id"`
	AssigneeType              *string                          `bun:"assignee_type"`
	AssigneeDisplayName       *string                          `bun:"assignee_display_name"`
	AssigneeAvatarFileID      *string                          `bun:"assignee_avatar_file_id"`
	TeamID                    *string                          `bun:"team_id"`
	TeamName                  *string                          `bun:"team_name"`
	LastActivityAt            *time.Time                       `bun:"last_activity_at"`
	UnreadCount               int                              `bun:"unread_count"`
	MentionedUnreadCount      int                              `bun:"mentioned_unread_count"`
	UnansweredMentionCount    int                              `bun:"unanswered_mention_count"`
	LastReadMessageID         *string                          `bun:"last_read_message_id"`
	LastMessageID             *string                          `bun:"last_message_id"`
	LastMessageType           *domain.MessageType              `bun:"last_message_type"`
	Pinned                    bool                             `bun:"pinned"`
}

type directConversationRow struct {
	ID                        string                           `bun:"id"`
	PeerIdentityID            string                           `bun:"peer_identity_id"`
	PeerType                  string                           `bun:"peer_type"`
	PeerName                  string                           `bun:"peer_name"`
	PeerAvatarFileID          *string                          `bun:"peer_avatar_file_id"`
	PeerStatus                domain.IdentityStatus            `bun:"peer_status"`
	PeerWorkStatus            domain.WorkStatus                `bun:"peer_work_status"`
	Preview                   *string                          `bun:"preview"`
	PreviewSenderIdentityType *domain.OrganizationIdentityType `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                       `bun:"last_message_at"`
	LastActivityAt            *time.Time                       `bun:"last_activity_at"`
	UnreadCount               int                              `bun:"unread_count"`
	LastMessageID             *string                          `bun:"last_message_id"`
	LastMessageType           *domain.MessageType              `bun:"last_message_type"`
	LastReadMessageID         *string                          `bun:"last_read_message_id"`
	Muted                     bool                             `bun:"muted"`
	MarkedUnread              bool                             `bun:"marked_unread"`
	Pinned                    bool                             `bun:"pinned"`
	ArchivedAt                *time.Time                       `bun:"archived_at"`
}

type agentConversationRow struct {
	Title                     string                           `bun:"title"`
	ID                        string                           `bun:"id"`
	AgentIdentityID           string                           `bun:"agent_identity_id"`
	AgentName                 string                           `bun:"agent_name"`
	AgentAvatarFileID         *string                          `bun:"agent_avatar_file_id"`
	AgentStatus               domain.IdentityStatus            `bun:"agent_status"`
	AgentPersonal             bool                             `bun:"agent_personal"`
	AgentPaused               bool                             `bun:"agent_paused"`
	ServiceOpen               bool                             `bun:"service_open"`
	AgentComputerRevoked      bool                             `bun:"agent_computer_revoked"`
	AgentComputerLastSeenAt   *time.Time                       `bun:"agent_computer_last_seen_at"`
	Preview                   *string                          `bun:"preview"`
	PreviewSenderIdentityType *domain.OrganizationIdentityType `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                       `bun:"last_message_at"`
	AgentRunStatus            *string                          `bun:"agent_run_status"`
	LastActivityAt            *time.Time                       `bun:"last_activity_at"`
	UnreadCount               int                              `bun:"unread_count"`
	LastMessageID             *string                          `bun:"last_message_id"`
	LastMessageType           *domain.MessageType              `bun:"last_message_type"`
	LastReadMessageID         *string                          `bun:"last_read_message_id"`
	Muted                     bool                             `bun:"muted"`
	MarkedUnread              bool                             `bun:"marked_unread"`
	Pinned                    bool                             `bun:"pinned"`
	ArchivedAt                *time.Time                       `bun:"archived_at"`
}

type groupConversationRow struct {
	ID                        string                           `bun:"id"`
	Title                     string                           `bun:"title"`
	ImageFileID               *string                          `bun:"image_file_id"`
	Status                    string                           `bun:"status"`
	Preview                   *string                          `bun:"preview"`
	PreviewSenderIdentityType *domain.OrganizationIdentityType `bun:"preview_sender_identity_type"`
	LastMessageAt             *time.Time                       `bun:"last_message_at"`
	MemberCount               int                              `bun:"member_count"`
	MemberPreviewNames        []string                         `bun:"member_preview_names,array"`
	LastActivityAt            *time.Time                       `bun:"last_activity_at"`
	UnreadCount               int                              `bun:"unread_count"`
	MentionedUnreadCount      int                              `bun:"mentioned_unread_count"`
	LastMessageID             *string                          `bun:"last_message_id"`
	LastMessageType           *domain.MessageType              `bun:"last_message_type"`
	LastReadMessageID         *string                          `bun:"last_read_message_id"`
	Muted                     bool                             `bun:"muted"`
	MarkedUnread              bool                             `bun:"marked_unread"`
	Pinned                    bool                             `bun:"pinned"`
	ArchivedAt                *time.Time                       `bun:"archived_at"`
}

// NewLoadInboxQuery 创建成员收件箱查询。
func NewLoadInboxQuery(db bun.IDB) *LoadInboxQuery {
	return &LoadInboxQuery{db: db}
}

// ConversationPage 保存统一排序的一页会话及后续边界。
type ConversationPage struct {
	ConversationWindow
	NextCursor string
	HasMore    bool
}

// Execute 在同一只读快照中读取会话分页与完整未读总数。
func (q *LoadInboxQuery) Execute(ctx context.Context, identity *servermodels.Identity, input LoadInput) (ConversationPage, UnreadCounts, error) {
	input, err := normalizeLoadInput(input)
	if err != nil {
		return ConversationPage{}, UnreadCounts{}, err
	}
	if input.Limit < 0 || (input.Cursor != "" && input.BeforeCursor != "") {
		return ConversationPage{}, UnreadCounts{}, ErrQueryInvalid
	}
	if input.Limit == 0 {
		input.Limit = defaultInboxPageSize
	}
	var boundary *inboxCursor
	cursor := input.Cursor
	if input.BeforeCursor != "" {
		cursor = input.BeforeCursor
	}
	if cursor != "" {
		boundary, err = decodeInboxCursor(cursor, identity, input)
		if err != nil {
			return ConversationPage{}, UnreadCounts{}, err
		}
	}
	var page ConversationPage
	var counts UnreadCounts
	err = q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		pinOrderVersion, err := snapshot.pinOrderVersion(ctx, identity)
		if err != nil {
			return err
		}
		if boundary != nil {
			if err := authorizeInboxCursor(boundary, input.Partition, pinOrderVersion); err != nil {
				return err
			}
		}
		page, err = snapshot.loadConversationPage(ctx, identity, input, pinOrderVersion, boundary)
		if err != nil {
			return err
		}
		counts, err = snapshot.loadUnreadCounts(ctx, identity.Organization.ID, identity.OrganizationIdentity.ID, identity.User.ID)
		if err != nil {
			return err
		}
		counts.Pending, counts.PendingUnread, err = snapshot.countPending(ctx, identity)
		return err
	})
	return page, counts, err
}

// LoadAttention 在同一只读快照中读取本人的完整提醒数量，不读取会话分页，供工作区切换器与应用角标汇总各工作区使用。
func (q *LoadInboxQuery) LoadAttention(ctx context.Context, identity *servermodels.Identity) (UnreadCounts, error) {
	var counts UnreadCounts
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		var err error
		counts, err = snapshot.loadUnreadCounts(ctx, identity.Organization.ID, identity.OrganizationIdentity.ID, identity.User.ID)
		if err != nil {
			return err
		}
		counts.Pending, counts.PendingUnread, err = snapshot.countPending(ctx, identity)
		return err
	})
	return counts, err
}

// loadConversationPage 按精确活动边界取整页，边界行移除不影响后续读取。
func (q *LoadInboxQuery) loadConversationPage(ctx context.Context, identity *servermodels.Identity, input LoadInput, pinOrderVersion int64, boundary *inboxCursor) (ConversationPage, error) {
	var start, end *inboxCursorPoint
	if boundary != nil {
		start, end = &boundary.inboxCursorPoint, &boundary.inboxCursorPoint
	}
	points, err := q.readNeighborPoints(ctx, identity, input, start, input.BeforeCursor != "", input.Limit)
	if err != nil {
		return ConversationPage{}, err
	}
	if len(points) > 0 {
		start, end = &points[0], &points[len(points)-1]
	}
	window, err := q.buildConversationWindow(ctx, identity, input, pinOrderVersion, points, start, end)
	if err != nil {
		return ConversationPage{}, err
	}
	page := ConversationPage{ConversationWindow: window, HasMore: window.HasAfter}
	if page.HasMore {
		page.NextCursor = window.EndCursor
	}
	return page, nil
}

// serviceConversationDetailsQuery 读取企业内服务会话摘要，不按处理队列限制阅读；预览取当前成员可见的最后一条消息。
func (q *LoadInboxQuery) serviceConversationDetailsQuery(organizationID, currentIdentityID, userID string) *bun.SelectQuery {
	return q.serviceConversationAccessQuery(organizationID, currentIdentityID).
		ColumnExpr("unread.unread_count AS unread_count").
		ColumnExpr("unread.mentioned_unread_count AS mentioned_unread_count").
		ColumnExpr("unanswered.unanswered_mention_count AS unanswered_mention_count").
		ColumnExpr("state.last_read_message_id::text AS last_read_message_id").
		ColumnExpr("state.pin_rank IS NOT NULL AS pinned").
		ColumnExpr("cv.type AS type").
		ColumnExpr("cv.title AS title").
		ColumnExpr("svc.source, svc.audience").
		ColumnExpr("COALESCE("+contactname.Expr("c", "cci.display_name")+", requester_oi.display_name) AS requester_name").
		ColumnExpr("c.number AS requester_contact_number").
		ColumnExpr("COALESCE(cci.avatar_file_id, requester_oi.avatar_file_id)::text AS requester_avatar_file_id").
		ColumnExpr("svc.requester_subject_id::text AS requester_chat_subject_id").
		ColumnExpr("ch.type AS channel_type").
		ColumnExpr("ch.name AS channel_name").
		ColumnExpr("service_agent.id::text AS service_agent_identity_id, service_agent.display_name AS service_agent_name").
		ColumnExpr("? AS preview", messagequery.Summary("preview_msg")).
		ColumnExpr("preview_msg.type AS last_message_type").
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("preview_msg.visibility AS preview_visibility").
		ColumnExpr("last_visible.originated_at AS last_message_at").
		ColumnExpr("last_visible.id::text AS last_message_id").
		ColumnExpr("current.status AS service_session_status").
		ColumnExpr("current.id::text AS service_session_id").
		ColumnExpr("current.assignee_identity_id::text AS assignee_identity_id").
		ColumnExpr("assignee.type AS assignee_type").
		ColumnExpr("assignee.display_name AS assignee_display_name").
		ColumnExpr("(SELECT assignee_cs.id::text FROM chat_subjects AS assignee_cs WHERE assignee_cs.organization_id = current.organization_id AND assignee_cs.kind = ? AND assignee_cs.source_id = current.assignee_identity_id) AS assignee_chat_subject_id", domain.ChatSubjectKindOrganizationIdentity).
		ColumnExpr("assignee.avatar_file_id::text AS assignee_avatar_file_id").
		ColumnExpr("current.team_id::text AS team_id").
		ColumnExpr("team.name AS team_name").
		Join("LEFT JOIN messages AS preview_msg ON preview_msg.organization_id = cv.organization_id AND preview_msg.conversation_id = cv.id AND preview_msg.id = last_visible.id AND preview_msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = preview_msg.sender_participant_id AND preview_cp.organization_id = preview_msg.organization_id AND preview_cp.conversation_id = preview_msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.organization_id = preview_cp.organization_id").
		Join("LEFT JOIN organization_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.organization_id = preview_cs.organization_id AND preview_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN organization_identities AS assignee ON assignee.organization_id = cv.organization_id AND assignee.id = current.assignee_identity_id").
		Join("LEFT JOIN agent_conversations AS service_ac ON service_ac.organization_id = cv.organization_id AND service_ac.conversation_id = cv.id").
		Join("LEFT JOIN organization_identities AS service_agent ON service_agent.organization_id = service_ac.organization_id AND service_agent.id = service_ac.agent_identity_id").
		Join("LEFT JOIN teams AS team ON team.organization_id = cv.organization_id AND team.id = current.team_id").
		Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", userID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(q.db, currentIdentityID)).
		// 统计当前周期内被提醒成员之后尚未在会话中发言的提醒。
		Join(`JOIN LATERAL (
			SELECT count(*) AS unanswered_mention_count
			FROM message_mentions AS note_mention
			JOIN messages AS note ON note.organization_id = note_mention.organization_id AND note.id = note_mention.message_id
			WHERE note.organization_id = cv.organization_id AND note.conversation_id = cv.id AND note.service_session_id = current.id AND note.deleted_at IS NULL
				AND NOT EXISTS (
					SELECT 1 FROM messages AS answer
					JOIN conversation_participants AS answer_cp ON answer_cp.organization_id = answer.organization_id AND answer_cp.conversation_id = answer.conversation_id AND answer_cp.id = answer.sender_participant_id
					WHERE answer.organization_id = note.organization_id AND answer.conversation_id = note.conversation_id
						AND answer.message_seq > note.message_seq AND answer.deleted_at IS NULL AND answer_cp.subject_id = note_mention.subject_id
				)
		) AS unanswered ON TRUE`)
}

// filterServiceInbox 为服务会话追加来源与服务对象筛选；全部范围另按服务状态和负责人筛选。
func filterServiceInbox(query *bun.SelectQuery, input LoadInput) *bun.SelectQuery {
	query = query.Where("msg.id IS NOT NULL")
	if input.ChannelID != "" {
		query = query.Where("cci.channel_id = ?", input.ChannelID)
	}
	if input.Source != "" {
		query = query.Where("svc.source = ?", input.Source)
	}
	if input.Audience != "" {
		query = query.Where("svc.audience = ?", input.Audience)
	}
	if input.Scope != domain.InboxScopeAll {
		return query
	}
	query = query.Where("current.status = ?", input.ServiceStatus)
	switch input.AssigneeFilter {
	case domain.InboxAssigneeFilterUnassigned:
		query = query.Where("current.assignee_identity_id IS NULL")
	case domain.InboxAssigneeFilterIdentity:
		query = query.Where("current.assignee_identity_id = ?", input.AssigneeIdentityID)
	}
	return query
}

// withIndividualConversationDetails 为单聊阅读基线追加消息预览和个人未读状态；预览取会话末条消息，服务会话的末条消息只含发起人可见的消息。
func withIndividualConversationDetails(query *bun.SelectQuery, identityID, userID string) *bun.SelectQuery {
	return query.
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("msg.type AS last_message_type").
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("cv.last_message_at AS last_message_at").
		ColumnExpr("cv.last_message_id::text AS last_message_id").
		ColumnExpr("unread.unread_count AS unread_count").
		ColumnExpr("state.last_read_message_id::text AS last_read_message_id").
		ColumnExpr("COALESCE(state.muted, false) AS muted").
		ColumnExpr("COALESCE(state.marked_unread, false) AS marked_unread").
		ColumnExpr("state.pin_rank IS NOT NULL AS pinned").
		ColumnExpr("state.archived_at").
		Join("LEFT JOIN messages AS msg ON msg.organization_id = cv.organization_id AND msg.conversation_id = cv.id AND msg.id = cv.last_message_id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.organization_id = msg.organization_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.organization_id = preview_cp.organization_id").
		Join("LEFT JOIN organization_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.organization_id = preview_cs.organization_id AND preview_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", userID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(query.DB(), identityID))
}

// lastVisibleMessageQuery 构造会话 cv 中指定成员可见的最后一条消息，服务周期的系统事件只取发给发起人的服务进度。
func lastVisibleMessageQuery(db bun.IDB, identityID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("messages AS visible").
		ColumnExpr("visible.id, visible.originated_at").
		Where("visible.organization_id = cv.organization_id AND visible.conversation_id = cv.id").
		Where("visible.type <> ? OR visible.service_session_id IS NULL OR visible.visibility = ?", domain.MessageTypeSystem, domain.MessageVisibilityRequester).
		Where("?", messagequery.VisibleTo("visible", identityID)).
		OrderExpr("visible.message_seq DESC").
		Limit(1)
}

// directConversationDetailsQuery 按真人身份对及有效成员关系读取长期单聊。
func (q *LoadInboxQuery) directConversationDetailsQuery(organizationID, identityID, userID string) *bun.SelectQuery {
	return withIndividualConversationDetails(q.directConversationAccessQuery(organizationID, identityID), identityID, userID).
		ColumnExpr("peer_oi.id AS peer_identity_id, peer_oi.type AS peer_type, peer_oi.display_name AS peer_name, peer_oi.avatar_file_id AS peer_avatar_file_id, peer_u.status AS peer_status, peer_oi.work_status AS peer_work_status")
}

// agentConversationDetailsQuery 按业务归属和有效成员关系读取独立 AI 聊天。
func (q *LoadInboxQuery) agentConversationDetailsQuery(organizationID, identityID, userID string) *bun.SelectQuery {
	return withAgentConversationDetails(q.agentConversationAccessQuery(organizationID, identityID), identityID, userID)
}

// withAgentConversationDetails 为 AI 会话阅读基线追加消息摘要及当前运行状态。
func withAgentConversationDetails(query *bun.SelectQuery, identityID, userID string) *bun.SelectQuery {
	return withIndividualConversationDetails(query, identityID, userID).
		ColumnExpr("cv.title, oi.id AS agent_identity_id, oi.display_name AS agent_name, oi.avatar_file_id AS agent_avatar_file_id, agent.status AS agent_status, latest_agent_run.status AS agent_run_status").
		ColumnExpr("? = ANY(agent.service_audiences) AS agent_personal, agent.paused_at IS NOT NULL AS agent_paused, agent_computer.revoked_at IS NOT NULL AS agent_computer_revoked, agent_computer.last_seen_at AS agent_computer_last_seen_at", domain.ServiceAudiencePersonal).
		ColumnExpr(`EXISTS (
			SELECT 1 FROM service_conversations AS open_svc
			JOIN service_sessions AS open_ss ON open_ss.organization_id = open_svc.organization_id AND open_ss.id = open_svc.current_service_session_id
			WHERE open_svc.organization_id = cv.organization_id AND open_svc.conversation_id = cv.id AND open_ss.status = ?
		) AS service_open`, domain.ServiceSessionStatusOpen).
		Join("LEFT JOIN computers AS agent_computer ON agent_computer.organization_id = agent.organization_id AND agent_computer.id = agent.computer_id").
		Join("LEFT JOIN LATERAL (SELECT agr.status FROM agent_runs AS agr WHERE agr.organization_id = cv.organization_id AND agr.conversation_id = cv.id AND agr.agent_identity_id = ac.agent_identity_id ORDER BY agr.created_at DESC, agr.id DESC LIMIT 1) AS latest_agent_run ON TRUE")
}

// groupConversationsQuery 共用群聊成员范围、个人状态和未读统计。
func (q *LoadInboxQuery) groupConversationsQuery(organizationID, identityID, userID string) *bun.SelectQuery {
	return q.groupConversationAccessQuery(organizationID, identityID).
		ColumnExpr("COALESCE(cv.title, '') AS title").
		ColumnExpr(chatstate.GroupMemberPreviewNamesExpr+" AS member_preview_names", identityID, chatstate.GroupMemberPreviewNamesLimit).
		ColumnExpr("cv.image_file_id::text AS image_file_id").
		ColumnExpr("cv.status AS status").
		ColumnExpr("? AS preview", messagequery.Summary("msg")).
		ColumnExpr("msg.type AS last_message_type").
		ColumnExpr("preview_oi.type AS preview_sender_identity_type").
		ColumnExpr("cv.last_message_at AS last_message_at").
		ColumnExpr("cv.last_message_id::text AS last_message_id").
		ColumnExpr("members.member_count AS member_count").
		ColumnExpr("unread.unread_count AS unread_count").
		ColumnExpr("unread.mentioned_unread_count AS mentioned_unread_count").
		ColumnExpr("state.last_read_message_id::text AS last_read_message_id").
		ColumnExpr("COALESCE(state.muted, false) AS muted").
		ColumnExpr("COALESCE(state.marked_unread, false) AS marked_unread").
		ColumnExpr("state.pin_rank IS NOT NULL AS pinned").
		ColumnExpr("state.archived_at").
		Join("JOIN LATERAL (SELECT count(*) AS member_count FROM conversation_participants AS member_cp WHERE member_cp.organization_id = cv.organization_id AND member_cp.conversation_id = cv.id AND member_cp.left_at IS NULL) AS members ON TRUE").
		Join("LEFT JOIN messages AS msg ON msg.organization_id = cv.organization_id AND msg.conversation_id = cv.id AND msg.id = cv.last_message_id AND msg.deleted_at IS NULL").
		Join("LEFT JOIN conversation_participants AS preview_cp ON preview_cp.id = msg.sender_participant_id AND preview_cp.organization_id = msg.organization_id AND preview_cp.conversation_id = msg.conversation_id").
		Join("LEFT JOIN chat_subjects AS preview_cs ON preview_cs.id = preview_cp.subject_id AND preview_cs.organization_id = preview_cp.organization_id").
		Join("LEFT JOIN organization_identities AS preview_oi ON preview_oi.id = preview_cs.source_id AND preview_oi.organization_id = preview_cs.organization_id AND preview_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", userID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(q.db, identityID))
}

// loadUnreadCounts 按完整会话范围汇总提醒，不受当前筛选和列表条数限制，本人已归档的聊天除外。
func (q *LoadInboxQuery) loadUnreadCounts(ctx context.Context, organizationID, identityID, userID string) (UnreadCounts, error) {
	direct := q.db.NewSelect().TableExpr("(?) AS direct", withIndividualConversationDetails(q.directConversationsQuery(organizationID, identityID), identityID, userID)).
		ColumnExpr("unread_count, 0::bigint AS mentioned_unread_count, muted, marked_unread, archived_at")
	group := q.db.NewSelect().TableExpr("(?) AS groups", q.groupConversationsQuery(organizationID, identityID, userID)).
		ColumnExpr("unread_count, mentioned_unread_count, muted, marked_unread, archived_at")
	agent := q.db.NewSelect().TableExpr("(?) AS agents", withIndividualConversationDetails(q.agentConversationsQuery(organizationID, identityID), identityID, userID)).ColumnExpr("unread_count, 0::bigint AS mentioned_unread_count, muted, marked_unread, archived_at")
	counts := UnreadCounts{}
	err := q.db.NewSelect().TableExpr("(?) AS internal", direct.UnionAll(group).UnionAll(agent)).
		Where("internal.archived_at IS NULL").
		ColumnExpr("COALESCE(sum(unread_count), 0) AS unread_count").
		ColumnExpr(`COALESCE(sum(CASE WHEN muted THEN mentioned_unread_count
            ELSE GREATEST(unread_count, CASE WHEN marked_unread THEN 1 ELSE 0 END)
        END), 0) AS attention_unread_count`).Scan(ctx, &counts)
	if err != nil {
		return UnreadCounts{}, fmt.Errorf("count internal unread messages: %w", err)
	}
	return counts, nil
}

// countPending 统计本人全部待处理条目数及这些条目中本人的未读消息总数，不受当前筛选影响；未读口径与客户会话摘要一致。
func (q *LoadInboxQuery) countPending(ctx context.Context, identity *servermodels.Identity) (int, int, error) {
	var counts struct {
		Pending int `bun:"pending_count"`
		Unread  int `bun:"pending_unread_count"`
	}
	err := q.db.NewSelect().TableExpr("(?) AS pending", q.pendingCandidates(identity, LoadInput{Scope: domain.InboxScopePending})).
		ColumnExpr("count(*) AS pending_count").
		ColumnExpr("COALESCE(sum(unread.unread_count), 0) AS pending_unread_count").
		Join("JOIN conversations AS cv ON cv.organization_id = ? AND cv.id = pending.id", identity.Organization.ID).
		Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", identity.User.ID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(q.db, identity.OrganizationIdentity.ID)).
		Scan(ctx, &counts)
	if err != nil {
		return 0, 0, fmt.Errorf("count pending service conversations: %w", err)
	}
	return counts.Pending, counts.Unread, nil
}

// summary 将 AI 会话查询结果转换为统一摘要，对象为个人 AI 员工时按当前时间计算其在线状态。
func (row agentConversationRow) summary() ConversationSummary {
	var personalPresence domain.PersonalAgentPresence
	if row.AgentPersonal {
		personalPresence = domain.ResolvePersonalAgentPresence(row.AgentStatus, row.AgentPaused, row.AgentComputerRevoked, row.AgentComputerLastSeenAt, time.Now())
	}
	var agentRunStatus *domain.AgentRunStatus
	if row.AgentRunStatus != nil {
		status := domain.AgentRunStatus(*row.AgentRunStatus)
		agentRunStatus = &status
	}
	return ConversationSummary{
		ID: row.ID, Type: domain.ConversationTypeAgent, UnreadCount: row.UnreadCount, Muted: row.Muted, MarkedUnread: row.MarkedUnread, Pinned: row.Pinned, ArchivedAt: row.ArchivedAt, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Agent: &AgentConversationSummary{
			Title: row.Title, AgentIdentityID: row.AgentIdentityID, AgentName: row.AgentName, AgentAvatarFileID: row.AgentAvatarFileID, AgentStatus: row.AgentStatus,
			PersonalPresence: personalPresence,
			Preview:          row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, LastMessageAt: row.LastMessageAt, AgentRunStatus: agentRunStatus,
			ServiceOpen: row.ServiceOpen,
		},
	}
}

// LoadAgentConversation 读取当前成员指定 AI 会话的完整收件箱摘要。
func (q *LoadInboxQuery) LoadAgentConversation(ctx context.Context, identity *servermodels.Identity, conversationID string) (ConversationSummary, error) {
	var row agentConversationRow
	err := withAgentConversationDetails(q.agentConversationsQuery(identity.Organization.ID, identity.OrganizationIdentity.ID), identity.OrganizationIdentity.ID, identity.User.ID).Where("cv.id = ?", conversationID).Scan(ctx, &row)
	if err != nil {
		return ConversationSummary{}, err
	}
	return row.summary(), nil
}

// summary 转换服务会话的统一摘要。
func (row serviceConversationRow) summary() ConversationSummary {
	var assignee *AssigneeSummary
	if row.AssigneeIdentityID != nil && row.AssigneeType != nil && row.AssigneeDisplayName != nil {
		assignee = &AssigneeSummary{IdentityID: *row.AssigneeIdentityID, Type: domain.OrganizationIdentityType(*row.AssigneeType), DisplayName: *row.AssigneeDisplayName, AvatarFileID: row.AssigneeAvatarFileID}
	}
	var channel *ServiceChannelSummary
	if row.ChannelType != nil && row.ChannelName != nil {
		channel = &ServiceChannelSummary{Type: domain.ChannelType(*row.ChannelType), Name: *row.ChannelName}
	}
	return ConversationSummary{
		ID: row.ID, Type: row.Type, UnreadCount: row.UnreadCount, MentionedUnreadCount: row.MentionedUnreadCount, Pinned: row.Pinned, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Service: &ServiceConversationSummary{
			Title: row.Title, Source: row.Source, Audience: row.Audience,
			RequesterName: row.RequesterName, RequesterContactNumber: row.RequesterContactNumber, RequesterAvatarFileID: row.RequesterAvatarFileID, RequesterChatSubjectID: row.RequesterChatSubjectID, AssigneeChatSubjectID: row.AssigneeChatSubjectID,
			Channel: channel, AgentIdentityID: row.AgentIdentityID, AgentName: row.AgentName,
			Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, PreviewVisibility: row.PreviewVisibility, LastMessageAt: row.LastMessageAt,
			ServiceSessionID: row.ServiceSessionID, ServiceSessionStatus: domain.ServiceSessionStatus(row.ServiceSessionStatus), Assignee: assignee,
			TeamID: row.TeamID, TeamName: row.TeamName,
			UnansweredMentionCount: row.UnansweredMentionCount,
		},
	}
}

// summary 转换真人单聊会话的统一摘要。
func (row directConversationRow) summary() ConversationSummary {
	return ConversationSummary{
		ID: row.ID, Type: domain.ConversationTypeDirect, UnreadCount: row.UnreadCount, Muted: row.Muted, MarkedUnread: row.MarkedUnread, Pinned: row.Pinned, ArchivedAt: row.ArchivedAt, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Direct: &DirectConversationSummary{
			PeerIdentityID: row.PeerIdentityID, PeerType: domain.OrganizationIdentityType(row.PeerType), PeerName: row.PeerName, PeerAvatarFileID: row.PeerAvatarFileID, PeerStatus: row.PeerStatus, PeerWorkStatus: row.PeerWorkStatus,
			Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType, LastMessageAt: row.LastMessageAt,
		},
	}
}

// summary 转换群聊会话的统一摘要。
func (row groupConversationRow) summary() ConversationSummary {
	return ConversationSummary{
		ID: row.ID, Type: domain.ConversationTypeGroup, UnreadCount: row.UnreadCount, MentionedUnreadCount: row.MentionedUnreadCount, Muted: row.Muted, MarkedUnread: row.MarkedUnread, Pinned: row.Pinned, ArchivedAt: row.ArchivedAt, LastMessageID: row.LastMessageID, LastMessageType: row.LastMessageType, LastReadMessageID: row.LastReadMessageID, LastActivityAt: row.LastActivityAt,
		Group: &GroupConversationSummary{
			Title: row.Title, ImageFileID: row.ImageFileID, Status: domain.ConversationStatus(row.Status), Preview: row.Preview, PreviewSenderIdentityType: row.PreviewSenderIdentityType,
			LastMessageAt: row.LastMessageAt, MemberCount: row.MemberCount, MemberPreviewNames: row.MemberPreviewNames,
		},
	}
}

// chatKinds 是一级栏聊天可筛选的会话类型，顺序用于规范化筛选值。
var chatKinds = []domain.ConversationType{domain.ConversationTypeDirect, domain.ConversationTypeGroup, domain.ConversationTypeAgent}

// normalizeChatKinds 校验会话类型属于聊天范围，并按固定顺序去重；覆盖全部类型等同不限类型。
func normalizeChatKinds(kinds []domain.ConversationType) ([]domain.ConversationType, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	selected := make(map[domain.ConversationType]bool, len(kinds))
	for _, kind := range kinds {
		if !slices.Contains(chatKinds, kind) {
			return nil, ErrQueryInvalid
		}
		selected[kind] = true
	}
	if len(selected) == len(chatKinds) {
		return nil, nil
	}
	normalized := make([]domain.ConversationType, 0, len(selected))
	for _, kind := range chatKinds {
		if selected[kind] {
			normalized = append(normalized, kind)
		}
	}
	return normalized, nil
}

// normalizeQueueFilter 规范化待领取条目的队列筛选：只在待领取类型生效，指定团队时须带有效团队编号。
func normalizeQueueFilter(input LoadInput) (LoadInput, error) {
	if input.PendingKind != domain.InboxPendingKindQueue {
		input.QueueFilter, input.QueueTeamID = "", ""
		return input, nil
	}
	if input.QueueFilter == "" {
		input.QueueFilter = domain.ServiceQueueFilterAll
	}
	switch input.QueueFilter {
	case domain.ServiceQueueFilterAll, domain.ServiceQueueFilterPublic:
		if input.QueueTeamID != "" {
			return input, ErrQueryInvalid
		}
	case domain.ServiceQueueFilterTeam:
		if !common.ValidUUID(input.QueueTeamID) {
			return input, ErrQueryInvalid
		}
	default:
		return input, ErrQueryInvalid
	}
	return input, nil
}

// normalizeAssigneeFilter 规范化全部范围的负责人筛选：指定企业身份时须带有效身份编号。
func normalizeAssigneeFilter(input LoadInput) (LoadInput, error) {
	if input.AssigneeFilter == "" {
		input.AssigneeFilter = domain.InboxAssigneeFilterAll
	}
	switch input.AssigneeFilter {
	case domain.InboxAssigneeFilterAll, domain.InboxAssigneeFilterUnassigned:
		if input.AssigneeIdentityID != "" {
			return input, ErrQueryInvalid
		}
	case domain.InboxAssigneeFilterIdentity:
		if !common.ValidUUID(input.AssigneeIdentityID) {
			return input, ErrQueryInvalid
		}
	default:
		return input, ErrQueryInvalid
	}
	return input, nil
}

// normalizeServiceFilters 校验服务会话共用的来源与服务对象筛选。
func normalizeServiceFilters(input LoadInput) error {
	if input.ChannelID != "" && !common.ValidUUID(input.ChannelID) {
		return ErrQueryInvalid
	}
	if input.Audience != "" && !slices.Contains([]domain.ServiceAudience{domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee, domain.ServiceAudiencePartner}, input.Audience) {
		return ErrQueryInvalid
	}
	// 按渠道筛选只适用于渠道来源。
	if input.Source != "" && (!slices.Contains([]domain.ServiceSource{domain.ServiceSourceChannel, domain.ServiceSourceDirect}, input.Source) ||
		(input.ChannelID != "" && input.Source != domain.ServiceSourceChannel)) {
		return ErrQueryInvalid
	}
	return nil
}

// normalizeLoadInput 规范化并校验会话列表范围与筛选；可读范围搜索不带列表范围和筛选，其余读取按范围保留适用的筛选。
func normalizeLoadInput(input LoadInput) (LoadInput, error) {
	input.Scope = domain.InboxScope(strings.TrimSpace(string(input.Scope)))
	input.PendingKind = domain.InboxPendingKind(strings.TrimSpace(string(input.PendingKind)))
	input.QueueFilter = domain.ServiceQueueFilter(strings.TrimSpace(string(input.QueueFilter)))
	input.QueueTeamID = strings.TrimSpace(input.QueueTeamID)
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	input.Source = domain.ServiceSource(strings.TrimSpace(string(input.Source)))
	input.Audience = domain.ServiceAudience(strings.TrimSpace(string(input.Audience)))
	input.ServiceStatus = domain.ServiceSessionStatus(strings.TrimSpace(string(input.ServiceStatus)))
	input.AssigneeFilter = domain.InboxAssigneeFilter(strings.TrimSpace(string(input.AssigneeFilter)))
	input.AssigneeIdentityID = strings.TrimSpace(input.AssigneeIdentityID)
	input.Partition = domain.InboxPartition(strings.TrimSpace(string(input.Partition)))
	if input.Partition == "" {
		input.Partition = domain.InboxPartitionAll
	}
	if !slices.Contains([]domain.InboxPartition{domain.InboxPartitionAll, domain.InboxPartitionPinned, domain.InboxPartitionRegular, domain.InboxPartitionArchived}, input.Partition) {
		return input, ErrQueryInvalid
	}
	// 搜索词按 NFKC 规范化并合并连续空白；搜索读取完整排序或已归档的聊天，不区分置顶分区。
	input.Search = strings.Join(strings.Fields(norm.NFKC.String(input.Search)), " ")
	if input.Search == "" {
		input.SearchRange = ""
	} else {
		if input.SearchRange == "" {
			input.SearchRange = SearchRangeList
		}
		if (input.Partition != domain.InboxPartitionAll && input.Partition != domain.InboxPartitionArchived) || (input.SearchRange != SearchRangeList && input.SearchRange != SearchRangeReadable) {
			return input, ErrQueryInvalid
		}
	}
	if input.SearchRange == SearchRangeReadable {
		if input.Partition != domain.InboxPartitionAll || input.Scope != "" || input.PendingKind != "" || input.QueueFilter != "" || input.QueueTeamID != "" || input.ChannelID != "" || input.Source != "" || input.Audience != "" ||
			input.ServiceStatus != "" || input.AssigneeFilter != "" || input.AssigneeIdentityID != "" || len(input.Kinds) > 0 {
			return input, ErrQueryInvalid
		}
		return input, nil
	}
	pending, all, chat := input.Scope == domain.InboxScopePending, input.Scope == domain.InboxScopeAll, input.Scope == domain.InboxScopeChat
	// 已归档分区只适用于聊天范围。
	if (!pending && !all && !chat) || (!chat && input.Partition == domain.InboxPartitionArchived) {
		return input, ErrQueryInvalid
	}
	if chat {
		kinds, err := normalizeChatKinds(input.Kinds)
		if err != nil {
			return input, err
		}
		return LoadInput{Cursor: input.Cursor, BeforeCursor: input.BeforeCursor, Limit: input.Limit, Partition: input.Partition, Scope: input.Scope, Kinds: kinds, Search: input.Search, SearchRange: input.SearchRange}, nil
	}
	input.Kinds = nil
	if err := normalizeServiceFilters(input); err != nil {
		return input, err
	}
	if pending {
		// 待处理按等待起点排序，不区分置顶分区；服务状态与负责人不适用。
		if input.Partition != domain.InboxPartitionAll ||
			(input.PendingKind != "" && !slices.Contains([]domain.InboxPendingKind{domain.InboxPendingKindReply, domain.InboxPendingKindQueue, domain.InboxPendingKindMention}, input.PendingKind)) {
			return input, ErrQueryInvalid
		}
		input.ServiceStatus, input.AssigneeFilter, input.AssigneeIdentityID = "", "", ""
		return normalizeQueueFilter(input)
	}
	input.PendingKind, input.QueueFilter, input.QueueTeamID = "", "", ""
	if input.ServiceStatus == "" {
		input.ServiceStatus = domain.ServiceSessionStatusOpen
	}
	if input.ServiceStatus != domain.ServiceSessionStatusOpen && input.ServiceStatus != domain.ServiceSessionStatusClosed {
		return input, ErrQueryInvalid
	}
	return normalizeAssigneeFilter(input)
}
