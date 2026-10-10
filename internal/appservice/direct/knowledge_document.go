//go:build server

package direct

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	filecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
)

// ListKnowledgeDocuments 返回当前企业知识库中的文档。
func (o *knowledgeOps) ListKnowledgeDocuments(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID string, input appservice.KnowledgeDocumentListInput) (appservice.KnowledgeDocumentList, error) {
	result, err := o.documentQuery.List(ctx, identity, baseID, knowledgeaction.DocumentListInput{Keyword: input.Keyword, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.KnowledgeDocumentList{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentReadFailed, identity.Workspace.ID, baseID)
	}
	return appservice.KnowledgeDocumentList{
		Documents: arr.Map(result.Documents, func(record knowledgeaction.DocumentRecord) appservice.KnowledgeDocument {
			return knowledgeDocumentFromAction(meta, record)
		}),
		Page: appservice.PageInfo{Number: result.Page, Size: result.PageSize, Total: result.Total},
	}, nil
}

// GetKnowledgeDocument 返回文档详情。
func (o *knowledgeOps) GetKnowledgeDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string) (appservice.KnowledgeDocument, error) {
	record, err := o.documentQuery.Get(ctx, identity, baseID, documentID)
	if err != nil {
		return appservice.KnowledgeDocument{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentReadFailed, identity.Workspace.ID, baseID)
	}
	return knowledgeDocumentFromAction(meta, *record), nil
}

// CreateKnowledgeDocuments 在事务中创建文档并激活已上传原件。
func (o *knowledgeOps) CreateKnowledgeDocuments(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID string, input appservice.KnowledgeDocumentBatchInput) (appservice.KnowledgeDocumentBatch, error) {
	records, err := o.createDocuments.Execute(ctx, identity, baseID, input.FileIDs)
	if err != nil {
		return appservice.KnowledgeDocumentBatch{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentSaveFailed, identity.Workspace.ID, baseID)
	}
	output := appservice.KnowledgeDocumentBatch{Documents: arr.Map(records, func(record knowledgeaction.DocumentRecord) appservice.KnowledgeDocument {
		return knowledgeDocumentFromAction(meta, record)
	})}
	slog.InfoContext(ctx, "知识文档已保存", "knowledge_base_id", baseID, "document_count", len(records))
	return output, nil
}

// DeleteKnowledgeDocument 删除文档并安排原件清理。
func (o *knowledgeOps) DeleteKnowledgeDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string) error {
	if err := o.deleteDocument.Execute(ctx, identity, baseID, documentID); err != nil {
		return knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentDeleteFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "知识文档已删除", "knowledge_base_id", baseID, "document_id", documentID)
	return nil
}

// CreateKnowledgeTextDocument 创建在线编写的文档并安排索引。
func (o *knowledgeOps) CreateKnowledgeTextDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID string, input appservice.KnowledgeTextDocumentInput) (appservice.KnowledgeDocument, error) {
	record, err := o.saveTextDocument.Execute(ctx, identity, baseID, "", knowledgeaction.TextDocumentInput{Title: input.Title, Content: input.Content})
	if err != nil {
		return appservice.KnowledgeDocument{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentSaveFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "在线文档已创建", "knowledge_base_id", baseID, "document_id", record.ID)
	return knowledgeDocumentFromAction(meta, *record), nil
}

// GetKnowledgeDocumentContent 返回在线文档正文或网页抓取快照。
func (o *knowledgeOps) GetKnowledgeDocumentContent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string) (appservice.KnowledgeDocumentContent, error) {
	record, err := o.documentQuery.Content(ctx, identity, baseID, documentID)
	if err != nil {
		return appservice.KnowledgeDocumentContent{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentReadFailed, identity.Workspace.ID, baseID)
	}
	return appservice.KnowledgeDocumentContent{Document: knowledgeDocumentFromAction(meta, record.Document), Content: record.Content}, nil
}

// UpdateKnowledgeDocumentContent 修改在线文档的名称与正文并安排索引。
func (o *knowledgeOps) UpdateKnowledgeDocumentContent(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string, input appservice.KnowledgeDocumentContentInput) (appservice.KnowledgeDocument, error) {
	record, err := o.saveTextDocument.Execute(ctx, identity, baseID, documentID, knowledgeaction.TextDocumentInput{Title: input.Title, Content: input.Content})
	if err != nil {
		return appservice.KnowledgeDocument{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentSaveFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "在线文档已保存", "knowledge_base_id", baseID, "document_id", documentID)
	return knowledgeDocumentFromAction(meta, *record), nil
}

// RenameKnowledgeDocument 修改在线文档或网页文档的名称。
func (o *knowledgeOps) RenameKnowledgeDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string, input appservice.KnowledgeDocumentRenameInput) (appservice.KnowledgeDocument, error) {
	record, err := o.renameDocument.Execute(ctx, identity, baseID, documentID, input.Title)
	if err != nil {
		return appservice.KnowledgeDocument{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentSaveFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "知识文档已改名", "knowledge_base_id", baseID, "document_id", documentID)
	return knowledgeDocumentFromAction(meta, *record), nil
}

// CreateKnowledgeWebDocument 导入网页并安排首次抓取。
func (o *knowledgeOps) CreateKnowledgeWebDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID string, input appservice.KnowledgeWebDocumentInput) (appservice.KnowledgeDocument, error) {
	record, err := o.createWebDocument.Execute(ctx, identity, baseID, knowledgeaction.WebDocumentInput{Title: input.Title, SourceURL: input.SourceURL})
	if err != nil {
		return appservice.KnowledgeDocument{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentSaveFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "网页文档已导入", "knowledge_base_id", baseID, "document_id", record.ID, "source_url", record.SourceURL)
	return knowledgeDocumentFromAction(meta, *record), nil
}

// RefetchKnowledgeDocument 重新抓取网页文档并重新索引。
func (o *knowledgeOps) RefetchKnowledgeDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string, input appservice.KnowledgeDocumentRefetchInput) error {
	if err := o.documentProcessing.Refetch(ctx, identity, baseID, documentID, input.SourceURL); err != nil {
		return knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentRetryFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "网页文档已提交重新抓取", "knowledge_base_id", baseID, "document_id", documentID)
	return nil
}

