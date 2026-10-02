//go:build server

package aimodel

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// Reference 定义一处业务配置对模型的引用及其用途。
type Reference struct {
	ModelID string              `bun:"model_id"`
	Usage   domain.AIModelUsage `bun:"usage"`
}

// References 返回工作区业务配置对指定模型的全部引用：AI 员工生效版本、知识库向量与重排、客服判断、小结与翻译设置。
func References(ctx context.Context, db bun.IDB, organizationID string, modelIDs []string) ([]Reference, error) {
	references := make([]Reference, 0)
	if len(modelIDs) == 0 {
		return references, nil
	}
	ids := bun.In(modelIDs)
	if err := db.NewRaw(`
SELECT DISTINCT reference.model_id::text AS model_id, reference.usage FROM (
	SELECT ar.model_id, ? AS usage FROM agents AS a
	JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.organization_id = a.organization_id
	WHERE a.organization_id = ?
	UNION ALL SELECT embedding_model_id, ? FROM knowledge_bases WHERE organization_id = ?
	UNION ALL SELECT rerank_model_id, ? FROM knowledge_bases WHERE organization_id = ?
	UNION ALL SELECT decision_model_id, ? FROM customer_service_settings WHERE organization_id = ?
	UNION ALL SELECT summary_model_id, ? FROM customer_service_settings WHERE organization_id = ?
	UNION ALL SELECT translation_model_id, ? FROM customer_service_settings WHERE organization_id = ?
) AS reference
WHERE reference.model_id IN (?)
ORDER BY model_id, usage`,
		domain.AIModelUsageAgent, organizationID,
		domain.AIModelUsageEmbedding, organizationID,
		domain.AIModelUsageRerank, organizationID,
		domain.AIModelUsageDecision, organizationID,
		domain.AIModelUsageSummary, organizationID,
		domain.AIModelUsageTranslation, organizationID,
		ids,
	).Scan(ctx, &references); err != nil {
		return nil, fmt.Errorf("list AI model references: %w", err)
	}
	return references, nil
}

// Satisfies 判断模型类型与输入模态是否满足用途要求。
func Satisfies(usage domain.AIModelUsage, modelType domain.AIModelType, inputModalities []domain.AIModelInputModality) bool {
	requirement, ok := usage.Requirement()
	if !ok || modelType != requirement.Type {
		return false
	}
	return !requirement.RequiresText || slices.Contains(inputModalities, domain.AIModelInputModalityText)
}
