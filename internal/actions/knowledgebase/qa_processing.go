//go:build server

package knowledgebase

import (
	"context"
	"uuid"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// QAProcessing 安排问答条目的索引和重新索引。
type QAProcessing struct {
	db    *bun.DB
	tasks servertask.TxEnqueuer
}

// NewQAProcessing 创建问答索引调度器。
func NewQAProcessing(db *bun.DB, tasks servertask.TxEnqueuer) *QAProcessing {
	return &QAProcessing{db: db, tasks: tasks}
}

// enqueue 在业务事务中按当前向量参数批量投递索引任务。
func (p *QAProcessing) enqueue(ctx context.Context, tx bun.IDB, workspaceID string, base *servermodels.KnowledgeBase, entries ...*servermodels.KnowledgeQAEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, len(entries))
	processingIDs := make([]string, len(entries))
	requests := make([]servertask.EnqueueRequest, len(entries))
	for index, entry := range entries {
		entry.ProcessingID = uuid.NewV7().String()
		entry.Status, entry.FailureCode = domain.KnowledgeIndexQueued, ""
		ids[index], processingIDs[index] = entry.ID, entry.ProcessingID
		requests[index] = servertask.EnqueueRequest{ActionName: ProcessQAEntryActionName, Payload: ProcessQAInput{
			WorkspaceID: workspaceID, KnowledgeBaseID: base.ID, EntryID: entry.ID, ProcessingID: entry.ProcessingID,
			EmbeddingModelID: base.EmbeddingModelID, EmbeddingDimension: base.EmbeddingDimension,
		}, Options: servertask.EnqueueOptions{WorkspaceID: workspaceID, Queue: servertask.QueueKnowledge, MaxAttempts: 1, IdempotencyKey: entry.ProcessingID}}
	}
	if _, err := tx.NewUpdate().Model((*servermodels.KnowledgeQAEntry)(nil)).
		TableExpr("unnest(?::uuid[], ?::uuid[]) AS batch(id, processing_id)", pgdialect.Array(ids), pgdialect.Array(processingIDs)).
		Set("processing_id = batch.processing_id").
		Set("status = ?, failure_code = ''", domain.KnowledgeIndexQueued).
		Where("kqe.id = batch.id").
		Exec(ctx); err != nil {
		return err
	}
	err := p.tasks.EnqueueManyIn(ctx, requests)
	return err
}

// Retry 按当前知识库配置为问答条目投递一次索引任务。
func (p *QAProcessing) Retry(ctx context.Context, identity *servermodels.Identity, baseID, entryID string) error {
	return serverstorage.RunInTx(ctx, p.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		base, err := lockKnowledgeBase(ctx, tx, identity.Workspace.ID, baseID)
		if err != nil {
			return err
		}
		if err := validateQAKnowledgeBase(base); err != nil {
			return err
		}
		entry, err := lockQAEntry(ctx, tx, baseID, entryID)
		if err != nil {
			return err
		}
		return p.enqueue(ctx, tx, identity.Workspace.ID, base, entry)
	})
}
