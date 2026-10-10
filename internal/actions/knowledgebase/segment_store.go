//go:build server

package knowledgebase

import (
	"context"
	"fmt"
	"strings"
	"uuid"

	"github.com/pgvector/pgvector-go"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

const (
	// segmentInsertSize 是单条插入语句写入的分段数量，受 PostgreSQL 绑定参数上限约束。
	segmentInsertSize = 500
	// segmentCandidateLimit 是词法路和向量路各自返回的候选数量。
	segmentCandidateLimit = 50
	// hnswEFSearch 是专属 HNSW 索引检索时的候选列表大小，取前 50 条时召回率约 0.98。
	hnswEFSearch = 400
	// lexicalMatchLimit 是词法路参与排名的最大命中数，超过时按近似排名处理。
	lexicalMatchLimit = 2000
)

// segmentBatch 固定一次分段发布的企业、知识库、来源、批次和向量维度。
type segmentBatch struct {
	WorkspaceID        string
	KnowledgeBaseID    string
	SourceType         domain.KnowledgeSourceType
	SourceID           string
	BatchID            string
	EmbeddingDimension int
}

// segmentHit 表示检索或阅读命中的一条已发布分段，来源名称为文档文件名或问答主问题。
type segmentHit struct {
	ID             string `bun:"id"`
	SourceID       string `bun:"source_id"`
	SourceName     string `bun:"source_name"`
	SegmentBatchID string `bun:"segment_batch_id"`
	Position       int    `bun:"position"`
	Context        string `bun:"context"`
	Content        string `bun:"content"`
}

// segmentColumns 返回检索和阅读结果读取的分段列，来源名称按知识库类别取文档名称或问答主问题。
func segmentColumns(base servermodels.KnowledgeBase) string {
	name := documentNameExpr
	if base.Category == string(domain.KnowledgeBaseCategoryQA) {
		name = "question.content"
	}
	return "ks.id, ks.source_id, " + name + " AS source_name, ks.segment_batch_id, ks.position, ks.context, ks.content"
}

// publishedSegments 按知识库类别联结来源记录，只选取与来源当前已发布批次一致的分段。
func publishedSegments(db bun.IDB, base servermodels.KnowledgeBase) *bun.SelectQuery {
	query := db.NewSelect().TableExpr("public.knowledge_segments AS ks").Where("ks.knowledge_base_id = ?", base.ID)
	if base.Category == string(domain.KnowledgeBaseCategoryQA) {
		return query.Join("JOIN knowledge_qa_entries kqe ON kqe.id = ks.source_id AND kqe.segment_batch_id = ks.segment_batch_id").
			Join("JOIN knowledge_qa_contents question ON question.entry_id = kqe.id AND question.kind = ?", domain.KnowledgeQAContentPrimaryQuestion)
	}
	return query.Join("JOIN knowledge_documents kd ON kd.id = ks.source_id AND kd.segment_batch_id = ks.segment_batch_id").
		Join("LEFT JOIN files f ON f.id = kd.file_id")
}

// searchableSegments 返回参与检索的已发布分段；只检索帮助中心文章时，文档知识库只保留在线编写的文档。
func searchableSegments(db bun.IDB, base servermodels.KnowledgeBase, articlesOnly bool) *bun.SelectQuery {
	query := publishedSegments(db, base)
	if articlesOnly && base.Category != string(domain.KnowledgeBaseCategoryQA) {
		query = query.Where("kd.source_kind = ?", domain.KnowledgeDocumentSourceText)
	}
	return query
}

// insertSegments 按批次标识和来源内序号写入本批次分段、上下文、向量和词法词元，词法词元由上下文与正文共同生成。
func insertSegments(ctx context.Context, tx bun.IDB, batch segmentBatch, segments []segment, vectors [][]float32) error {
	for start := 0; start < len(segments); start += segmentInsertSize {
		chunk := segments[start:min(start+segmentInsertSize, len(segments))]
		placeholders := make([]string, 0, len(chunk))
		arguments := make([]any, 0, len(chunk)*12)
		for offset, item := range chunk {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::vector, ?, ?::tsvector)")
			arguments = append(arguments, uuid.NewV7().String(),
				batch.WorkspaceID, batch.KnowledgeBaseID, batch.SourceType, batch.SourceID, batch.BatchID,
				item.Position, item.CharacterCount, item.Context, item.Content,
				pgvector.NewVector(vectors[start+offset]), batch.EmbeddingDimension, searchtext.WordVector(indexText(item.Context, item.Content)))
		}
		query := "INSERT INTO public.knowledge_segments (id, workspace_id, knowledge_base_id, source_type, source_id, segment_batch_id, position, character_count, context, content, embedding, embedding_dimension, search_vector) VALUES " + strings.Join(placeholders, ", ")
		if _, err := tx.ExecContext(ctx, query, arguments...); err != nil {
			return err
		}
	}
	return nil
}

