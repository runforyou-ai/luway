//go:build server

package aimodel

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrUsageInvalid 表示模型用途不在支持的取值内。
var ErrUsageInvalid = errors.New("AI model usage invalid")

// ListOptionsQuery 读取对工作区可用且满足用途要求的模型选项。
type ListOptionsQuery struct{ db *bun.DB }

// NewListOptionsQuery 创建模型选项查询。
func NewListOptionsQuery(db *bun.DB) *ListOptionsQuery {
	return &ListOptionsQuery{db: db}
}

// Execute 返回满足用途要求且有可用来源的模型：平台模型在前并按名称排序，工作区模型按供应商名称、模型名称排序。
func (q *ListOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity, usage domain.AIModelUsage) ([]Option, error) {
	if _, ok := usage.Requirement(); !ok {
		return nil, ErrUsageInvalid
	}
	options := make([]Option, 0)
	if err := optionQuery(q.db, identity.Workspace.ID, usage).
		Where("EXISTS (SELECT 1 FROM ai_model_routes AS amr WHERE amr.model_id = aim.id AND amr.enabled)").
		OrderExpr("aim.workspace_id IS NULL DESC, lower(COALESCE(aip.name, '')) ASC, lower(aim.name) ASC, aim.id ASC").
		Scan(ctx, &options); err != nil {
		return nil, fmt.Errorf("list AI model options: %w", err)
	}
	return options, nil
}
