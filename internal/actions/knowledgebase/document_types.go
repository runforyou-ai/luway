//go:build server

package knowledgebase

import (
	"errors"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

var (
	ErrDocumentNotFound          = errors.New("knowledge document not found")
	ErrDocumentUnsupported       = errors.New("knowledge document unsupported")
	ErrDocumentBatchInvalid      = errors.New("knowledge document batch must contain 1 to 10 distinct files")
	ErrDocumentSourceUnsupported = errors.New("knowledge document source unsupported")
	ErrDocumentURLDuplicate      = errors.New("knowledge document url duplicated")
)

// ProcessInput 固定本次文档任务的内容来源、分段和向量参数。
type ProcessInput struct {
	OrganizationID           string                             `json:"organizationId"`
	KnowledgeBaseID          string                             `json:"knowledgeBaseId"`
	DocumentID               string                             `json:"documentId"`
	SourceKind               domain.KnowledgeDocumentSourceKind `json:"sourceKind"`
	FetchPage                bool                               `json:"fetchPage"`
	ProcessingID             string                             `json:"processingId"`
	ChunkLength              int                                `json:"chunkLength"`
	ChunkOverlap             int                                `json:"chunkOverlap"`
	EmbeddingProviderID      string                             `json:"embeddingProviderId"`
	EmbeddingModelIdentifier string                             `json:"embeddingModelIdentifier"`
	EmbeddingDimension       int                                `json:"embeddingDimension"`
}

// ProcessError 定义知识来源索引的失败原因码和执行阶段。
type ProcessError struct {
	Code  string
	Stage domain.KnowledgeIndexStatus
}

// Error 返回语言无关的失败原因。
func (e *ProcessError) Error() string { return "knowledge processing: " + e.Code }

// DocumentRecord 汇总文档归属、内容来源与正文元数据。
type DocumentRecord struct {
	ID             string                             `bun:"id"`
	SourceKind     domain.KnowledgeDocumentSourceKind `bun:"source_kind"`
	SourceURL      string                             `bun:"source_url"`
	Name           string                             `bun:"name"`
	ContentType    string                             `bun:"content_type"`
	ByteSize       int64                              `bun:"byte_size"`
	Status         domain.KnowledgeIndexStatus        `bun:"status"`
	SegmentBatchID string                             `bun:"segment_batch_id"`
	SegmentCount   int                                `bun:"segment_count"`
	FailureCode    string                             `bun:"failure_code"`
	UpdatedAt      time.Time                          `bun:"updated_at"`
}

// DocumentListInput 定义知识库文档的分页查询条件。
type DocumentListInput struct {
	Keyword        string
	Page, PageSize int
}

// DocumentListOutput 返回文档分页结果。
type DocumentListOutput struct {
	Documents             []DocumentRecord
	Page, PageSize, Total int
}

// TextDocumentInput 定义在线编写文档的名称与正文。
type TextDocumentInput struct {
	Title   string
	Content string
}

// DocumentContentRecord 汇总文档元数据与正文。
type DocumentContentRecord struct {
	Document DocumentRecord
	Content  string
}

// WebDocumentInput 定义网页导入文档的名称与页面地址。
type WebDocumentInput struct {
	Title     string
	SourceURL string
}
