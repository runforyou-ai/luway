//go:build server

package knowledgebase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/uptrace/bun"
)

const (
	// ReconcileVectorIndexesActionName 是知识库专属向量索引对账任务的 Action 名称。
	ReconcileVectorIndexesActionName = "knowledge.reconcile_vector_indexes"
	// VectorIndexScheduleKey 是知识库专属向量索引对账的定时计划键。
	VectorIndexScheduleKey = "knowledge.vector_indexes"

	// dedicatedVectorIndexThreshold 是建立知识库专属 HNSW 部分索引的已发布分段数量下限，更小的知识库按知识库过滤后精确排序。
	dedicatedVectorIndexThreshold = 10000
	// vectorIndexPrefix 是知识库专属向量索引名称前缀，名称后接去掉连字符的知识库编号与向量维度。
	vectorIndexPrefix = "knowledge_segments_hnsw_"
)

// ReconcileVectorIndexesInput 定义知识库专属向量索引对账的输入。
type ReconcileVectorIndexesInput struct{}

// ReconcileVectorIndexesAction 使知识库专属向量索引与知识库规模和向量维度保持一致，对账期间占用业务连接池中的一条连接执行索引构建。
type ReconcileVectorIndexesAction struct{ db *bun.DB }

// NewReconcileVectorIndexesAction 创建知识库专属向量索引对账 Action。
func NewReconcileVectorIndexesAction(db *bun.DB) *ReconcileVectorIndexesAction {
	return &ReconcileVectorIndexesAction{db: db}
}

// vectorIndexName 返回知识库在指定向量维度下的专属索引名称。
func vectorIndexName(knowledgeBaseID string, dimension int) string {
	return fmt.Sprintf("%s%s_%d", vectorIndexPrefix, strings.ReplaceAll(knowledgeBaseID, "-", ""), dimension)
}

// Execute 在同一连接上持有会话级咨询锁完成对账，上一轮仍在执行时跳过本轮；删除知识库已删除、维度变化或构建失败的专属索引，并为已发布分段达到阈值的知识库并发创建缺少的索引。
func (a *ReconcileVectorIndexesAction) Execute(ctx context.Context, _ ReconcileVectorIndexesInput) error {
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open vector index maintenance connection: %w", err)
	}
	locked, err := serverstorage.TrySessionLock(ctx, conn, serverstorage.LockVectorIndexes, "")
	if err != nil {
		discardConn(conn)
		return err
	}
	if !locked {
		_ = conn.Close()
		slog.InfoContext(ctx, "知识库专属向量索引对账仍在执行，跳过本轮")
		return nil
	}
	// 上下文未取消且解锁成功时连接归还连接池，其余情况丢弃连接，会话级咨询锁随连接关闭释放。
	defer func() {
		if ctx.Err() == nil {
			if err := serverstorage.SessionUnlock(ctx, conn, serverstorage.LockVectorIndexes); err == nil {
				_ = conn.Close()
				return
			}
		}
		discardConn(conn)
	}()
	type wantedIndex struct {
		KnowledgeBaseID string `bun:"knowledge_base_id"`
		Dimension       int    `bun:"embedding_dimension"`
	}
	var wanted []wantedIndex
	// 已发布分段数取自文档与问答条目记录的发布分段数量。
	if err := conn.NewRaw(`
		SELECT kb.id::text AS knowledge_base_id, kb.embedding_dimension
		FROM knowledge_bases AS kb
		JOIN (
			SELECT knowledge_base_id, segment_count FROM knowledge_documents
			UNION ALL
			SELECT knowledge_base_id, segment_count FROM knowledge_qa_entries
		) AS source ON source.knowledge_base_id = kb.id
		WHERE kb.embedding_dimension > 0
		GROUP BY kb.id, kb.embedding_dimension
		HAVING sum(source.segment_count) >= ?`, dedicatedVectorIndexThreshold).Scan(ctx, &wanted); err != nil {
		return fmt.Errorf("list knowledge bases for vector indexes: %w", err)
	}
	targets := arr.KeyBy(wanted, func(item wantedIndex) string { return vectorIndexName(item.KnowledgeBaseID, item.Dimension) })
	type existingIndex struct {
		Name  string `bun:"name"`
		Valid bool   `bun:"valid"`
	}
	var existing []existingIndex
	if err := conn.NewRaw(`
		SELECT c.relname AS name, i.indisvalid AS valid
		FROM pg_index AS i
		JOIN pg_class AS c ON c.oid = i.indexrelid
		WHERE i.indrelid = 'public.knowledge_segments'::regclass AND c.relname LIKE ?`, vectorIndexPrefix+"%").Scan(ctx, &existing); err != nil {
		return fmt.Errorf("list knowledge vector indexes: %w", err)
	}
	var built set.Set[string]
	for _, index := range existing {
		if _, ok := targets[index.Name]; ok && index.Valid {
			built.Add(index.Name)
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `DROP INDEX CONCURRENTLY IF EXISTS public."`+index.Name+`"`); err != nil {
			return fmt.Errorf("drop knowledge vector index %s: %w", index.Name, err)
		}
		slog.InfoContext(ctx, "知识库专属向量索引已删除", "index", index.Name, "valid", index.Valid)
	}
	for name, target := range targets {
		if built.Has(name) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now() //clock:local
		// 部分索引条件以字面量写入知识库编号与维度，检索查询以同样的字面量条件命中该索引。
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(
			`CREATE INDEX CONCURRENTLY IF NOT EXISTS "%s" ON public.knowledge_segments USING hnsw ((embedding::halfvec(%d)) halfvec_cosine_ops) WHERE knowledge_base_id = '%s' AND embedding_dimension = %d`,
			name, target.Dimension, target.KnowledgeBaseID, target.Dimension)); err != nil {
			return fmt.Errorf("create knowledge vector index %s: %w", name, err)
		}
		slog.InfoContext(ctx, "知识库专属向量索引创建完成", "knowledge_base_id", target.KnowledgeBaseID, "dimension", target.Dimension, "duration_ms", time.Since(started).Milliseconds()) //clock:local
	}
	return nil
}

// discardConn 关闭连接并将其从连接池丢弃，连接上的会话状态随之释放。
func discardConn(conn bun.Conn) {
	serverstorage.DiscardConn(conn)
	_ = conn.Close()
}
