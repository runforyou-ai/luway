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

// ErrUsageInvalid 表示模型用途不是已支持取值。
var ErrUsageInvalid = errors.New("AI model usage invalid")

// ListOptionsQuery 读取工作区中满足用途要求的模型选项。
type ListOptionsQuery struct{ db *bun.DB }

// NewListOptionsQuery 创建模型选项查询。
func NewListOptionsQuery(db *bun.DB) *ListOptionsQuery {
	return &ListOptionsQuery{db: db}
}

// Execute 按供应商名称、模型名称排序返回满足用途要求的模型。
func (q *ListOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity, usage domain.AIModelUsage) ([]Option, error) {
	if _, ok := usage.Requirement(); !ok {
		return nil, ErrUsageInvalid
	}
	options := make([]Option, 0)
	if err := modelQuery(q.db, identity.Organization.ID, usage).
		OrderExpr("lower(aip.name) ASC, lower(aim.name) ASC, aim.identifier ASC").
		Scan(ctx, &options); err != nil {
		return nil, fmt.Errorf("list AI model options: %w", err)
	}
	return options, nil
}
