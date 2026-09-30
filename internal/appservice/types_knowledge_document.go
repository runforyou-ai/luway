package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// KnowledgeDocumentFormat 定义允许上传的文档扩展名。
type KnowledgeDocumentFormat string

const (
	KnowledgeDocumentTXT      KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentTXT)
	KnowledgeDocumentMD       KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentMD)
	KnowledgeDocumentMarkdown KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentMarkdown)
	KnowledgeDocumentHTML     KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentHTML)
	KnowledgeDocumentHTM      KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentHTM)
	KnowledgeDocumentPDF      KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentPDF)
	KnowledgeDocumentDOCX     KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentDOCX)
	KnowledgeDocumentPPTX     KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentPPTX)
	KnowledgeDocumentXLSX     KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentXLSX)
	KnowledgeDocumentCSV      KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentCSV)
	KnowledgeDocumentJSON     KnowledgeDocumentFormat = KnowledgeDocumentFormat(domain.KnowledgeDocumentJSON)
)

// KnowledgeDocumentSourceKind 定义文档的内容来源。
type KnowledgeDocumentSourceKind string

const (
	KnowledgeDocumentSourceFile KnowledgeDocumentSourceKind = KnowledgeDocumentSourceKind(domain.KnowledgeDocumentSourceFile)
	KnowledgeDocumentSourceText KnowledgeDocumentSourceKind = KnowledgeDocumentSourceKind(domain.KnowledgeDocumentSourceText)
	KnowledgeDocumentSourceWeb  KnowledgeDocumentSourceKind = KnowledgeDocumentSourceKind(domain.KnowledgeDocumentSourceWeb)
)

// KnowledgeDocument 定义文档列表与预览页使用的元数据。
type KnowledgeDocument struct {
	Format           KnowledgeDocumentFormat        `json:"format"`
	SourceKind       KnowledgeDocumentSourceKind    `json:"sourceKind"`
	ID               string                         `json:"id"`
	Name             string                         `json:"name"`
	SourceURL        string                         `json:"sourceUrl"`
	ContentType      string                         `json:"contentType"`
	ByteSize         int64                          `json:"byteSize"`
	Status           KnowledgeIndexStatus           `json:"status"`
	ProcessingStatus KnowledgeIndexProcessingStatus `json:"processingStatus"`
	SegmentBatchID   string                         `json:"segmentBatchId"`
	SegmentCount     int                            `json:"segmentCount"`
	FailureMessage   string                         `json:"failureMessage"`
	UpdatedAt        time.Time                      `json:"updatedAt"`
}

// KnowledgeDocumentListInput 定义知识库文档的查询参数。
type KnowledgeDocumentListInput struct {
	Keyword  string `json:"keyword" query:"keyword"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=20"`
}

// KnowledgeDocumentList 返回文档及分页信息。
type KnowledgeDocumentList struct {
	Documents []KnowledgeDocument `json:"documents"`
	Page      PageInfo            `json:"page"`
}

// KnowledgeDocumentBatchInput 将最多十个已上传原件保存到知识库。
type KnowledgeDocumentBatchInput struct {
	FileIDs []string `json:"fileIds"`
}

// KnowledgeDocumentBatch 返回已保存文档，供重试核对。
type KnowledgeDocumentBatch struct {
	Documents []KnowledgeDocument `json:"documents"`
}

// KnowledgeTextDocumentInput 定义在线编写文档的名称与正文。
type KnowledgeTextDocumentInput struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// KnowledgeWebDocumentInput 定义网页导入文档的名称与页面地址。
type KnowledgeWebDocumentInput struct {
	Title     string `json:"title"`
	SourceURL string `json:"sourceUrl"`
}

// KnowledgeDocumentRefetchInput 定义重新抓取时可选的新页面地址。
type KnowledgeDocumentRefetchInput struct {
	SourceURL string `json:"sourceUrl"`
}

// KnowledgeDocumentContentInput 定义在线文档的名称与正文更新。
type KnowledgeDocumentContentInput struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// KnowledgeDocumentRenameInput 定义文档名称更新。
type KnowledgeDocumentRenameInput struct {
	Title string `json:"title"`
}

// KnowledgeDocumentContent 返回文档元数据与正文。
type KnowledgeDocumentContent struct {
	Document KnowledgeDocument `json:"document"`
	Content  string            `json:"content"`
}

// KnowledgeDocumentPreviewRequest 定义读取原件用于本地预览的请求。
type KnowledgeDocumentPreviewRequest struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// KnowledgeDocumentSegmentInput 定义固定批次中的分页和锚点定位。
type KnowledgeDocumentSegmentInput struct {
	SegmentBatchID  string `json:"segmentBatchId" query:"segmentBatchId"`
	AnchorSegmentID string `json:"anchorSegmentId" query:"anchorSegmentId"`
	Page            int    `json:"page" query:"page,default=0"`
	PageSize        int    `json:"pageSize" query:"pageSize,default=20"`
}

// KnowledgeDocumentSegment 定义可阅读和定位的分段正文，上下文为分段所属的标题路径和表头。
type KnowledgeDocumentSegment struct {
	ID             string `json:"id"`
	Position       int    `json:"position"`
	Context        string `json:"context"`
	Content        string `json:"content"`
	CharacterCount int    `json:"characterCount"`
}

// KnowledgeDocumentSegmentPage 返回一页分段及锚点位置。
type KnowledgeDocumentSegmentPage struct {
	SegmentBatchID  string                     `json:"segmentBatchId"`
	Segments        []KnowledgeDocumentSegment `json:"segments"`
	Page            PageInfo                   `json:"page"`
	AnchorSegmentID string                     `json:"anchorSegmentId"`
	AnchorPosition  int                        `json:"anchorPosition"`
}
