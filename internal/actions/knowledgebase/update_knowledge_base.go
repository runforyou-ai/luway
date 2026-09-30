//go:build server

package knowledgebase

import (
	"context"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
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
	if !common.ValidUUID(knowledgeBaseID) {
		return nil, ErrNotFound
	}
	var record Record
	documentCount, entryCount := 0, 0
	reindexed := false
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		stored, err := lockKnowledgeBase(ctx, tx, identity.Organization.ID, knowledgeBaseID)
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
		if err := validateModels(ctx, tx, identity.Organization.ID, input); err != nil {
			return err
		}
		// 向量模型、维度，或标准库的分段长度、重叠变化时重新索引。
		reindexed = stored.EmbeddingProviderID != input.EmbeddingProviderID || stored.EmbeddingModelIdentifier != input.EmbeddingModelIdentifier || stored.EmbeddingDimension != input.EmbeddingDimension ||
			input.Category == domain.KnowledgeBaseCategoryStandard && (stored.ChunkLength == nil || stored.ChunkOverlap == nil || *stored.ChunkLength != *input.ChunkLength || *stored.ChunkOverlap != *input.ChunkOverlap)
		result, err := tx.NewUpdate().Model((*servermodels.KnowledgeBase)(nil)).
			Set("name = ?", input.Name).
			Set("category = ?", input.Category).
			Set("description = ?", input.Description).
			Set("embedding_provider_id = ?", bun.NullZero(input.EmbeddingProviderID)).
			Set("embedding_model_identifier = ?", input.EmbeddingModelIdentifier).
			Set("embedding_dimension = ?", input.EmbeddingDimension).
			Set("chunk_length = ?", input.ChunkLength).
			Set("chunk_overlap = ?", input.ChunkOverlap).
			Set("retrieval_count = ?", input.RetrievalCount).
			Set("retrieval_score_threshold = ?", input.RetrievalScoreThreshold).
			Set("rerank_provider_id = ?", input.RerankProviderID).
			Set("rerank_model_identifier = ?", input.RerankModelIdentifier).
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			Where("id = ?", knowledgeBaseID).
			Exec(ctx)
		if isConstraintConflict(err, "knowledge_bases_organization_name_unique") {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		if err := rowsAffectedOne(result, ErrNotFound); err != nil {
			return err
		}
		if reindexed {
			stored.EmbeddingProviderID, stored.EmbeddingModelIdentifier, stored.EmbeddingDimension = input.EmbeddingProviderID, input.EmbeddingModelIdentifier, input.EmbeddingDimension
			stored.ChunkLength, stored.ChunkOverlap = input.ChunkLength, input.ChunkOverlap
			documentCount, entryCount, err = a.reindex(ctx, tx, identity.Organization.ID, stored)
			if err != nil {
				return err
			}
		}
		updated, err := loadKnowledgeBaseRecord(ctx, tx, identity.Organization.ID, knowledgeBaseID)
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
		slog.Info("知识库索引参数变更，已清空分段并重新投递索引", "organization_id", identity.Organization.ID, "knowledge_base_id", knowledgeBaseID, "document_count", documentCount, "qa_entry_count", entryCount)
	}
	return &record, nil
}

// reindex 锁定知识库全部文档和问答条目，删除全部分段、清除已发布批次，并按新配置批量投递索引任务。
func (a *UpdateKnowledgeBaseAction) reindex(ctx context.Context, tx bun.Tx, organizationID string, base *servermodels.KnowledgeBase) (int, int, error) {
	knowledgeBaseID := base.ID
	var documents []*servermodels.KnowledgeDocument
	if err := tx.NewSelect().Model(&documents).Where("kd.knowledge_base_id = ?", knowledgeBaseID).Order("kd.id").For("UPDATE").Scan(ctx); err != nil {
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
	if err := a.documents.enqueue(ctx, tx, organizationID, base, false, documents...); err != nil {
		return 0, 0, err
	}
	if err := a.qaEntries.enqueue(ctx, tx, organizationID, base, entries...); err != nil {
		return 0, 0, err
	}
	return len(documents), len(entries), nil
}
