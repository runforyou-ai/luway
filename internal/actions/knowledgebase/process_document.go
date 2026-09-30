//go:build server

package knowledgebase

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/documentconvert"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/embedding"
	"github.com/runforyou-ai/cervi/pkg/textsplit"
	"github.com/runforyou-ai/cervi/pkg/webfetch"
	"github.com/uptrace/bun"
)

type documentConverter interface {
	Convert(context.Context, string, io.Reader) (string, error)
}
type segmentEmbedder interface {
	Embed(context.Context, embedding.Credential, string, int, []string) ([][]float32, error)
}
type documentFileReader interface {
	Open(context.Context, *servermodels.File) (io.ReadCloser, error)
}
type pageFetcher interface {
	Fetch(context.Context, string) (webfetch.Page, error)
}

// ProcessDocumentAction 按内容来源取得正文，并执行分段、向量化与批次发布。
type ProcessDocumentAction struct {
	db        *bun.DB
	converter documentConverter
	embedder  segmentEmbedder
	files     documentFileReader
	pages     pageFetcher
}

// NewProcessDocumentAction 创建文档处理任务。
func NewProcessDocumentAction(db *bun.DB, converter documentConverter, embedder segmentEmbedder, files documentFileReader, pages pageFetcher) *ProcessDocumentAction {
	return &ProcessDocumentAction{db: db, converter: converter, embedder: embedder, files: files, pages: pages}
}

// Execute 执行当前文档任务，并在同一事务中写入分段与发布批次。
func (a *ProcessDocumentAction) Execute(ctx context.Context, input ProcessInput) error {
	started := time.Now()
	stored, hasStored, err := a.storedContent(ctx, input.DocumentID)
	if err != nil {
		return err
	}
	// 网页在创建、重新抓取或尚无快照时出网，其余情况使用已保存的正文，上传原件每次都转换原件。
	fetchPage := input.SourceKind == domain.KnowledgeDocumentSourceWeb && (input.FetchPage || !hasStored)
	useStored := input.SourceKind == domain.KnowledgeDocumentSourceText || (input.SourceKind == domain.KnowledgeDocumentSourceWeb && !fetchPage)
	firstStage := domain.KnowledgeIndexFetching
	if useStored {
		firstStage = domain.KnowledgeIndexSplitting
	}
	current, err := a.setStage(ctx, input, firstStage)
	if err != nil || !current {
		return err
	}
	slog.Info("知识文档处理开始", "document_id", input.DocumentID, "source_kind", input.SourceKind, "fetch_page", fetchPage, "processing_id", input.ProcessingID)
	markdown := stored
	if !useStored {
		if fetchPage {
			markdown, current, err = a.convertPage(ctx, input)
		} else {
			markdown, current, err = a.convertOriginal(ctx, input)
		}
		if err != nil || !current {
			return err
		}
		if current, err := a.setStage(ctx, input, domain.KnowledgeIndexSplitting); err != nil || !current {
			return err
		}
	}

	segments := textsplit.Split(markdown, input.ChunkLength, input.ChunkOverlap)
	if len(segments) == 0 {
		return &ProcessError{Code: "empty_content", Stage: domain.KnowledgeIndexSplitting}
	}
	published, err := embedAndPublish(ctx, a.db, a.embedder, indexPublication{
		Model:                    (*servermodels.KnowledgeDocument)(nil),
		Batch:                    segmentBatch{OrganizationID: input.OrganizationID, KnowledgeBaseID: input.KnowledgeBaseID, SourceType: domain.KnowledgeSourceDocument, SourceID: input.DocumentID, BatchID: input.ProcessingID, EmbeddingDimension: input.EmbeddingDimension},
		EmbeddingProviderID:      input.EmbeddingProviderID,
		EmbeddingModelIdentifier: input.EmbeddingModelIdentifier,
	}, segments)
	if err == nil && published {
		slog.Info("知识文档分段与向量完成", "document_id", input.DocumentID, "processing_id", input.ProcessingID, "segment_count", len(segments), "embedding_dimension", input.EmbeddingDimension, "duration_ms", time.Since(started).Milliseconds())
	}
	return err
}

