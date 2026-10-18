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

// Execute 返回满足用途要求的模型：平台部署只返回 Auto；工作区部署只返回自配模型。
func (q *ListOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity, usage domain.AIModelUsage) ([]Option, error) {
	if _, ok := usage.Requirement(); !ok {
		return nil, ErrUsageInvalid
	}
	if SourceOf(ctx) == domain.AIModelSourcePlatform {
		if auto, err := autoOption(ctx, q.db, identity.Workspace.ID, usage); err != nil {
			return nil, err
		} else if auto != nil {
			return []Option{*auto}, nil
		}
		return []Option{}, nil
	}
	workspace := make([]Option, 0)
	if err := optionQuery(q.db, identity.Workspace.ID, usage).
		Where("EXISTS (SELECT 1 FROM ai_model_routes AS amr WHERE amr.model_id = aim.id AND amr.enabled)").
		OrderExpr("lower(COALESCE(aip.name, '')) ASC, lower(aim.name) ASC, aim.id ASC").
		Scan(ctx, &workspace); err != nil {
		return nil, fmt.Errorf("list AI model options: %w", err)
	}
	return workspace, nil
}
