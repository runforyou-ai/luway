//go:build server

package knowledgebase

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/embedding"
	"github.com/runforyou-ai/luway/pkg/textsplit"
	"github.com/uptrace/bun"
)

// resolveEmbeddingCredential 读取同企业的向量模型供应商并解析 OpenAI 兼容入口。
func resolveEmbeddingCredential(ctx context.Context, db bun.IDB, organizationID, providerID string) (embedding.Credential, error) {
	provider := &servermodels.AIProvider{}
	err := db.NewSelect().Model(provider).Where("id = ? AND organization_id = ?", providerID, organizationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return embedding.Credential{}, &embedding.Error{Code: "embedding_model_unavailable"}
	}
	if err != nil {
		return embedding.Credential{}, err
	}
	return embeddingCredential(provider)
}

// embeddingCredential 按供应商品牌解析 OpenAI 兼容入口并组装向量接口凭据。
func embeddingCredential(provider *servermodels.AIProvider) (embedding.Credential, error) {
	baseURL, err := modelprovider.CompatibleBaseURL(provider.Brand, provider.APIURL)
	if err != nil {
		return embedding.Credential{}, &embedding.Error{Code: "embedding_model_unavailable"}
	}
	return embedding.Credential{BaseURL: baseURL, APIKey: provider.APIKey}, nil
}

// indexPublication 固定一次分段发布的来源模型、分段批次和向量模型。
type indexPublication struct {
	Model                    any
	Batch                    segmentBatch
	EmbeddingProviderID      string
	EmbeddingModelIdentifier string
}

// embedAndPublish 向量化来源分段，并在当前任务仍有效时于同一事务中替换分段、发布批次；返回是否已发布。
func embedAndPublish(ctx context.Context, db *bun.DB, embedder segmentEmbedder, publication indexPublication, segments []textsplit.Segment) (bool, error) {
	batch := publication.Batch
	if current, err := updateIndexStage(ctx, db, publication.Model, batch.SourceID, batch.BatchID, domain.KnowledgeIndexEmbedding); err != nil || !current {
		return false, err
	}
	credential, err := resolveEmbeddingCredential(ctx, db, batch.OrganizationID, publication.EmbeddingProviderID)
	var failure *embedding.Error
	if errors.As(err, &failure) {
		return false, &ProcessError{Code: failure.Code, Stage: domain.KnowledgeIndexEmbedding}
	}
	if err != nil {
		return false, err
	}
	texts := make([]string, 0, len(segments))
	for _, segment := range segments {
		texts = append(texts, textsplit.IndexText(segment.Context, segment.Content))
	}
	vectors, err := embedder.Embed(ctx, credential, publication.EmbeddingModelIdentifier, batch.EmbeddingDimension, texts)
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
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewUpdate().Model(publication.Model).
			Set("status = ?", domain.KnowledgeIndexSucceeded).Set("segment_batch_id = ?", batch.BatchID).Set("segment_count = ?", len(segments)).Set("failure_code = ''").Set("updated_at = now()").
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
		if err := servertask.LockExecution(ctx, tx); err != nil {
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
		Set("status = ?", stage).Set("failure_code = ''").Set("updated_at = now()").
		Where("id = ? AND processing_id = ? AND status NOT IN (?, ?, ?, ?)", sourceID, processingID,
			domain.KnowledgeIndexInitial, domain.KnowledgeIndexSucceeded, domain.KnowledgeIndexFailed, domain.KnowledgeIndexCancelled).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// finalizeIndexFailure 保存来源当前任务的失败状态和原因码，并释放任务执行租约；返回状态是否发生变更。
func finalizeIndexFailure(ctx context.Context, db *bun.DB, model any, sourceID, processingID, code string) (bool, error) {
	changed := false
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewUpdate().Model(model).Set("status = ?", domain.KnowledgeIndexFailed).Set("failure_code = ?", code).Set("updated_at = now()").
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
		return servertask.LockExecution(ctx, tx)
	})
	if errors.Is(err, servertask.ErrExecutionLost) {
		return false, nil
	}
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
