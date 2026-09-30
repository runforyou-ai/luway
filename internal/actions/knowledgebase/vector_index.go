//go:build server

package knowledgebase

import (
	"context"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

const (
	ReconcileVectorIndexesActionName = "knowledge.reconcile_vector_indexes"
	VectorIndexScheduleKey           = "knowledge.vector_indexes"

	// dedicatedVectorIndexThreshold 是建立知识库专属 HNSW 部分索引的已发布分段数量下限，更小的知识库按知识库过滤后精确排序。
	dedicatedVectorIndexThreshold = 10000
	// vectorIndexPrefix 是知识库专属向量索引名称前缀，名称后接去掉连字符的知识库编号与向量维度。
	vectorIndexPrefix = "knowledge_segments_hnsw_"
	// vectorIndexLockKey 是对账期间持有的会话级咨询锁编号，同一时刻只有一轮对账执行。
	vectorIndexLockKey = 7_302_021_002
)

// ReconcileVectorIndexesInput 定义知识库专属向量索引对账的输入。
type ReconcileVectorIndexesInput struct{}

// ReconcileVectorIndexesAction 使知识库专属向量索引与知识库规模和向量维度保持一致，db 为不限制长时间 DDL 的维护连接池。
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
	var backend struct {
		Locked bool `bun:"locked"`
		PID    int  `bun:"pid"`
	}
	if err := conn.NewRaw("SELECT pg_try_advisory_lock(?) AS locked, pg_backend_pid() AS pid", vectorIndexLockKey).Scan(ctx, &backend); err != nil {
		discardConn(conn)
		return fmt.Errorf("lock vector index reconciliation: %w", err)
	}
	if !backend.Locked {
		_ = conn.Close()
		slog.Info("知识库专属向量索引对账仍在执行，跳过本轮")
		return nil
	}
	// 数据库驱动不随上下文取消中断执行中的语句，上下文取消时经另一条连接取消对账连接上的语句。
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			if _, err := a.db.ExecContext(context.WithoutCancel(ctx), "SELECT pg_cancel_backend(?)", backend.PID); err != nil {
				slog.Warn("取消知识库专属向量索引对账语句失败", "error", err)
			}
		case <-finished:
		}
	}()
	// 上下文未取消且解锁成功时连接归还连接池，其余情况丢弃连接，会话级咨询锁随连接关闭释放。
	defer func() {
		close(finished)
		<-watcherDone
		if ctx.Err() == nil {
			if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock(?)", vectorIndexLockKey); err == nil {
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
	targets := make(map[string]wantedIndex, len(wanted))
	for _, item := range wanted {
		targets[vectorIndexName(item.KnowledgeBaseID, item.Dimension)] = item
	}
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
	built := map[string]bool{}
	for _, index := range existing {
		if _, ok := targets[index.Name]; ok && index.Valid {
			built[index.Name] = true
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `DROP INDEX CONCURRENTLY IF EXISTS public."`+index.Name+`"`); err != nil {
			return fmt.Errorf("drop knowledge vector index %s: %w", index.Name, err)
		}
	}
	for name, target := range targets {
		if built[name] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		started := time.Now()
		// 部分索引条件以字面量写入知识库编号与维度，检索查询以同样的字面量条件命中该索引。
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(
			`CREATE INDEX CONCURRENTLY IF NOT EXISTS "%s" ON public.knowledge_segments USING hnsw ((embedding::halfvec(%d)) halfvec_cosine_ops) WHERE knowledge_base_id = '%s' AND embedding_dimension = %d`,
			name, target.Dimension, target.KnowledgeBaseID, target.Dimension)); err != nil {
			return fmt.Errorf("create knowledge vector index %s: %w", name, err)
		}
		slog.Info("知识库专属向量索引创建完成", "knowledge_base_id", target.KnowledgeBaseID, "dimension", target.Dimension, "duration_ms", time.Since(started).Milliseconds())
	}
	return nil
}

// discardConn 关闭连接并使其不再回到连接池，连接上的会话状态随之释放。
func discardConn(conn bun.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
