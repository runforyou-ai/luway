//go:build server

package knowledgebase

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// Input 定义知识库可编辑字段。
type Input struct {
	EmbeddingProviderID      string
	EmbeddingModelIdentifier string
	EmbeddingDimension       int
	ChunkLength              *int
	ChunkOverlap             *int
	RetrievalCount           int
	RetrievalScoreThreshold  float64
	RerankProviderID         string
	RerankModelIdentifier    string
	Name                     string
	Category                 domain.KnowledgeBaseCategory
	Description              string
}

// Record 定义知识库详情字段。
type Record struct {
	EmbeddingProviderID      string
	EmbeddingModelIdentifier string
	EmbeddingDimension       int
	ChunkLength              *int
	ChunkOverlap             *int
	RetrievalCount           int
	RetrievalScoreThreshold  float64
	RerankProviderID         string
	RerankModelIdentifier    string
	ID                       string                       `bun:"id"`
	Name                     string                       `bun:"name"`
	Category                 domain.KnowledgeBaseCategory `bun:"category"`
	Description              string                       `bun:"description"`
	CreatedAt                time.Time                    `bun:"created_at"`
	UpdatedAt                time.Time                    `bun:"updated_at"`
}
