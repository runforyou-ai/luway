//go:build server

package models

import (
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
	"time"
)

// KnowledgeDocument 保存知识文档的业务归属、内容来源和原件关联。
type KnowledgeDocument struct {
	bun.BaseModel            `bun:"table:knowledge_documents,alias:kd"`
	ID                       string                             `bun:"id,pk"`
	KnowledgeBaseID          string                             `bun:"knowledge_base_id"`
	SourceKind               domain.KnowledgeDocumentSourceKind `bun:"source_kind"`
	Title                    string                             `bun:"title"`
	SourceURL                string                             `bun:"source_url"`
	FileID                   string                             `bun:"file_id,nullzero"`
	Status                   domain.KnowledgeIndexStatus        `bun:"status"`
	ProcessingID             string                             `bun:"processing_id,nullzero"`
	SegmentBatchID           string                             `bun:"segment_batch_id,nullzero"`
	SegmentCount             int                                `bun:"segment_count"`
	FailureCode              string                             `bun:"failure_code"`
	ChunkLength              int                                `bun:"chunk_length"`
	ChunkOverlap             int                                `bun:"chunk_overlap"`
	EmbeddingProviderID      string                             `bun:"embedding_provider_id,nullzero"`
	EmbeddingModelIdentifier string                             `bun:"embedding_model_identifier"`
	EmbeddingDimension       int                                `bun:"embedding_dimension"`
	CreatedByUserID          string                             `bun:"created_by_user_id"`
	CreatedAt                time.Time                          `bun:"created_at"`
	UpdatedAt                time.Time                          `bun:"updated_at"`
}
