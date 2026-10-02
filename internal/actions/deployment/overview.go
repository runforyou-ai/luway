//go:build server

package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Overview 定义部署实例的标识、安装时间、规模和当前能力。
type Overview struct {
	InstanceID     string
	InstalledAt    time.Time
	AccountCount   int
	WorkspaceCount int
	Capabilities   domain.InstanceCapabilities
}

// OverviewQuery 读取部署概况。
type OverviewQuery struct {
	db *bun.DB
}

// NewOverviewQuery 创建部署概况查询。
func NewOverviewQuery(db *bun.DB) *OverviewQuery {
	return &OverviewQuery{db: db}
}

// Execute 返回实例标识、安装时间、账号数、工作区数和实例能力。
func (q *OverviewQuery) Execute(ctx context.Context) (Overview, error) {
	deployment, err := Load(ctx, q.db)
	if err != nil {
		return Overview{}, err
	}
	accounts, err := q.db.NewSelect().Model((*servermodels.Account)(nil)).Count(ctx)
	if err != nil {
		return Overview{}, fmt.Errorf("count accounts: %w", err)
	}
	workspaces, err := q.db.NewSelect().Model((*servermodels.Organization)(nil)).Count(ctx)
	if err != nil {
		return Overview{}, fmt.Errorf("count workspaces: %w", err)
	}
	capabilities, err := Capabilities(ctx, q.db)
	if err != nil {
		return Overview{}, err
	}
	return Overview{
		InstanceID: deployment.InstanceID, InstalledAt: deployment.CreatedAt,
		AccountCount: accounts, WorkspaceCount: workspaces, Capabilities: capabilities,
	}, nil
}
