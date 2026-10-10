//go:build server

package knowledgebase

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// lockModels 校验并锁定知识库所选的向量模型和重排模型直至事务结束，须在锁定知识库之前调用。
func lockModels(ctx context.Context, tx bun.Tx, workspaceID string, input Input) error {
	fields := make(map[string]common.FieldCode)
	for _, selected := range []struct {
		modelID, field string
		usage          domain.AIModelUsage
		code           common.FieldCode
	}{
		{input.EmbeddingModelID, "embeddingModelId", domain.AIModelUsageEmbedding, ValidationEmbeddingModelInvalid},
		{input.RerankModelID, "rerankModelId", domain.AIModelUsageRerank, ValidationRerankModelInvalid},
	} {
		if _, err := aimodel.Lock(ctx, tx, workspaceID, selected.modelID, selected.usage); errors.Is(err, aimodel.ErrUnavailable) {
			fields[selected.field] = selected.code
		} else if err != nil {
			return err
		}
	}
	if len(fields) > 0 {
		return &common.FieldError{Fields: fields}
	}
	return nil
}