// deleteSourceSegments 删除一个文档或问答条目的全部分段。
func deleteSourceSegments(ctx context.Context, tx bun.IDB, sourceID string) error {
	_, err := tx.NewDelete().TableExpr("public.knowledge_segments").Where("source_id = ?", sourceID).Exec(ctx)
	return err
}

// deleteKnowledgeBaseSegments 删除知识库的全部分段。
func deleteKnowledgeBaseSegments(ctx context.Context, tx bun.IDB, knowledgeBaseID string) error {
	_, err := tx.NewDelete().TableExpr("public.knowledge_segments").Where("knowledge_base_id = ?", knowledgeBaseID).Exec(ctx)
	return err
}

// searchSegmentsByVector 先在知识库分段内按余弦距离取候选，再联结来源读取已发布分段；有专属 HNSW 部分索引的知识库近似检索，其余按知识库过滤后精确排序；附加过滤条件时 HNSW 迭代扫描直到取满候选，维度以字面量写入，使查询条件与专属索引的部分索引条件一致。
func searchSegmentsByVector(ctx context.Context, db bun.IDB, base servermodels.KnowledgeBase, articlesOnly bool, vector []float32) ([]segmentHit, error) {
	nearest := db.NewSelect().TableExpr("public.knowledge_segments AS candidate").
		ColumnExpr("candidate.id").
		ColumnExpr(fmt.Sprintf("candidate.embedding::halfvec(%d) <=> ?::halfvec(%d) AS distance", base.EmbeddingDimension, base.EmbeddingDimension), pgvector.NewVector(vector)).
		Where("candidate.knowledge_base_id = ?", base.ID).
		Where(fmt.Sprintf("candidate.embedding_dimension = %d", base.EmbeddingDimension)).
		OrderExpr("distance").Limit(segmentCandidateLimit)
	// 只检索帮助中心文章时，候选阶段即限定为在线编写的文档。
	if articlesOnly && base.Category != string(domain.KnowledgeBaseCategoryQA) {
		nearest = nearest.Where("candidate.source_id IN (SELECT kd.id FROM knowledge_documents AS kd WHERE kd.knowledge_base_id = ? AND kd.source_kind = ?)", base.ID, domain.KnowledgeDocumentSourceText)
	}
	hits := make([]segmentHit, 0, segmentCandidateLimit)
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d; SET LOCAL hnsw.iterative_scan = strict_order", hnswEFSearch)); err != nil {
			return fmt.Errorf("set hnsw search options: %w", err)
		}
		return searchableSegments(tx, base, articlesOnly).ColumnExpr(segmentColumns(base)).
			Join("JOIN (?) AS nearest ON nearest.id = ks.id", nearest).
			OrderExpr("nearest.distance").Limit(segmentCandidateLimit).Scan(ctx, &hits)
	})
	return hits, err
}

// searchSegmentsByText 在候选上限内按覆盖密度排名词法命中的已发布分段。
func searchSegmentsByText(ctx context.Context, db bun.IDB, base servermodels.KnowledgeBase, articlesOnly bool, tsquery string) ([]segmentHit, error) {
	candidates := searchableSegments(db, base, articlesOnly).ColumnExpr(segmentColumns(base)).ColumnExpr("ks.search_vector").
		Where("ks.search_vector @@ ?::tsquery", tsquery).Limit(lexicalMatchLimit)
	hits := make([]segmentHit, 0, segmentCandidateLimit)
	err := db.NewSelect().With("candidates", candidates).TableExpr("candidates").
		ColumnExpr("id, source_id, source_name, segment_batch_id, position, context, content").
		OrderExpr("ts_rank_cd(search_vector, ?::tsquery) DESC, id", tsquery).
		Limit(segmentCandidateLimit).Scan(ctx, &hits)
	return hits, err
}

// readSegmentWindow 读取指定分段及其前后相邻分段；分段不属于来源当前已发布批次时返回 ErrSegmentStale。
func readSegmentWindow(ctx context.Context, db bun.IDB, base servermodels.KnowledgeBase, sourceID, segmentID, batchID string, before, after int) ([]segmentHit, error) {
	if !str.IsUUID(sourceID) || !str.IsUUID(segmentID) || !str.IsUUID(batchID) {
		return nil, ErrSegmentStale
	}
	// 锚点定位与取窗在同一条语句的快照内完成，锚点分段存在时结果必然包含锚点本身。
	anchor := publishedSegments(db, base).ColumnExpr("ks.position").
		Where("ks.source_id = ? AND ks.id = ? AND ks.segment_batch_id = ?", sourceID, segmentID, batchID)
	hits := make([]segmentHit, 0, before+after+1)
	err := publishedSegments(db, base).ColumnExpr(segmentColumns(base)).
		Join("JOIN (?) AS anchor ON ks.position BETWEEN anchor.position - ? AND anchor.position + ?", anchor, before, after).
		Where("ks.source_id = ? AND ks.segment_batch_id = ?", sourceID, batchID).
		OrderExpr("ks.position").Scan(ctx, &hits)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, ErrSegmentStale
	}
	return hits, nil
}