// GetKnowledgeDocumentPreview 按原件实际存储类型返回受控读取请求。
func (o *knowledgeOps) GetKnowledgeDocumentPreview(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string) (appservice.KnowledgeDocumentPreviewRequest, error) {
	record, err := o.documentQuery.File(ctx, identity, baseID, documentID)
	if err != nil {
		return appservice.KnowledgeDocumentPreviewRequest{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentReadFailed, identity.Workspace.ID, baseID)
	}
	if record.StorageBackend == string(domain.FileStorageBackendLocal) {
		url, err := o.files.links.URL(domain.FileStorageBackendLocal, record.StorageKey)
		if err != nil {
			return appservice.KnowledgeDocumentPreviewRequest{}, fileOperationError(meta, err, i18n.ErrorKnowledgeDocumentReadFailed)
		}
		return appservice.KnowledgeDocumentPreviewRequest{URL: url, Headers: map[string]string{"Authorization": "Bearer " + meta.Token}}, nil
	}
	request, err := filecontent.PresignDownload(ctx, o.files.s3(), record.StorageKey, "inline")
	if err != nil {
		return appservice.KnowledgeDocumentPreviewRequest{}, fileOperationError(meta, err, i18n.ErrorKnowledgeDocumentReadFailed)
	}
	return appservice.KnowledgeDocumentPreviewRequest{URL: request.URL, Headers: map[string]string{}}, nil
}

// knowledgeDocumentFromAction 转换本地文档元数据。
func knowledgeDocumentFromAction(meta appservice.RequestMeta, record knowledgeaction.DocumentRecord) appservice.KnowledgeDocument {
	status, message := knowledgeIndexPresentation(meta, record.Status, record.FailureCode)
	// 在线文档与网页文档的正文是 Markdown，名称不带扩展名。
	format := domain.KnowledgeDocumentMD
	if record.SourceKind == domain.KnowledgeDocumentSourceFile {
		format = appservice.KnowledgeDocumentFormat(strings.ToLower(filepath.Ext(record.Name)))
	}
	return appservice.KnowledgeDocument{ProcessingStatus: appservice.KnowledgeIndexProcessingStatus(record.Status), SegmentBatchID: record.SegmentBatchID, SegmentCount: record.SegmentCount, FailureMessage: message, Format: format, SourceKind: record.SourceKind, ID: record.ID, Name: record.Name, SourceURL: record.SourceURL, ContentType: record.ContentType, ByteSize: record.ByteSize, Status: status, UpdatedAt: record.UpdatedAt}
}

// RetryKnowledgeDocument 按当前配置为文档安排新的处理任务。
func (o *knowledgeOps) RetryKnowledgeDocument(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string) error {
	if err := o.documentProcessing.Retry(ctx, identity, baseID, documentID); err != nil {
		return knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentRetryFailed, identity.Workspace.ID, baseID)
	}
	slog.InfoContext(ctx, "知识文档已提交重试", "knowledge_base_id", baseID, "document_id", documentID)
	return nil
}

// ListKnowledgeDocumentSegments 返回可连续阅读的一页分段。
func (o *knowledgeOps) ListKnowledgeDocumentSegments(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, baseID, documentID string, input appservice.KnowledgeDocumentSegmentInput) (appservice.KnowledgeDocumentSegmentPage, error) {
	page, err := o.documentQuery.Segments(ctx, identity, baseID, documentID, knowledgeaction.SegmentQueryInput{Page: input.Page, PageSize: input.PageSize, SegmentBatchID: input.SegmentBatchID, AnchorSegmentID: input.AnchorSegmentID})
	if err != nil {
		return appservice.KnowledgeDocumentSegmentPage{}, knowledgeBaseError(ctx, meta, err, i18n.ErrorKnowledgeDocumentReadFailed, identity.Workspace.ID, baseID)
	}
	return appservice.KnowledgeDocumentSegmentPage{
		SegmentBatchID: page.SegmentBatchID, Page: appservice.PageInfo{Number: page.Page, Size: page.PageSize, Total: page.Total}, AnchorSegmentID: page.AnchorSegmentID, AnchorPosition: page.AnchorPosition,
		Segments: arr.Map(page.Segments, func(segment knowledgeaction.Segment) appservice.KnowledgeDocumentSegment {
			return appservice.KnowledgeDocumentSegment{ID: segment.ID, Position: segment.Position, Context: segment.Context, Content: segment.Content, CharacterCount: segment.CharacterCount}
		}),
	}, nil
}
