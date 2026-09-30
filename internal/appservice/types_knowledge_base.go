package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// KnowledgeBaseCategory 表示知识库内容类型。
type KnowledgeBaseCategory string

const (
	KnowledgeBaseCategoryStandard KnowledgeBaseCategory = KnowledgeBaseCategory(domain.KnowledgeBaseCategoryStandard)
	KnowledgeBaseCategoryQA       KnowledgeBaseCategory = KnowledgeBaseCategory(domain.KnowledgeBaseCategoryQA)
)

// KnowledgeBaseInput 定义知识库可编辑字段。
type KnowledgeBaseInput struct {
	EmbeddingProviderID      string                `json:"embeddingProviderId"`
	EmbeddingModelIdentifier string                `json:"embeddingModelIdentifier"`
	EmbeddingDimension       int                   `json:"embeddingDimension"`
	ChunkLength              *int                  `json:"chunkLength"`
	ChunkOverlap             *int                  `json:"chunkOverlap"`
	RetrievalCount           int                   `json:"retrievalCount"`
	RetrievalScoreThreshold  float64               `json:"retrievalScoreThreshold"`
	RerankProviderID         string                `json:"rerankProviderId"`
	RerankModelIdentifier    string                `json:"rerankModelIdentifier"`
	Name                     string                `json:"name"`
	Category                 KnowledgeBaseCategory `json:"category"`
	Description              string                `json:"description"`
}

// KnowledgeBase 定义知识库详情。
type KnowledgeBase struct {
	EmbeddingProviderID      string                `json:"embeddingProviderId"`
	EmbeddingModelIdentifier string                `json:"embeddingModelIdentifier"`
	EmbeddingDimension       int                   `json:"embeddingDimension"`
	ChunkLength              *int                  `json:"chunkLength"`
	ChunkOverlap             *int                  `json:"chunkOverlap"`
	RetrievalCount           int                   `json:"retrievalCount"`
	RetrievalScoreThreshold  float64               `json:"retrievalScoreThreshold"`
	RerankProviderID         string                `json:"rerankProviderId"`
	RerankModelIdentifier    string                `json:"rerankModelIdentifier"`
	ID                       string                `json:"id"`
	Name                     string                `json:"name"`
	Category                 KnowledgeBaseCategory `json:"category"`
	Description              string                `json:"description"`
	CreatedAt                time.Time             `json:"createdAt"`
	UpdatedAt                time.Time             `json:"updatedAt"`
}

// KnowledgeBaseList 定义知识库列表。
type KnowledgeBaseList struct {
	KnowledgeBases []KnowledgeBase `json:"knowledgeBases"`
}

// KnowledgeBaseAgent 定义当前配置版本绑定知识库的 AI 员工。
type KnowledgeBaseAgent struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"displayName"`
	Status      UserStatus `json:"status"`
}

// KnowledgeBaseAgentList 定义绑定知识库的 AI 员工列表。
type KnowledgeBaseAgentList struct {
	Agents []KnowledgeBaseAgent `json:"agents"`
}

// KnowledgeRetrievalInput 定义检索测试的查询内容。
type KnowledgeRetrievalInput struct {
	Query string `json:"query"`
}

// KnowledgeRetrievalRecord 定义检索测试命中的来源片段和重排得分；问答记录的编号为条目编号并携带完整答案。
type KnowledgeRetrievalRecord struct {
	DocumentID     string  `json:"documentId"`
	DocumentName   string  `json:"documentName"`
	SegmentID      string  `json:"segmentId"`
	SegmentBatchID string  `json:"segmentBatchId"`
	Position       int     `json:"position"`
	Context        string  `json:"context"`
	Content        string  `json:"content"`
	Answer         string  `json:"answer"`
	Score          float64 `json:"score"`
}

// KnowledgeRetrievalResult 定义检索测试结果。
type KnowledgeRetrievalResult struct {
	Records []KnowledgeRetrievalRecord `json:"records"`
}
