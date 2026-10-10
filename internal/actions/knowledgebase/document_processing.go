//go:build server

package knowledgebase

import (
	"context"
	"strings"
	"uuid"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/webfetch"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// ProcessDocumentActionName 是知识文档处理任务的 Action 名称。
const ProcessDocumentActionName = "knowledge.document.process"

// DocumentProcessing 安排文档处理和重新处理。
type DocumentProcessing struct {
	db    *bun.DB
	tasks servertask.TxEnqueuer
}

// NewDocumentProcessing 创建文档处理调度器。
func NewDocumentProcessing(db *bun.DB, tasks servertask.TxEnqueuer) *DocumentProcessing {
	return &DocumentProcessing{db: db, tasks: tasks}
}

// enqueue 固定当前分段参数，并在业务事务中按当前向量参数批量投递处理任务；fetchPage 表示网页来源本次是否重新抓取。
func (p *DocumentProcessing) enqueue(ctx context.Context, tx bun.IDB, workspaceID string, base *servermodels.KnowledgeBase, fetchPage bool, documents ...*servermodels.KnowledgeDocument) error {
	if len(documents) == 0 {
		return nil
	}
	ids := make([]string, len(documents))
	processingIDs := make([]string, len(documents))
	requests := make([]servertask.EnqueueRequest, len(documents))
	for index, document := range documents {
		document.ProcessingID = uuid.NewV7().String()
		document.ChunkLength, document.ChunkOverlap = *base.ChunkLength, *base.ChunkOverlap
		document.Status, document.FailureCode = domain.KnowledgeIndexQueued, ""
		ids[index], processingIDs[index] = document.ID, document.ProcessingID
		requests[index] = servertask.EnqueueRequest{ActionName: ProcessDocumentActionName, Payload: ProcessInput{
			WorkspaceID: workspaceID, KnowledgeBaseID: base.ID, DocumentID: document.ID, SourceKind: document.SourceKind, FetchPage: fetchPage, ProcessingID: document.ProcessingID,
			ChunkLength: document.ChunkLength, ChunkOverlap: document.ChunkOverlap,
			EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
		}, Options: servertask.EnqueueOptions{WorkspaceID: workspaceID, Queue: servertask.QueueKnowledge, MaxAttempts: 1, IdempotencyKey: document.ProcessingID}}
	}
	if _, err := tx.NewUpdate().Model((*servermodels.KnowledgeDocument)(nil)).
		TableExpr("unnest(?::uuid[], ?::uuid[]) AS batch(id, processing_id)", pgdialect.Array(ids), pgdialect.Array(processingIDs)).
		Set("processing_id = batch.processing_id").
		Set("chunk_length = ?, chunk_overlap = ?", *base.ChunkLength, *base.ChunkOverlap).
		Set("status = ?, failure_code = ''", domain.KnowledgeIndexQueued).
		Where("kd.id = batch.id").
		Exec(ctx); err != nil {
		return err
	}
	err := p.tasks.EnqueueManyIn(ctx, requests)
	return err
}

// Retry 按当前配置重新索引文档，网页来源读取已保存的快照。
func (p *DocumentProcessing) Retry(ctx context.Context, identity *servermodels.Identity, baseID, documentID string) error {
	return p.schedule(ctx, identity, baseID, documentID, "", false)
}

// Refetch 重新抓取网页文档，传入的地址非空时同时更新页面地址。
func (p *DocumentProcessing) Refetch(ctx context.Context, identity *servermodels.Identity, baseID, documentID, sourceURL string) error {
	return p.schedule(ctx, identity, baseID, documentID, sourceURL, true)
}

// schedule 在业务事务中锁定文档并投递一次处理。
func (p *DocumentProcessing) schedule(ctx context.Context, identity *servermodels.Identity, baseID, documentID, sourceURL string, fetchPage bool) error {
	return serverstorage.RunInTx(ctx, p.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		base, err := lockKnowledgeBase(ctx, tx, identity.Workspace.ID, baseID)
		if err != nil {
			return err
		}
		document, err := lockDocument(ctx, tx, baseID, documentID)
		if err != nil {
			return err
		}
		if fetchPage {
			if document.SourceKind != domain.KnowledgeDocumentSourceWeb {
				return ErrDocumentSourceUnsupported
			}
			if address := strings.TrimSpace(sourceURL); address != "" {
				normalized, err := webfetch.Normalize(address)
				if err != nil {
					return &common.FieldError{Fields: map[string]common.FieldCode{"sourceUrl": ValidationDocumentURLInvalid}}
				}
				if _, err := tx.NewUpdate().Model(document).Set("source_url = ?", normalized).WherePK().Exec(ctx); err != nil {
					if pgerr.UniqueViolationOn(err, "knowledge_documents_source_url_unique") {
						return ErrDocumentURLDuplicate
					}
					return err
				}
			}
		}
		return p.enqueue(ctx, tx, identity.Workspace.ID, base, fetchPage, document)
	})
}