// convertOriginal 读取上传原件并转换为 Markdown，任务已被替代时返回 false。
func (a *ProcessDocumentAction) convertOriginal(ctx context.Context, input ProcessInput) (string, bool, error) {
	file := &servermodels.File{}
	err := a.db.NewSelect().Model(file).Join("JOIN knowledge_documents kd ON kd.file_id = f.id").Where("kd.id = ? AND f.organization_id = ?", input.DocumentID, input.OrganizationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, &ProcessError{Code: "file_read_failed", Stage: domain.KnowledgeIndexFetching}
	}
	if err != nil {
		return "", false, err
	}
	source, err := a.files.Open(ctx, file)
	if err != nil {
		return "", false, &ProcessError{Code: "file_read_failed", Stage: domain.KnowledgeIndexFetching}
	}
	defer source.Close()
	if current, err := a.setStage(ctx, input, domain.KnowledgeIndexConverting); err != nil || !current {
		return "", false, err
	}
	markdown, err := a.converter.Convert(ctx, file.OriginalName, source)
	if err != nil {
		var failure *documentconvert.Error
		if errors.As(err, &failure) {
			return "", false, &ProcessError{Code: failure.Code, Stage: domain.KnowledgeIndexConverting}
		}
		return "", false, err
	}
	return markdown, true, nil
}

// storedContent 读取在线文档正文或网页抓取快照，第二个返回值表示是否已有正文记录。
func (a *ProcessDocumentAction) storedContent(ctx context.Context, documentID string) (string, bool, error) {
	content := &servermodels.KnowledgeDocumentContent{}
	err := a.db.NewSelect().Model(content).Where("kdc.document_id = ?", documentID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return content.Content, true, nil
}

// convertPage 抓取网页并转换为 Markdown，任务仍有效时保存本次快照。
func (a *ProcessDocumentAction) convertPage(ctx context.Context, input ProcessInput) (string, bool, error) {
	document := &servermodels.KnowledgeDocument{}
	err := a.db.NewSelect().Model(document).Where("kd.id = ?", input.DocumentID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	page, err := a.pages.Fetch(ctx, document.SourceURL)
	if err != nil {
		var failure *webfetch.Error
		if errors.As(err, &failure) {
			return "", false, &ProcessError{Code: failure.Code, Stage: domain.KnowledgeIndexFetching}
		}
		return "", false, err
	}
	if current, err := a.setStage(ctx, input, domain.KnowledgeIndexConverting); err != nil || !current {
		return "", false, err
	}
	// 原件转换器按文件扩展名选择转换方式。
	name := "page.txt"
	if page.ContentType == webfetch.ContentTypeHTML {
		name = "page.html"
	}
	markdown, err := a.converter.Convert(ctx, name, bytes.NewReader(page.Body))
	if err != nil {
		var failure *documentconvert.Error
		if errors.As(err, &failure) {
			return "", false, &ProcessError{Code: failure.Code, Stage: domain.KnowledgeIndexConverting}
		}
		return "", false, err
	}
	saved, err := a.saveSnapshot(ctx, input, markdown)
	if err != nil || !saved {
		return "", false, err
	}
	return markdown, true, nil
}

// saveSnapshot 持文档行锁核验当前任务后写入网页快照，任务已被替代或文档已删除时放弃写入。
func (a *ProcessDocumentAction) saveSnapshot(ctx context.Context, input ProcessInput, markdown string) (bool, error) {
	saved := false
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		document := &servermodels.KnowledgeDocument{}
		if err := tx.NewSelect().Model(document).Where("kd.id = ?", input.DocumentID).For("UPDATE").Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if document.ProcessingID != input.ProcessingID || !document.Status.IsProcessing() {
			return nil
		}
		content := &servermodels.KnowledgeDocumentContent{DocumentID: input.DocumentID, Content: markdown}
		if _, err := tx.NewInsert().Model(content).Column("document_id", "content").
			On("CONFLICT (document_id) DO UPDATE").Set("content = EXCLUDED.content").Set("updated_at = now()").Exec(ctx); err != nil {
			return err
		}
		saved = true
		return nil
	})
	return saved, err
}

// setStage 更新当前文档任务的执行阶段，任务已被替代或已进入终态时返回 false。
func (a *ProcessDocumentAction) setStage(ctx context.Context, input ProcessInput, stage domain.KnowledgeIndexStatus) (bool, error) {
	return updateIndexStage(ctx, a.db, (*servermodels.KnowledgeDocument)(nil), input.DocumentID, input.ProcessingID, stage)
}

// FinalizeFailure 保存当前文档任务的失败状态和原因码。
func (a *ProcessDocumentAction) FinalizeFailure(ctx context.Context, input ProcessInput, runErr error) error {
	code, stage := indexFailureCode(runErr)
	changed, err := finalizeIndexFailure(ctx, a.db, (*servermodels.KnowledgeDocument)(nil), input.DocumentID, input.ProcessingID, code)
	if err == nil && changed {
		slog.Warn("知识文档处理失败", "document_id", input.DocumentID, "processing_id", input.ProcessingID, "failure_code", code, "stage", stage)
	}
	return err
}
