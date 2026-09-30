//go:build server

package installation

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// StatusQuery 查询部署是否已完成首次安装。
type StatusQuery struct {
	db *bun.DB
}

// NewStatusQuery 创建安装状态查询。
func NewStatusQuery(db *bun.DB) *StatusQuery {
	return &StatusQuery{db: db}
}

// Execute 返回部署是否已有账号。
func (q *StatusQuery) Execute(ctx context.Context) (bool, error) {
	installed, err := q.db.NewSelect().Model((*servermodels.Account)(nil)).Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check installation: %w", err)
	}
	return installed, nil
}
