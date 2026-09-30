//go:build server

package servicecategory

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListQuery 读取当前企业的咨询分类目录。
type ListQuery struct{ db *bun.DB }

// NewListQuery 创建咨询分类目录查询。
func NewListQuery(db *bun.DB) *ListQuery { return &ListQuery{db: db} }

// Execute 按名称顺序返回全部未归档的咨询分类。
func (q *ListQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Record, error) {
	records := make([]Record, 0)
	if err := selectRecords(q.db, identity.Organization.ID).OrderExpr("lower(sc.name) ASC, sc.id ASC").Scan(ctx, &records); err != nil {
		return nil, fmt.Errorf("list service categories: %w", err)
	}
	return records, nil
}
