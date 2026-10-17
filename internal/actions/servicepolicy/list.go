//go:build server

package servicepolicy

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListQuery 读取当前企业的咨询分类目录。
type ListQuery struct{ db *bun.DB }

// NewListQuery 创建咨询分类目录查询。
func NewListQuery(db *bun.DB) *ListQuery { return &ListQuery{db: db} }

// Execute 按名称顺序返回全部未归档的咨询分类。
func (q *ListQuery) Execute(ctx context.Context, identity *servermodels.Identity, audience domain.ServiceAudience) ([]Record, error) {
	if !ValidAudience(audience) {
		return nil, ErrAudienceInvalid
	}
	records := make([]Record, 0)
	if err := selectRecords(q.db, identity.Workspace.ID, audience).OrderExpr("lower(sc.name) ASC, sc.id ASC").Scan(ctx, &records); err != nil {
		return nil, fmt.Errorf("list service categories: %w", err)
	}
	return records, nil
}
