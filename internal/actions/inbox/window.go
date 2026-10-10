//go:build server

package inbox

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ConversationWindow 保存连续列表窗口、可双向续读的原始边界及本人置顶顺序版本。
type ConversationWindow struct {
	Conversations   []ConversationSummary
	StartCursor     string
	EndCursor       string
	PinOrderVersion int64
	HasBefore       bool
	HasAfter        bool
}

// candidatePointsQuery 复用所有列表窗口的资格、置顶与归档分区及最小排序投影；待处理范围另投影条目类型与等待起点。
func (q *LoadInboxQuery) candidatePointsQuery(identity *servermodels.Identity, input LoadInput) *bun.SelectQuery {
	query := q.db.NewSelect().TableExpr("(?) AS candidates", q.listCandidates(identity, input)).
		ColumnExpr("candidates.id, candidates.last_activity_at, cus.pin_rank").
		Join("LEFT JOIN conversation_user_states AS cus ON cus.workspace_id = ? AND cus.conversation_id = candidates.id AND cus.user_id = ?",
			identity.Workspace.ID, identity.User.ID)
	if input.Scope == domain.InboxScopePending {
		query = query.ColumnExpr("candidates.pending_kind, candidates.waiting_since, candidates.mentioned")
	}
	// 聊天范围的已归档分区只含本人已归档的聊天，其余分区排除这些聊天。
	if input.Scope == domain.InboxScopeChat {
		if input.Partition == domain.InboxPartitionArchived {
			return query.Where("cus.archived_at IS NOT NULL")
		}
		query = query.Where("cus.archived_at IS NULL")
	}
	switch input.Partition {
	case domain.InboxPartitionPinned:
		return query.Where("cus.pin_rank IS NOT NULL")
	case domain.InboxPartitionRegular:
		return query.Where("cus.pin_rank IS NULL")
	}
	return query
}

// constrainInboxPoint 按显示顺序施加前后边界，包含边界时也适用于闭区间重读。
func constrainInboxPoint(query *bun.SelectQuery, order inboxOrder, point inboxCursorPoint, before, inclusive bool) *bun.SelectQuery {
	if order == inboxOrderPinned || order == inboxOrderWaiting {
		// 置顶顺序值与等待起点都按升序展示，靠前即值更小。
		operator := ">"
		if before {
			operator = "<"
		}
		if inclusive {
			operator += "="
		}
		if order == inboxOrderPinned {
			return query.Where("(cus.pin_rank, candidates.id) "+operator+" (?, ?)", point.PinRank, point.ID)
		}
		return query.Where("(candidates.waiting_since, candidates.id) "+operator+" (?, ?)", point.WaitingSince, point.ID)
	}
	operator := "<"
	if before {
		operator = ">"
	}
	if inclusive {
		operator += "="
	}
	if point.LastActivityAt == nil {
		if before {
			return query.Where("(candidates.last_activity_at IS NOT NULL OR (candidates.last_activity_at IS NULL AND candidates.id "+operator+" ?))", point.ID)
		}
		return query.Where("candidates.last_activity_at IS NULL AND candidates.id "+operator+" ?", point.ID)
	}
	if before {
		return query.Where("(candidates.last_activity_at, candidates.id) "+operator+" (?, ?)", *point.LastActivityAt, point.ID)
	}
	return query.Where("((candidates.last_activity_at, candidates.id) "+operator+" (?, ?) OR candidates.last_activity_at IS NULL)", *point.LastActivityAt, point.ID)
}

// orderInboxPoints 按显示顺序和读取方向排列候选，向前读取时先取最近的邻居。
func orderInboxPoints(query *bun.SelectQuery, order inboxOrder, before bool) *bun.SelectQuery {
	switch order {
	case inboxOrderPinned:
		if before {
			return query.OrderExpr("cus.pin_rank DESC, candidates.id DESC")
		}
		return query.OrderExpr("cus.pin_rank ASC, candidates.id ASC")
	case inboxOrderWaiting:
		if before {
			return query.OrderExpr("candidates.waiting_since DESC, candidates.id DESC")
		}
		return query.OrderExpr("candidates.waiting_since ASC, candidates.id ASC")
	}
	if before {
		return query.OrderExpr("candidates.last_activity_at ASC NULLS FIRST, candidates.id ASC")
	}
	return query.OrderExpr("candidates.last_activity_at DESC NULLS LAST, candidates.id DESC")
}

