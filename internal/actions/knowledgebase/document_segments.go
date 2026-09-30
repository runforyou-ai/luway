//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	ErrSegmentsNotReady    = errors.New("knowledge document segments are not ready")
	ErrSegmentStale        = errors.New("knowledge document segment is stale")
	ErrSegmentQueryInvalid = errors.New("knowledge segment query is invalid")
)

// SegmentQueryInput 定义固定批次中的分页或锚点查询。
type SegmentQueryInput struct {
	SegmentBatchID  string
	AnchorSegmentID string
	Page, PageSize  int
}

// Segment 定义按来源顺序阅读的一段正文及其标题路径和表头。
type Segment struct {
	ID             string `bun:"id"`
	Position       int    `bun:"position"`
	Context        string `bun:"context"`
	Content        string `bun:"content"`
	CharacterCount int    `bun:"character_count"`
}

// SegmentPage 返回目标页和经核验的锚点。
type SegmentPage struct {
	SegmentBatchID  string
	Segments        []Segment
	Page            int
	PageSize        int
	Total           int
	AnchorSegmentID string
	AnchorPosition  int
}

// Segments 在同一快照中核验文档归属与已发布批次，并读取一页分段。
func (q *DocumentQuery) Segments(ctx context.Context, identity *servermodels.Identity, baseID, documentID string, input SegmentQueryInput) (SegmentPage, error) {
	var result SegmentPage
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := loadKnowledgeBase(ctx, tx, identity.Organization.ID, baseID); err != nil {
			return err
		}
		document, err := loadDocumentRecord(ctx, tx, baseID, documentID)
		if err != nil {
			return err
		}
		if document.SegmentBatchID == "" {
			return ErrSegmentsNotReady
		}
		if input.SegmentBatchID != "" && input.SegmentBatchID != document.SegmentBatchID {
			return ErrSegmentStale
		}
		if input.AnchorSegmentID != "" && (input.Page != 0 || input.SegmentBatchID == "") {
			return ErrSegmentQueryInvalid
		}
		if input.Page < 0 || input.PageSize < 0 || input.PageSize > 100 ||
			(input.AnchorSegmentID != "" && !common.ValidUUID(input.AnchorSegmentID)) {
			return ErrSegmentQueryInvalid
		}
		if input.PageSize == 0 {
			input.PageSize = 20
		}
		page, position := input.Page, 0
		if page == 0 {
			page = 1
		}
		scope := []any{identity.Organization.ID, baseID, documentID, document.SegmentBatchID}
		condition := "organization_id = ? AND knowledge_base_id = ? AND source_id = ? AND segment_batch_id = ?"
		if input.AnchorSegmentID != "" {
			err := tx.NewSelect().TableExpr("public.knowledge_segments").ColumnExpr("position").
				Where(condition, scope...).Where("id = ?", input.AnchorSegmentID).Scan(ctx, &position)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSegmentStale
			}
			if err != nil {
				return err
			}
			// 按锚点之前的实际分段数量计算所在页。
			preceding, err := tx.NewSelect().TableExpr("public.knowledge_segments").
				Where(condition, scope...).Where("position < ?", position).Count(ctx)
			if err != nil {
				return err
			}
			page = preceding/input.PageSize + 1
		}
		segments := make([]Segment, 0, input.PageSize)
		err = tx.NewSelect().TableExpr("public.knowledge_segments").
			ColumnExpr("id, context, content, position, character_count").
			Where(condition, scope...).OrderExpr("position, id").
			Limit(input.PageSize).Offset((page-1)*input.PageSize).Scan(ctx, &segments)
		if err != nil {
			return err
		}
		result = SegmentPage{
			SegmentBatchID: document.SegmentBatchID, Segments: segments,
			Page: page, PageSize: input.PageSize, Total: document.SegmentCount,
			AnchorSegmentID: input.AnchorSegmentID, AnchorPosition: position,
		}
		return nil
	})
	return result, err
}
