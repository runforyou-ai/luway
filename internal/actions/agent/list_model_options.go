//go:build server

package agent

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListModelOptionsQuery 读取 AI 员工可用的对话模型。
type ListModelOptionsQuery struct{ db *bun.DB }

// NewListModelOptionsQuery 创建 AI 员工对话模型选项查询。
func NewListModelOptionsQuery(db *bun.DB) *ListModelOptionsQuery {
	return &ListModelOptionsQuery{db: db}
}

// Execute 返回当前企业可用于 AI 员工的文本对话模型。
func (q *ListModelOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]ModelOption, error) {
	options := make([]ModelOption, 0)
	if err := q.db.NewSelect().TableExpr("ai_provider_models AS aipm").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aipm.identifier AS model_identifier, aipm.name AS model_name").
		Join("JOIN ai_providers AS aip ON aip.id = aipm.provider_id AND aip.organization_id = aipm.organization_id").
		Where("aipm.organization_id = ?", identity.Organization.ID).
		Where("aipm.model_type = ?", domain.AIModelTypeChat).
		Where("aipm.input_modalities @> ?::jsonb", `["text"]`).
		OrderExpr("lower(aip.name) ASC, lower(aipm.name) ASC, aipm.identifier ASC").
		Scan(ctx, &options); err != nil {
		return nil, fmt.Errorf("list agent model options: %w", err)
	}
	return options, nil
}
