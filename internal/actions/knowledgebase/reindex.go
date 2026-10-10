//go:build server

package knowledgebase

import (
	"context"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// EmbeddingReindexer 重新索引引用指定向量模型的知识库。
type EmbeddingReindexer struct {
	documents *DocumentProcessing
	qaEntries *QAProcessing
}

// NewEmbeddingReindexer 创建按向量模型重新索引知识库的调度器。
func NewEmbeddingReindexer(db *bun.DB, tasks servertask.TxEnqueuer) *EmbeddingReindexer {
	return &EmbeddingReindexer{documents: NewDocumentProcessing(db, tasks), qaEntries: NewQAProcessing(db, tasks)}
}

// ReindexEmbeddingModels 在业务事务中锁定引用这些向量模型的知识库，清空分段并按当前配置重新投递索引。
func (r *EmbeddingReindexer) ReindexEmbeddingModels(ctx context.Context, tx bun.Tx, workspaceID string, modelIDs []string) error {
	var bases []*servermodels.KnowledgeBase
	if err := tx.NewSelect().Model(&bases).
		Where("kb.workspace_id = ?", workspaceID).
		Where("kb.embedding_model_id IN (?)", bun.List(modelIDs)).
		Order("kb.id ASC").For("UPDATE").
		Scan(ctx); err != nil {
		return err
	}
	for _, base := range bases {
		documentCount, entryCount, err := reindexBase(ctx, tx, r.documents, r.qaEntries, workspaceID, base)
		if err != nil {
			return err
		}
		slog.InfoContext(logscope.WithWorkspace(ctx, workspaceID), "向量模型上游标识变更，已清空知识库分段并重新投递索引", "knowledge_base_id", base.ID, "embedding_model_id", base.EmbeddingModelID, "document_count", documentCount, "qa_entry_count", entryCount)
	}
	return nil
}

// reindexBase 锁定知识库全部文档和问答条目，删除全部分段、清除已发布批次，并按新配置批量投递索引任务。
func reindexBase(ctx context.Context, tx bun.Tx, documents *DocumentProcessing, qaEntries *QAProcessing, workspaceID string, base *servermodels.KnowledgeBase) (int, int, error) {
	knowledgeBaseID := base.ID
	var sources []*servermodels.KnowledgeDocument
	if err := tx.NewSelect().Model(&sources).Where("kd.knowledge_base_id = ?", knowledgeBaseID).Order("kd.id").For("UPDATE").Scan(ctx); err != nil {
		return 0, 0, err
	}
	var entries []*servermodels.KnowledgeQAEntry
	if err := tx.NewSelect().Model(&entries).Where("kqe.knowledge_base_id = ?", knowledgeBaseID).Order("kqe.id").For("UPDATE").Scan(ctx); err != nil {
		return 0, 0, err
	}
	if err := deleteKnowledgeBaseSegments(ctx, tx, knowledgeBaseID); err != nil {
		return 0, 0, err
	}
	if _, err := tx.NewUpdate().Model((*servermodels.KnowledgeDocument)(nil)).Set("segment_batch_id = NULL").Set("segment_count = 0").Where("knowledge_base_id = ?", knowledgeBaseID).Exec(ctx); err != nil {
		return 0, 0, err
	}
	if _, err := tx.NewUpdate().Model((*servermodels.KnowledgeQAEntry)(nil)).Set("segment_batch_id = NULL").Set("segment_count = 0").Where("knowledge_base_id = ?", knowledgeBaseID).Exec(ctx); err != nil {
		return 0, 0, err
	}
	if err := documents.enqueue(ctx, tx, workspaceID, base, false, sources...); err != nil {
		return 0, 0, err
	}
	if err := qaEntries.enqueue(ctx, tx, workspaceID, base, entries...); err != nil {
		return 0, 0, err
	}
	return len(sources), len(entries), nil
}
