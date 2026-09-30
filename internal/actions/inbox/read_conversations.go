//go:build server

package inbox

import (
	"context"
	"database/sql"

	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
		if !common.ValidUUID(id) {
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

// readSummaries 按当前阅读资格批量读取四类会话的公开摘要；同一会话既是服务会话又是本人的 AI 聊天时，serviceView 为真取服务会话摘要，否则取 AI 聊天摘要。
func (q *LoadInboxQuery) readSummaries(ctx context.Context, identity *servermodels.Identity, ids []string, serviceView bool) (map[string]*ConversationSummary, error) {
	organizationID, identityID, userID := identity.Organization.ID, identity.OrganizationIdentity.ID, identity.User.ID
	var services []serviceConversationRow
	if err := q.serviceConversationDetailsQuery(organizationID, identityID, userID).Where("cv.id IN (?)", bun.In(ids)).Scan(ctx, &services); err != nil {
		return nil, err
	}
	var directs []directConversationRow
	if err := q.directConversationDetailsQuery(organizationID, identityID, userID).Where("cv.id IN (?)", bun.In(ids)).Scan(ctx, &directs); err != nil {
		return nil, err
	}
	var agents []agentConversationRow
	if err := q.agentConversationDetailsQuery(organizationID, identityID, userID).Where("cv.id IN (?)", bun.In(ids)).Scan(ctx, &agents); err != nil {
		return nil, err
	}
	var groups []groupConversationRow
	if err := q.groupConversationsQuery(organizationID, identityID, userID).Where("cv.id IN (?)", bun.In(ids)).Scan(ctx, &groups); err != nil {
		return nil, err
	}
	summaries := make(map[string]*ConversationSummary, len(ids))
	// 同一会话的摘要以后写入的一类为准。
	putServices := func() {
		for _, row := range services {
			summary := row.summary()
			summaries[row.ID] = &summary
		}
	}
	if !serviceView {
		putServices()
	}
	for _, row := range directs {
		summary := row.summary()
		summaries[row.ID] = &summary
	}
	for _, row := range agents {
		summary := row.summary()
		summaries[row.ID] = &summary
	}
	for _, row := range groups {
		summary := row.summary()
		summaries[row.ID] = &summary
	}
	if serviceView {
		putServices()
	}
	return summaries, nil
}

// matchInboxIDs 复用列表筛选与置顶分区核对指定 ID，不受分页边界限制，返回匹配会话的候选位置。
func (q *LoadInboxQuery) matchInboxIDs(ctx context.Context, identity *servermodels.Identity, ids []string, input LoadInput) (map[string]inboxCursorPoint, error) {
	var points []inboxCursorPoint
	if err := q.candidatePointsQuery(identity, input).Where("candidates.id IN (?)", bun.In(ids)).Scan(ctx, &points); err != nil {
		return nil, err
	}
	matches := make(map[string]inboxCursorPoint, len(points))
	for _, point := range points {
		matches[point.ID] = point
	}
	return matches, nil
}
