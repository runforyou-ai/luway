//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// KnowledgeQAEntry 表示本地知识库中的一条完整问答及其索引状态。
type KnowledgeQAEntry struct {
	bun.BaseModel            `bun:"table:knowledge_qa_entries,alias:kqe"`
	ID                       string                      `bun:"id,pk"`
	KnowledgeBaseID          string                      `bun:"knowledge_base_id"`
	Status                   domain.KnowledgeIndexStatus `bun:"status"`
	ProcessingID             string                      `bun:"processing_id,nullzero"`
	SegmentBatchID           string                      `bun:"segment_batch_id,nullzero"`
	SegmentCount             int                         `bun:"segment_count"`
	FailureCode              string                      `bun:"failure_code"`
	EmbeddingProviderID      string                      `bun:"embedding_provider_id,nullzero"`
	EmbeddingModelIdentifier string                      `bun:"embedding_model_identifier"`
	EmbeddingDimension       int                         `bun:"embedding_dimension"`
	CreatedByUserID          string                      `bun:"created_by_user_id"`
	CreatedAt                time.Time                   `bun:"created_at"`
	UpdatedAt                time.Time                   `bun:"updated_at"`
}
