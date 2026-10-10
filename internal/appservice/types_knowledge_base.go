package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// KnowledgeBaseCategory 表示知识库内容类型。
type KnowledgeBaseCategory = domain.KnowledgeBaseCategory

// KnowledgeBaseInput 定义知识库可编辑字段。
type KnowledgeBaseInput struct {
	EmbeddingModelID        string                `json:"embeddingModelId" validate:"uuid" msg:"field.knowledge_base_embedding_model_invalid"`
	EmbeddingDimension      int                   `json:"embeddingDimension" validate:"oneof=384 512 768 1024 1536 2048 3072" msg:"field.knowledge_base_embedding_dimension_invalid"`
	ChunkLength             *int                  `json:"chunkLength"`
	ChunkOverlap            *int                  `json:"chunkOverlap"`
	RetrievalCount          int                   `json:"retrievalCount" validate:"min=1,max=20" msg:"field.knowledge_base_retrieval_count_invalid"`
	RetrievalScoreThreshold float64               `json:"retrievalScoreThreshold" validate:"min=0,max=1" msg:"field.knowledge_base_retrieval_score_threshold_invalid"`
	RerankModelID           string                `json:"rerankModelId" validate:"uuid" msg:"field.knowledge_base_rerank_model_invalid"`
	Name                    string                `json:"name" validate:"notblank,max=120" msg:"notblank=field.knowledge_base_name_required,max=field.knowledge_base_name_too_long"`
	Category                KnowledgeBaseCategory `json:"category" validate:"oneof=standard qa" msg:"field.knowledge_base_category_invalid"`
	Description             string                `json:"description" validate:"max=1000" msg:"field.knowledge_base_description_too_long"`
}

// KnowledgeBase 定义知识库详情。
type KnowledgeBase struct {
	EmbeddingModelID        string                `json:"embeddingModelId"`
	EmbeddingDimension      int                   `json:"embeddingDimension"`
	ChunkLength             *int                  `json:"chunkLength"`
	ChunkOverlap            *int                  `json:"chunkOverlap"`
	RetrievalCount          int                   `json:"retrievalCount"`
	RetrievalScoreThreshold float64               `json:"retrievalScoreThreshold"`
	RerankModelID           string                `json:"rerankModelId"`
	ID                      string                `json:"id"`
	Name                    string                `json:"name"`
	Category                KnowledgeBaseCategory `json:"category"`
	Description             string                `json:"description"`
	CreatedAt               time.Time             `json:"createdAt"`
	UpdatedAt               time.Time             `json:"updatedAt"`
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
	Query string `json:"query" validate:"notblank,max=250" msg:"field.knowledge_retrieval_query_invalid"`
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
