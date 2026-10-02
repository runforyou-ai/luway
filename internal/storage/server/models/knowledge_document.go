//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// KnowledgeDocument 保存知识文档的业务归属、内容来源和原件关联。
type KnowledgeDocument struct {
	bun.BaseModel   `bun:"table:knowledge_documents,alias:kd"`
	ID              string                             `bun:"id,pk"`
	KnowledgeBaseID string                             `bun:"knowledge_base_id"`
	SourceKind      domain.KnowledgeDocumentSourceKind `bun:"source_kind"`
	Title           string                             `bun:"title"`
	SourceURL       string                             `bun:"source_url"`
	FileID          string                             `bun:"file_id,nullzero"`
	Status          domain.KnowledgeIndexStatus        `bun:"status"`
	ProcessingID    string                             `bun:"processing_id,nullzero"`
	SegmentBatchID  string                             `bun:"segment_batch_id,nullzero"`
	SegmentCount    int                                `bun:"segment_count"`
	FailureCode     string                             `bun:"failure_code"`
	ChunkLength     int                                `bun:"chunk_length"`
	ChunkOverlap    int                                `bun:"chunk_overlap"`
	CreatedByUserID string                             `bun:"created_by_user_id"`
	CreatedAt       time.Time                          `bun:"created_at"`
	UpdatedAt       time.Time                          `bun:"updated_at"`
}
