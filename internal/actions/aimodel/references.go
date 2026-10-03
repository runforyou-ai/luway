//go:build server

package aimodel

import (
	"context"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// Reference 定义一处业务配置对模型的引用、引用所在的工作区及其用途。
type Reference struct {
	OrganizationID string              `bun:"organization_id"`
	ModelID        string              `bun:"model_id"`
	Usage          domain.AIModelUsage `bun:"usage"`
}

// References 返回工作区业务配置对指定模型的全部引用：AI 员工生效版本、知识库向量与重排、客服判断、小结与翻译设置。
func References(ctx context.Context, db bun.IDB, organizationID string, modelIDs []string) ([]Reference, error) {
	return references(ctx, db, &organizationID, modelIDs)
}

// PlatformReferences 返回平台内全部工作区业务配置对指定模型的引用。
func PlatformReferences(ctx context.Context, db bun.IDB, modelIDs []string) ([]Reference, error) {
	return references(ctx, db, nil, modelIDs)
}

// references 读取业务配置对指定模型的引用，organizationID 为空时读取全部工作区。
func references(ctx context.Context, db bun.IDB, organizationID *string, modelIDs []string) ([]Reference, error) {
	result := make([]Reference, 0)
	if len(modelIDs) == 0 {
		return result, nil
	}
	if err := db.NewRaw(`
SELECT DISTINCT reference.organization_id::text AS organization_id, reference.model_id::text AS model_id, reference.usage FROM (
	SELECT a.organization_id, ar.model_id, ? AS usage FROM agents AS a
	JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.organization_id = a.organization_id
	UNION ALL SELECT organization_id, embedding_model_id, ? FROM knowledge_bases
	UNION ALL SELECT organization_id, rerank_model_id, ? FROM knowledge_bases
	UNION ALL SELECT organization_id, decision_model_id, ? FROM customer_service_settings
	UNION ALL SELECT organization_id, summary_model_id, ? FROM customer_service_settings
	UNION ALL SELECT organization_id, translation_model_id, ? FROM customer_service_settings
) AS reference
WHERE reference.model_id IN (?) AND (?::uuid IS NULL OR reference.organization_id = ?::uuid)
ORDER BY organization_id, model_id, usage`,
		domain.AIModelUsageAgent, domain.AIModelUsageEmbedding, domain.AIModelUsageRerank,
		domain.AIModelUsageDecision, domain.AIModelUsageSummary, domain.AIModelUsageTranslation,
		bun.In(modelIDs), organizationID, organizationID,
	).Scan(ctx, &result); err != nil {
		return nil, fmt.Errorf("list AI model references: %w", err)
	}
	return result, nil
}

// Satisfies 判断模型类型与输入模态是否满足用途要求。
func Satisfies(usage domain.AIModelUsage, modelType domain.AIModelType, inputModalities []domain.AIModelInputModality) bool {
	requirement, ok := usage.Requirement()
	if !ok || modelType != requirement.Type {
		return false
	}
	return !requirement.RequiresText || slices.Contains(inputModalities, domain.AIModelInputModalityText)
}
