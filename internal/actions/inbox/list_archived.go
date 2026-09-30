//go:build server

package inbox

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ArchivedInput 定义已归档聊天列表的类型筛选、名称搜索与页码；未选类型表示不限类型。
type ArchivedInput struct {
	Kinds    []domain.ConversationType
	Search   string
	Page     int
	PageSize int
}

// ArchivedPage 保存一页已归档的聊天及分页信息。
type ArchivedPage struct {
	Conversations []ConversationSummary
	Page          common.PageInfo
}

// ListArchived 在同一只读快照中按最近活动倒序读取本人已归档的群聊、单聊与 AI 聊天。
func (q *LoadInboxQuery) ListArchived(ctx context.Context, identity *servermodels.Identity, input ArchivedInput) (ArchivedPage, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return ArchivedPage{}, ErrQueryInvalid
	}
	load, err := normalizeLoadInput(LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionArchived, Kinds: input.Kinds, Search: input.Search})
	if err != nil {
		return ArchivedPage{}, err
	}
	result := ArchivedPage{Conversations: make([]ConversationSummary, 0), Page: common.PageInfo{Number: page, Size: pageSize}}
	err = q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		total, err := tx.NewSelect().TableExpr("(?) AS archived", snapshot.candidatePointsQuery(identity, load)).Count(ctx)
		if err != nil {
			return fmt.Errorf("count archived conversations: %w", err)
		}
		result.Page.Total = total
		var points []inboxCursorPoint
		if err := orderInboxPoints(snapshot.candidatePointsQuery(identity, load), inboxOrderActivity, false).
			Limit(pageSize).Offset((page-1)*pageSize).Scan(ctx, &points); err != nil {
			return fmt.Errorf("list archived conversations: %w", err)
		}
		if len(points) == 0 {
			return nil
		}
		ids := make([]string, len(points))
		for index, point := range points {
			ids[index] = point.ID
		}
		summaries, err := snapshot.readSummaries(ctx, identity, ids, false)
		if err != nil {
			return err
		}
		for _, point := range points {
			if summary, ok := summaries[point.ID]; ok {
				result.Conversations = append(result.Conversations, *summary)
			}
		}
		return nil
	})
	if err != nil {
		return ArchivedPage{}, err
	}
	return result, nil
}
