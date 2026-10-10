//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// documentNameExpr 是文档名称的取值表达式，上传原件取文件名，其余来源取文档标题。
const documentNameExpr = "COALESCE(NULLIF(kd.title, ''), f.original_name)"

// DocumentQuery 读取当前企业中的文档、分段和可预览原件。
type DocumentQuery struct{ db *bun.DB }

// NewDocumentQuery 创建知识文档查询。
func NewDocumentQuery(db *bun.DB) *DocumentQuery { return &DocumentQuery{db: db} }

// List 按创建时间倒序返回知识库文档。
func (q *DocumentQuery) List(ctx context.Context, identity *servermodels.Identity, baseID string, input DocumentListInput) (DocumentListOutput, error) {
	base, err := loadKnowledgeBase(ctx, q.db, identity.Workspace.ID, baseID)
	if err != nil {
		return DocumentListOutput{}, err
	}
	if base.Category != string(domain.KnowledgeBaseCategoryStandard) {
		return DocumentListOutput{}, ErrDocumentUnsupported
	}
	var pageValid bool
	input.Page, input.PageSize, pageValid = common.NormalizePagination(input.Page, input.PageSize)
	if !pageValid {
		return DocumentListOutput{}, ErrPageSizeInvalid
	}
	records := make([]DocumentRecord, 0)
	query := documentSelect(q.db).Where("kd.knowledge_base_id = ?", baseID)
	if keyword := strings.TrimSpace(input.Keyword); keyword != "" {
		query = query.Where(documentNameExpr+" ILIKE ?", common.ContainsPattern(keyword))
	}
	total, err := query.OrderExpr("kd.created_at DESC, kd.id DESC").Limit(int64(input.PageSize)).Offset(int64((input.Page-1)*input.PageSize)).ScanAndCount(ctx, &records)
	return DocumentListOutput{Documents: records, Page: input.Page, PageSize: input.PageSize, Total: int(total)}, err
}

// Get 读取当前企业知识库中的文档详情。
func (q *DocumentQuery) Get(ctx context.Context, identity *servermodels.Identity, baseID, documentID string) (*DocumentRecord, error) {
	if _, err := loadKnowledgeBase(ctx, q.db, identity.Workspace.ID, baseID); err != nil {
		return nil, err
	}
	return loadDocumentRecord(ctx, q.db, baseID, documentID)
}

// Content 读取在线文档正文或网页抓取快照。
func (q *DocumentQuery) Content(ctx context.Context, identity *servermodels.Identity, baseID, documentID string) (*DocumentContentRecord, error) {
	if _, err := loadKnowledgeBase(ctx, q.db, identity.Workspace.ID, baseID); err != nil {
		return nil, err
	}
	record, err := loadDocumentRecord(ctx, q.db, baseID, documentID)
	if err != nil {
		return nil, err
	}
	if record.SourceKind == domain.KnowledgeDocumentSourceFile {
		return nil, ErrDocumentSourceUnsupported
	}
	content := &servermodels.KnowledgeDocumentContent{}
	err = q.db.NewSelect().Model(content).Where("kdc.document_id = ?", documentID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return &DocumentContentRecord{Document: *record}, nil
	}
	if err != nil {
		return nil, err
	}
	return &DocumentContentRecord{Document: *record, Content: content.Content}, nil
}

// File 读取仍关联当前企业文档的已激活原件。
func (q *DocumentQuery) File(ctx context.Context, identity *servermodels.Identity, baseID, documentID string) (*servermodels.File, error) {
	record := &servermodels.File{}
	err := q.db.NewSelect().Model(record).Join("JOIN knowledge_documents kd ON kd.file_id = f.id").Join("JOIN knowledge_bases kb ON kb.id = kd.knowledge_base_id").Where("kd.id = ? AND kb.id = ? AND kb.workspace_id = ? AND f.workspace_id = ?", documentID, baseID, identity.Workspace.ID, identity.Workspace.ID).Where("f.status = ?", domain.FileStatusActive).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDocumentNotFound
	}
	return record, err
}

// documentSelect 按内容来源取名称、内容类型和字节数，上传原件的属性仍从原件读取。
func documentSelect(db bun.IDB) *bun.SelectQuery {
	return db.NewSelect().TableExpr("knowledge_documents AS kd").
		ColumnExpr("kd.id, kd.source_kind, kd.source_url, kd.status, kd.segment_batch_id, kd.segment_count, kd.failure_code, kd.updated_at").
		ColumnExpr(documentNameExpr+" AS name").
		ColumnExpr("CASE WHEN kd.source_kind = ? THEN f.content_type ELSE ? END AS content_type", domain.KnowledgeDocumentSourceFile, domain.KnowledgeDocumentMarkdownContentType).
		ColumnExpr("CASE WHEN kd.source_kind = ? THEN f.byte_size ELSE COALESCE(octet_length(kdc.content), 0) END AS byte_size", domain.KnowledgeDocumentSourceFile).
		Join("LEFT JOIN files f ON f.id = kd.file_id").
		Join("LEFT JOIN knowledge_document_contents kdc ON kdc.document_id = kd.id")
}

// loadDocumentRecord 读取指定知识库下的一条文档。
func loadDocumentRecord(ctx context.Context, db bun.IDB, baseID, documentID string) (*DocumentRecord, error) {
	record := &DocumentRecord{}
	err := documentSelect(db).Where("kd.knowledge_base_id = ? AND kd.id = ?", baseID, documentID).Scan(ctx, record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDocumentNotFound
	}
	return record, err
}

// lockDocument 按知识库读取文档记录并持有行锁。
func lockDocument(ctx context.Context, db bun.IDB, baseID, documentID string) (*servermodels.KnowledgeDocument, error) {
	record := &servermodels.KnowledgeDocument{}
	err := db.NewSelect().Model(record).Where("kd.id = ? AND kd.knowledge_base_id = ?", documentID, baseID).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDocumentNotFound
	}
	return record, err
}
