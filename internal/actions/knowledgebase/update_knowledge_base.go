//go:build server

package knowledgebase

import (
	"context"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// UpdateKnowledgeBaseAction 修改企业知识库，向量模型、维度或分段参数变化时重新索引全部资料。
type UpdateKnowledgeBaseAction struct {
	db        *bun.DB
	documents *DocumentProcessing
	qaEntries *QAProcessing
}

// NewUpdateKnowledgeBaseAction 创建知识库修改操作。
func NewUpdateKnowledgeBaseAction(db *bun.DB, tasks servertask.TxEnqueuer) *UpdateKnowledgeBaseAction {
	return &UpdateKnowledgeBaseAction{db: db, documents: NewDocumentProcessing(db, tasks), qaEntries: NewQAProcessing(db, tasks)}
}

// Execute 校验并修改当前企业的知识库。
func (a *UpdateKnowledgeBaseAction) Execute(ctx context.Context, identity *servermodels.Identity, knowledgeBaseID string, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	var record Record
	documentCount, entryCount := 0, 0
	reindexed := false
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := lockModels(ctx, tx, identity.Workspace.ID, input); err != nil {
			return err
		}
		stored, err := lockKnowledgeBase(ctx, tx, identity.Workspace.ID, knowledgeBaseID)
		if err != nil {
			return err
		}
		if stored.Category != string(input.Category) {
			occupied, err := tx.NewSelect().Model((*servermodels.KnowledgeQAEntry)(nil)).Where("knowledge_base_id = ?", knowledgeBaseID).Exists(ctx)
			if err != nil {
				return err
			}
			if occupied {
				return ErrBaseHasContent
			}
			occupied, err = tx.NewSelect().Model((*servermodels.KnowledgeDocument)(nil)).Where("knowledge_base_id = ?", knowledgeBaseID).Exists(ctx)
			if err != nil {
				return err
			}
			if occupied {
				return ErrBaseHasContent
			}
		}
		// 向量模型、维度，或标准库的分段长度、重叠变化时重新索引。
		reindexed = stored.EmbeddingModelID != input.EmbeddingModelID || stored.EmbeddingDimension != input.EmbeddingDimension ||
			input.Category == domain.KnowledgeBaseCategoryStandard && (stored.ChunkLength == nil || stored.ChunkOverlap == nil || *stored.ChunkLength != *input.ChunkLength || *stored.ChunkOverlap != *input.ChunkOverlap)
		result, err := tx.NewUpdate().Model((*servermodels.KnowledgeBase)(nil)).
			Set("name = ?", input.Name).
			Set("category = ?", input.Category).
			Set("description = ?", input.Description).
			Set("embedding_model_id = ?", input.EmbeddingModelID).
			Set("embedding_dimension = ?", input.EmbeddingDimension).
			Set("chunk_length = ?", input.ChunkLength).
			Set("chunk_overlap = ?", input.ChunkOverlap).
			Set("retrieval_count = ?", input.RetrievalCount).
			Set("retrieval_score_threshold = ?", input.RetrievalScoreThreshold).
			Set("rerank_model_id = ?", input.RerankModelID).
			Where("workspace_id = ?", identity.Workspace.ID).
			Where("id = ?", knowledgeBaseID).
			Exec(ctx)
		if pgerr.UniqueViolationOn(err, "knowledge_bases_workspace_name_unique") {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		if err := rowsAffectedOne(result, ErrNotFound); err != nil {
			return err
		}
		if reindexed {
			stored.EmbeddingModelID, stored.EmbeddingDimension = input.EmbeddingModelID, input.EmbeddingDimension
			stored.ChunkLength, stored.ChunkOverlap = input.ChunkLength, input.ChunkOverlap
			documentCount, entryCount, err = reindexBase(ctx, tx, a.documents, a.qaEntries, identity.Workspace.ID, stored)
			if err != nil {
				return err
			}
		}
		updated, err := loadKnowledgeBaseRecord(ctx, tx, identity.Workspace.ID, knowledgeBaseID)
		if err != nil {
			return err
		}
		record = *updated
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("update knowledge base: %w", err)
	}
	if reindexed {
		slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "知识库索引参数变更，已清空分段并重新投递索引", "knowledge_base_id", knowledgeBaseID, "document_count", documentCount, "qa_entry_count", entryCount)
	}
	return &record, nil
}
