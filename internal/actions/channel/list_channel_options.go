//go:build server

package channel

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Option 定义渠道选择项。
type Option struct {
	ID   string             `bun:"id"`
	Type domain.ChannelType `bun:"type"`
	Name string             `bun:"name"`
}

// ListChannelOptionsQuery 读取当前企业可供选择的渠道。
type ListChannelOptionsQuery struct {
	db *bun.DB
}

// NewListChannelOptionsQuery 创建渠道选择项查询。
func NewListChannelOptionsQuery(db *bun.DB) *ListChannelOptionsQuery {
	return &ListChannelOptionsQuery{db: db}
}

// Execute 返回当前企业已启用且支持的渠道选择项。
func (q *ListChannelOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Option, error) {
	channels := make([]Option, 0)
	if err := q.db.NewSelect().
		TableExpr("channels AS c").
		ColumnExpr("c.id::text AS id").
		ColumnExpr("c.type").
		ColumnExpr("c.name").
		Where("c.organization_id = ?", identity.Organization.ID).
		Where("c.enabled = TRUE").
		Where("c.type IN (?)", bun.In(domain.MessageChannelTypes())).
		OrderExpr("c.type ASC, lower(c.name) ASC, c.id ASC").
		Scan(ctx, &channels); err != nil {
		return nil, fmt.Errorf("list channel options: %w", err)
	}
	return channels, nil
}
