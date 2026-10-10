//go:build server

package businesssystem

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ListBusinessSystemsQuery 查询当前工作区的业务系统。
type ListBusinessSystemsQuery struct {
	db *bun.DB
}

// NewListBusinessSystemsQuery 创建业务系统列表查询。
func NewListBusinessSystemsQuery(db *bun.DB) *ListBusinessSystemsQuery {
	return &ListBusinessSystemsQuery{db: db}
}

// Execute 按添加时间返回当前工作区的业务系统列表。
func (q *ListBusinessSystemsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Record, error) {
	records := make([]servermodels.BusinessSystem, 0)
	if err := q.db.NewSelect().
		Model(&records).
		Where("bs.workspace_id = ?", identity.Workspace.ID).
		Order("bs.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list business systems: %w", err)
	}
	return arr.OrEmpty(arr.Map(records, recordFromModel)), nil
}