// readNeighborPoints 从边界向指定方向读取最近的候选，并统一返回显示顺序。
func (q *LoadInboxQuery) readNeighborPoints(ctx context.Context, identity *servermodels.Identity, input LoadInput, point *inboxCursorPoint, before bool, limit int) ([]inboxCursorPoint, error) {
	query := q.candidatePointsQuery(identity, input)
	if point != nil {
		query = constrainInboxPoint(query, input.order(), *point, before, false)
	}
	var points []inboxCursorPoint
	if err := orderInboxPoints(query, input.order(), before).Limit(int64(limit)).Scan(ctx, &points); err != nil {
		return nil, fmt.Errorf("read inbox neighbors: %w", err)
	}
	if before {
		slices.Reverse(points)
	}
	return points, nil
}

// pinOrderVersion 读取当前用户的个人置顶顺序版本。
func (q *LoadInboxQuery) pinOrderVersion(ctx context.Context, identity *servermodels.Identity) (int64, error) {
	var version int64
	if err := q.db.NewSelect().Table("users").Column("pin_order_version").
		Where("workspace_id = ? AND id = ?", identity.Workspace.ID, identity.User.ID).
		Scan(ctx, &version); err != nil {
		return 0, fmt.Errorf("read pin order version: %w", err)
	}
	return version, nil
}

// authorizeInboxCursor 校验游标所属分区仍然有效，置顶区游标还要求个人顺序版本未变。
func authorizeInboxCursor(cursor *inboxCursor, partition domain.InboxPartition, pinOrderVersion int64) error {
	if partition != domain.InboxPartitionPinned {
		return nil
	}
	if cursor.PinRank == nil || cursor.PinOrderVersion != pinOrderVersion {
		return ErrCursorInvalid
	}
	return nil
}

// buildConversationWindow 在调用方快照内读取摘要、位置游标及窗口外资格。
func (q *LoadInboxQuery) buildConversationWindow(ctx context.Context, identity *servermodels.Identity, input LoadInput, pinOrderVersion int64, points []inboxCursorPoint, start, end *inboxCursorPoint) (ConversationWindow, error) {
	window := ConversationWindow{Conversations: make([]ConversationSummary, 0, len(points)), PinOrderVersion: pinOrderVersion}
	if start != nil {
		var err error
		window.StartCursor, err = encodeInboxCursor(identity, input, pinOrderVersion, *start)
		if err != nil {
			return window, err
		}
		window.EndCursor, err = encodeInboxCursor(identity, input, pinOrderVersion, *end)
		if err != nil {
			return window, err
		}
		window.HasBefore, err = constrainInboxPoint(q.candidatePointsQuery(identity, input), input.order(), *start, true, false).Exists(ctx)
		if err != nil {
			return window, fmt.Errorf("probe inbox before window: %w", err)
		}
		window.HasAfter, err = constrainInboxPoint(q.candidatePointsQuery(identity, input), input.order(), *end, false, false).Exists(ctx)
		if err != nil {
			return window, fmt.Errorf("probe inbox after window: %w", err)
		}
	}
	if len(points) == 0 {
		return window, nil
	}
	ids := arr.Map(points, func(point inboxCursorPoint) string { return point.ID })
	summaries, err := q.readSummaries(ctx, identity, ids, input.serviceView())
	if err != nil {
		return window, err
	}
	// 候选与摘要共用阅读资格和快照，位置游标保留当前查询及数据库精度。
	for _, point := range points {
		summary := *summaries[point.ID]
		summary.Pending = point.pending()
		summary.PositionCursor, err = encodeInboxCursor(identity, input, pinOrderVersion, point)
		if err != nil {
			return window, err
		}
		window.Conversations = append(window.Conversations, summary)
	}
	return window, nil
}
