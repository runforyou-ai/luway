//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// KnowledgeBase 表示 PostgreSQL 中的企业知识库。
type KnowledgeBase struct {
	bun.BaseModel `bun:"table:knowledge_bases,alias:kb"`

	EmbeddingModelID        string  `bun:"embedding_model_id"`
	EmbeddingDimension      int     `bun:"embedding_dimension"`
	ChunkLength             *int    `bun:"chunk_length"`
	ChunkOverlap            *int    `bun:"chunk_overlap"`
	RetrievalCount          int     `bun:"retrieval_count"`
	RetrievalScoreThreshold float64 `bun:"retrieval_score_threshold"`
	RerankModelID           string  `bun:"rerank_model_id"`

	ID              string    `bun:"id,pk"`
	WorkspaceID     string    `bun:"workspace_id"`
	CreatedByUserID string    `bun:"created_by_user_id"`
	Name            string    `bun:"name"`
	Category        string    `bun:"category"`
	Description     string    `bun:"description"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}
