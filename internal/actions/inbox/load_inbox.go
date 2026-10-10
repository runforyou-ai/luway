//go:build server

// Package inbox 实现统一收件箱领域的应用查询。
package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// defaultInboxPageSize 是未指定页大小时每页的会话数。
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
		counts, err = snapshot.loadUnreadCounts(ctx, identity.Workspace.ID, identity.WorkspaceIdentity.ID, identity.User.ID)
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
		counts, err = snapshot.loadUnreadCounts(ctx, identity.Workspace.ID, identity.WorkspaceIdentity.ID, identity.User.ID)
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

// loadUnreadCounts 按完整会话范围汇总提醒，不受当前筛选和列表条数限制，本人已归档的聊天除外。
func (q *LoadInboxQuery) loadUnreadCounts(ctx context.Context, workspaceID, identityID, userID string) (UnreadCounts, error) {
	direct := q.db.NewSelect().TableExpr("(?) AS direct", withIndividualConversationDetails(q.directConversationsQuery(workspaceID, identityID), identityID, userID)).
		ColumnExpr("unread_count, 0::bigint AS mentioned_unread_count, muted, marked_unread, archived_at")
	group := q.db.NewSelect().TableExpr("(?) AS groups", q.groupConversationsQuery(workspaceID, identityID, userID)).
		ColumnExpr("unread_count, mentioned_unread_count, muted, marked_unread, archived_at")
	agent := q.db.NewSelect().TableExpr("(?) AS agents", withIndividualConversationDetails(q.agentConversationsQuery(workspaceID, identityID), identityID, userID)).ColumnExpr("unread_count, 0::bigint AS mentioned_unread_count, muted, marked_unread, archived_at")
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
	err := q.db.NewSelect().TableExpr("(?) AS pending", q.pendingCandidates(identity.Workspace.ID, identity.WorkspaceIdentity.ID, LoadInput{Scope: domain.InboxScopePending})).
		ColumnExpr("count(*) AS pending_count").
		ColumnExpr("COALESCE(sum(unread.unread_count), 0) AS pending_unread_count").
		Join("JOIN conversations AS cv ON cv.workspace_id = ? AND cv.id = pending.id", identity.Workspace.ID).
		Join("LEFT JOIN conversation_user_states AS state ON state.workspace_id = cv.workspace_id AND state.conversation_id = cv.id AND state.user_id = ?", identity.User.ID).
		Join("JOIN LATERAL (?) AS unread ON TRUE", unreadCountsQuery(q.db, identity.WorkspaceIdentity.ID)).
		Scan(ctx, &counts)
	if err != nil {
		return 0, 0, fmt.Errorf("count pending service conversations: %w", err)
	}
	return counts.Pending, counts.Unread, nil
}

// LoadAgentConversation 读取当前成员指定 AI 会话的完整收件箱摘要。
func (q *LoadInboxQuery) LoadAgentConversation(ctx context.Context, identity *servermodels.Identity, conversationID string) (ConversationSummary, error) {
	var row agentConversationRow
	err := withAgentConversationDetails(q.agentConversationsQuery(identity.Workspace.ID, identity.WorkspaceIdentity.ID), identity.WorkspaceIdentity.ID, identity.User.ID).Where("cv.id = ?", conversationID).Scan(ctx, &row)
	if err != nil {
		return ConversationSummary{}, err
	}
	return row.summary(), nil
}
