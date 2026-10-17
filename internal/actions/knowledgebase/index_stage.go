//go:build server

package knowledgebase

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// resolveEmbeddingModel 读取同企业可用的向量模型及其来源；Auto 时按 sticky 钉住池成员，选型变化时写回知识库。
func resolveEmbeddingModel(ctx context.Context, db bun.IDB, workspaceID, knowledgeBaseID, modelID, stickyMemberID string) (*aimodel.Model, error) {
	model, newSticky, err := aimodel.ResolveAutoSticky(ctx, db, workspaceID, modelID, stickyMemberID, domain.AIModelUsageEmbedding)
	if errors.Is(err, aimodel.ErrUnavailable) {
		return nil, &modelcall.EmbeddingError{Code: "embedding_model_unavailable"}
	}
	if err != nil {
		return nil, err
	}
	if newSticky != stickyMemberID {
		if err := persistEmbeddingSticky(ctx, db, workspaceID, knowledgeBaseID, newSticky); err != nil {
			return nil, err
		}
	}
	return model, nil
}

// persistEmbeddingSticky 写入或清空知识库的 embedding Auto 钉住成员。
func persistEmbeddingSticky(ctx context.Context, db bun.IDB, workspaceID, knowledgeBaseID, stickyMemberID string) error {
	query := db.NewUpdate().Model((*servermodels.KnowledgeBase)(nil)).
		Where("workspace_id = ?", workspaceID).
		Where("id = ?", knowledgeBaseID)
	if stickyMemberID == "" {
		query = query.Set("embedding_sticky_member_id = NULL")
	} else {
		query = query.Set("embedding_sticky_member_id = ?", stickyMemberID)
	}
	_, err := query.Exec(ctx)
	return err
}

// loadEmbeddingSticky 读取知识库当前钉住的 embedding 池成员编号。
func loadEmbeddingSticky(ctx context.Context, db bun.IDB, workspaceID, knowledgeBaseID string) (string, error) {
	var sticky string
	err := db.NewSelect().Model((*servermodels.KnowledgeBase)(nil)).
		ColumnExpr("COALESCE(kb.embedding_sticky_member_id::text, '')").
		Where("kb.workspace_id = ?", workspaceID).
		Where("kb.id = ?", knowledgeBaseID).
		Scan(ctx, &sticky)
	return sticky, err
}

// indexPublication 固定一次分段发布的来源模型、分段批次、向量模型编号和模型调用所服务的业务对象类型。
type indexPublication struct {
	Model            any
	Batch            segmentBatch
	EmbeddingModelID string
	CallSource       domain.AIModelCallSource
}

// embedAndPublish 经统一调用入口向量化来源分段，并在当前任务仍有效时于同一事务中替换分段、发布批次；返回是否已发布。
func embedAndPublish(ctx context.Context, db *bun.DB, invoker *modelcall.Invoker, publication indexPublication, segments []segment) (bool, error) {
	batch := publication.Batch
	if current, err := updateIndexStage(ctx, db, publication.Model, batch.SourceID, batch.BatchID, domain.KnowledgeIndexEmbedding); err != nil || !current {
		return false, err
	}
	sticky, err := loadEmbeddingSticky(ctx, db, batch.WorkspaceID, batch.KnowledgeBaseID)
	if err != nil {
		return false, err
	}
	model, err := resolveEmbeddingModel(ctx, db, batch.WorkspaceID, batch.KnowledgeBaseID, publication.EmbeddingModelID, sticky)
	var failure *modelcall.EmbeddingError
	if errors.As(err, &failure) {
		return false, &ProcessError{Code: failure.Code, Stage: domain.KnowledgeIndexEmbedding}
	}
	if err != nil {
		return false, err
	}
	texts := arr.Map(segments, func(item segment) string { return indexText(item.Context, item.Content) })
	vectors, err := invoker.Embed(ctx, modelcall.SystemScope(batch.WorkspaceID, publication.CallSource, batch.SourceID), model, batch.EmbeddingDimension, texts)
	if errors.As(err, &failure) {
		return false, &ProcessError{Code: failure.Code, Stage: domain.KnowledgeIndexEmbedding}
	}
	if err != nil {
		return false, err
	}
	if len(vectors) != len(segments) {
		return false, &ProcessError{Code: "embedding_failed", Stage: domain.KnowledgeIndexEmbedding}
	}

	if current, err := updateIndexStage(ctx, db, publication.Model, batch.SourceID, batch.BatchID, domain.KnowledgeIndexPublishing); err != nil || !current {
		return false, err
	}
	published := false
	// 条件更新持有来源行锁并核对当前任务，同一事务中替换分段并发布批次。
	err = serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewUpdate().Model(publication.Model).
			Set("status = ?", domain.KnowledgeIndexSucceeded).Set("segment_batch_id = ?", batch.BatchID).Set("segment_count = ?", len(segments)).Set("failure_code = ''").
			Where("id = ? AND processing_id = ? AND status NOT IN (?, ?, ?, ?)", batch.SourceID, batch.BatchID,
				domain.KnowledgeIndexInitial, domain.KnowledgeIndexSucceeded, domain.KnowledgeIndexFailed, domain.KnowledgeIndexCancelled).
			Exec(ctx)
		if err != nil {
			return err
		}
		if count, err := result.RowsAffected(); err != nil || count == 0 {
			return err
		}
		if err := deleteSourceSegments(ctx, tx, batch.SourceID); err != nil {
			return err
		}
		if err := insertSegments(ctx, tx, batch, segments, vectors); err != nil {
			return err
		}
		published = true
		return nil
	})
	return published, err
}

// updateIndexStage 更新来源当前任务的执行阶段，任务已被替代或已进入终态时返回 false。
func updateIndexStage(ctx context.Context, db bun.IDB, model any, sourceID, processingID string, stage domain.KnowledgeIndexStatus) (bool, error) {
	result, err := db.NewUpdate().Model(model).
		Set("status = ?", stage).Set("failure_code = ''").
		Where("id = ? AND processing_id = ? AND status NOT IN (?, ?, ?, ?)", sourceID, processingID,
			domain.KnowledgeIndexInitial, domain.KnowledgeIndexSucceeded, domain.KnowledgeIndexFailed, domain.KnowledgeIndexCancelled).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// finalizeIndexFailure 保存来源当前任务的失败状态和原因码，返回状态是否发生变更。
func finalizeIndexFailure(ctx context.Context, db *bun.DB, model any, sourceID, processingID, code string) (bool, error) {
	changed := false
	err := serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewUpdate().Model(model).Set("status = ?", domain.KnowledgeIndexFailed).Set("failure_code = ?", code).
			Where("id = ? AND processing_id = ? AND status NOT IN (?, ?, ?, ?)", sourceID, processingID,
				domain.KnowledgeIndexInitial, domain.KnowledgeIndexSucceeded, domain.KnowledgeIndexFailed, domain.KnowledgeIndexCancelled).
			Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		changed = count > 0
		return nil
	})
	return changed, err
}

// indexFailureCode 从任务错误中提取失败原因码和阶段，未标记的错误按服务失败处理。
func indexFailureCode(runErr error) (string, domain.KnowledgeIndexStatus) {
	var failure *ProcessError
	if errors.As(runErr, &failure) {
		return failure.Code, failure.Stage
	}
	return "service_failed", ""
}
