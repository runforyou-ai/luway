//go:build server

package inbox

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ContextInput 指定目标会话、可选原位置和两侧窗口大小，零值大小默认为二十五条。
type ContextInput struct {
	Query        LoadInput
	AnchorID     string
	AnchorCursor string
	BeforeLimit  int
	AfterLimit   int
}

// ConversationContext 分别返回锚点当前资格与原位置附近的列表窗口。
type ConversationContext struct {
	Anchor ConversationResult
	Window ConversationWindow
}

// ReadContext 优先按原位置恢复邻域，没有原位置时只定位仍匹配筛选的锚点。
func (q *LoadInboxQuery) ReadContext(ctx context.Context, identity *servermodels.Identity, input ContextInput) (ConversationContext, error) {
	query, err := normalizeLoadInput(input.Query)
	if err != nil {
		return ConversationContext{}, err
	}
	if !common.ValidUUID(input.AnchorID) || input.BeforeLimit < 0 || input.AfterLimit < 0 {
		return ConversationContext{}, ErrQueryInvalid
	}
	// 统一已校验的 UUID 大小写，与数据库摘要和位置游标匹配。
	input.AnchorID = strings.ToLower(input.AnchorID)
	if input.BeforeLimit == 0 {
		input.BeforeLimit = 25
	}
	if input.AfterLimit == 0 {
		input.AfterLimit = 25
	}
	var original *inboxCursorPoint
	var originalCursor *inboxCursor
	if input.AnchorCursor != "" {
		cursor, err := decodeInboxCursor(input.AnchorCursor, identity, query)
		if err != nil {
			return ConversationContext{}, err
		}
		if cursor.ID != input.AnchorID {
			return ConversationContext{}, ErrCursorInvalid
		}
		originalCursor, original = cursor, &cursor.inboxCursorPoint
	}
	result := ConversationContext{Anchor: ConversationResult{ID: input.AnchorID}, Window: ConversationWindow{Conversations: []ConversationSummary{}}}
	err = q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		pinOrderVersion, err := snapshot.pinOrderVersion(ctx, identity)
		if err != nil {
			return err
		}
		result.Window.PinOrderVersion = pinOrderVersion
		if original != nil {
			if err := authorizeInboxCursor(originalCursor, query.Partition, pinOrderVersion); err != nil {
				return err
			}
		}
		summaries, err := snapshot.readSummaries(ctx, identity, []string{input.AnchorID}, query.serviceView())
		if err != nil {
			return err
		}
		// 锚点候选点同时给出当前分区资格和该分区的排序位置。
		var anchorPoints []inboxCursorPoint
		if err := snapshot.candidatePointsQuery(identity, query).Where("candidates.id = ?", input.AnchorID).Scan(ctx, &anchorPoints); err != nil {
			return fmt.Errorf("read inbox anchor point: %w", err)
		}
		result.Anchor.Conversation = summaries[input.AnchorID]
		result.Anchor.MatchesQuery = len(anchorPoints) > 0 && result.Anchor.Conversation != nil
		var current *inboxCursorPoint
		if result.Anchor.MatchesQuery {
			current = &anchorPoints[0]
			result.Anchor.Conversation.Pending = current.pending()
			result.Anchor.Conversation.PositionCursor, err = encodeInboxCursor(identity, query, pinOrderVersion, *current)
			if err != nil {
				return err
			}
		}
		point := original
		if point == nil {
			point = current
		}
		// 缺少可恢复位置时返回空窗口。
		if point == nil {
			return nil
		}
		before, err := snapshot.readNeighborPoints(ctx, identity, query, point, true, input.BeforeLimit)
		if err != nil {
			return err
		}
		after, err := snapshot.readNeighborPoints(ctx, identity, query, point, false, input.AfterLimit)
		if err != nil {
			return err
		}
		start, end := point, point
		if len(before) > 0 {
			start = &before[0]
		}
		if len(after) > 0 {
			end = &after[len(after)-1]
		}
		points := before
		// 原位置未变的锚点补入中心；已移动的锚点只按当前排序出现在前后邻域。
		if current != nil && compareInboxPoints(query.order(), *current, *point) == 0 {
			points = append(points, *current)
		}
		points = append(points, after...)
		result.Window, err = snapshot.buildConversationWindow(ctx, identity, query, pinOrderVersion, points, start, end)
		return err
	})
	return result, err
}
