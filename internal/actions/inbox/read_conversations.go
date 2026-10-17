//go:build server

package inbox

import (
	"context"
	"database/sql"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ConversationResult 保存指定会话的阅读与列表资格，不可用时不返回摘要。
type ConversationResult struct {
	ID           string
	MatchesQuery bool
	Conversation *ConversationSummary
}

// ReadByIDs 在同一快照中读取指定会话，筛选只决定列表资格，不缩小阅读范围。
func (q *LoadInboxQuery) ReadByIDs(ctx context.Context, identity *servermodels.Identity, ids []string, input *LoadInput) ([]ConversationResult, error) {
	for _, id := range ids {
		if !str.IsUUID(id) {
			return nil, ErrQueryInvalid
		}
	}
	if input != nil {
		normalized, err := normalizeLoadInput(*input)
		if err != nil {
			return nil, err
		}
		input = &normalized
	}
	if len(ids) == 0 {
		return []ConversationResult{}, nil
	}
	var results []ConversationResult
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		results, err = NewLoadInboxQuery(tx).readByIDs(ctx, identity, ids, input)
		return err
	})
	return results, err
}

// readByIDs 在当前快照中读取指定会话摘要并按筛选判断列表资格，匹配待处理范围时附带待处理条目摘要。
func (q *LoadInboxQuery) readByIDs(ctx context.Context, identity *servermodels.Identity, ids []string, input *LoadInput) ([]ConversationResult, error) {
	summaries, err := q.readSummaries(ctx, identity, ids, input != nil && input.serviceView())
	if err != nil {
		return nil, err
	}
	matches := make(map[string]inboxCursorPoint)
	if input != nil {
		matches, err = q.matchInboxIDs(ctx, identity, ids, *input)
		if err != nil {
			return nil, err
		}
	}
	results := make([]ConversationResult, 0, len(ids))
	for _, id := range ids {
		summary := summaries[id]
		point, matched := matches[id]
		if summary != nil && matched {
			summary.Pending = point.pending()
		}
		results = append(results, ConversationResult{ID: id, Conversation: summary, MatchesQuery: summary != nil && (input == nil || matched)})
	}
	return results, nil
}

// readSummaries 按当前阅读资格批量读取四类会话的公开摘要；同一会话既是服务会话又是本人的 AI 聊天时，serviceView 为真取服务会话摘要，否则取 AI 聊天摘要。未读与未回应提醒按页内会话编号批量聚合后填回。
func (q *LoadInboxQuery) readSummaries(ctx context.Context, identity *servermodels.Identity, ids []string, serviceView bool) (map[string]*ConversationSummary, error) {
	workspaceID, identityID, userID := identity.Workspace.ID, identity.WorkspaceIdentity.ID, identity.User.ID
	var rows summaryRows
	if err := q.serviceConversationDetailsQuery(workspaceID, identityID, userID).Where("cv.id IN (?)", bun.List(ids)).Scan(ctx, &rows.services); err != nil {
		return nil, err
	}
	if err := q.directConversationDetailsQuery(workspaceID, identityID, userID).Where("cv.id IN (?)", bun.List(ids)).Scan(ctx, &rows.directs); err != nil {
		return nil, err
	}
	if err := q.agentConversationDetailsQuery(workspaceID, identityID, userID).Where("cv.id IN (?)", bun.List(ids)).Scan(ctx, &rows.agents); err != nil {
		return nil, err
	}
	if err := q.groupConversationsQuery(workspaceID, identityID, userID).Where("cv.id IN (?)", bun.List(ids)).Scan(ctx, &rows.groups); err != nil {
		return nil, err
	}
	unread, err := q.loadConversationUnreadCounts(ctx, workspaceID, identityID, userID, ids)
	if err != nil {
		return nil, err
	}
	unanswered, err := q.loadUnansweredMentionCounts(ctx, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	rows.applyUnreadAndUnanswered(unread, unanswered)
	return rows.merge(serviceView), nil
}

// applyUnreadAndUnanswered 将页内批量未读与未回应提醒填回摘要行；缺省为 0。
func (rows *summaryRows) applyUnreadAndUnanswered(unread map[string]conversationUnreadCounts, unanswered map[string]int) {
	for i := range rows.services {
		if counts, ok := unread[rows.services[i].ID]; ok {
			rows.services[i].UnreadCount = counts.UnreadCount
			rows.services[i].MentionedUnreadCount = counts.MentionedUnreadCount
		}
		rows.services[i].UnansweredMentionCount = unanswered[rows.services[i].ID]
	}
	for i := range rows.directs {
		if counts, ok := unread[rows.directs[i].ID]; ok {
			rows.directs[i].UnreadCount = counts.UnreadCount
		}
	}
	for i := range rows.agents {
		if counts, ok := unread[rows.agents[i].ID]; ok {
			rows.agents[i].UnreadCount = counts.UnreadCount
		}
	}
	for i := range rows.groups {
		if counts, ok := unread[rows.groups[i].ID]; ok {
			rows.groups[i].UnreadCount = counts.UnreadCount
			rows.groups[i].MentionedUnreadCount = counts.MentionedUnreadCount
		}
	}
}

// summaryRows 保存同一查看者四类会话摘要查询的结果行。
type summaryRows struct {
	services []serviceConversationRow
	directs  []directConversationRow
	agents   []agentConversationRow
	groups   []groupConversationRow
}

// merge 按会话编号合并四类摘要，同一会话以后写入的一类为准：serviceView 为真时服务会话最后写入，否则最先写入。
func (rows summaryRows) merge(serviceView bool) map[string]*ConversationSummary {
	summaries := make(map[string]*ConversationSummary)
	putServices := func() {
		for _, row := range rows.services {
			summaries[row.ID] = new(row.summary())
		}
	}
	if !serviceView {
		putServices()
	}
	for _, row := range rows.directs {
		summaries[row.ID] = new(row.summary())
	}
	for _, row := range rows.agents {
		summaries[row.ID] = new(row.summary())
	}
	for _, row := range rows.groups {
		summaries[row.ID] = new(row.summary())
	}
	if serviceView {
		putServices()
	}
	return summaries
}

// matchInboxIDs 复用列表筛选与置顶分区核对指定 ID，不受分页边界限制，返回匹配会话的候选位置。
func (q *LoadInboxQuery) matchInboxIDs(ctx context.Context, identity *servermodels.Identity, ids []string, input LoadInput) (map[string]inboxCursorPoint, error) {
	var points []inboxCursorPoint
	if err := q.candidatePointsQuery(identity, input).Where("candidates.id IN (?)", bun.List(ids)).Scan(ctx, &points); err != nil {
		return nil, err
	}
	return arr.KeyBy(points, func(point inboxCursorPoint) string { return point.ID }), nil
}
