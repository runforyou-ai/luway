//go:build server

package businesssystem

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetBusinessSystemQuery 查询当前工作区中的业务系统。
type GetBusinessSystemQuery struct {
	db *bun.DB
}

// NewGetBusinessSystemQuery 创建业务系统详情查询。
func NewGetBusinessSystemQuery(db *bun.DB) *GetBusinessSystemQuery {
	return &GetBusinessSystemQuery{db: db}
}

// Execute 返回当前工作区中的业务系统详情。
func (q *GetBusinessSystemQuery) Execute(ctx context.Context, identity *servermodels.Identity, businessSystemID string) (*Record, error) {
	system, err := loadBusinessSystem(ctx, q.db, identity.Workspace.ID, businessSystemID, false)
	if err != nil {
		return nil, fmt.Errorf("get business system: %w", err)
	}
	output := recordFromModel(*system)
	return &output, nil
}
