//go:build server

package knowledgebase

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateKnowledgeBaseAction 创建企业知识库。
type CreateKnowledgeBaseAction struct {
	db *bun.DB
}

// NewCreateKnowledgeBaseAction 创建知识库新增操作。
func NewCreateKnowledgeBaseAction(db *bun.DB) *CreateKnowledgeBaseAction {
	return &CreateKnowledgeBaseAction{db: db}
}

// Execute 校验并创建当前企业的知识库。
func (a *CreateKnowledgeBaseAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	var record Record
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := validateModels(ctx, tx, identity.Organization.ID, input); err != nil {
			return err
		}
		knowledgeBase := &servermodels.KnowledgeBase{
			OrganizationID: identity.Organization.ID, CreatedByUserID: identity.User.ID,
			Name: input.Name, Category: string(input.Category), Description: input.Description,
			EmbeddingProviderID:      input.EmbeddingProviderID,
			EmbeddingModelIdentifier: input.EmbeddingModelIdentifier,
			EmbeddingDimension:       input.EmbeddingDimension,
			ChunkLength:              input.ChunkLength,
			ChunkOverlap:             input.ChunkOverlap,
			RetrievalCount:           input.RetrievalCount,
			RetrievalScoreThreshold:  input.RetrievalScoreThreshold,
			RerankProviderID:         input.RerankProviderID,
			RerankModelIdentifier:    input.RerankModelIdentifier,
		}
		_, err := tx.NewInsert().Model(knowledgeBase).
			Column("organization_id", "created_by_user_id", "name", "category", "description", "embedding_provider_id", "embedding_model_identifier", "embedding_dimension", "chunk_length", "chunk_overlap", "retrieval_count", "retrieval_score_threshold", "rerank_provider_id", "rerank_model_identifier").
			Returning("*").
			Exec(ctx)
		if isConstraintConflict(err, "knowledge_bases_organization_name_unique") {
			return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationNameDuplicate}}
		}
		if err != nil {
			return err
		}
		record = recordFromModel(*knowledgeBase)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create knowledge base: %w", err)
	}
	return &record, nil
}
