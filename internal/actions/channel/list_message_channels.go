//go:build server

package channel

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// ListMessageChannelsQuery 读取当前企业的消息渠道。
type ListMessageChannelsQuery struct {
	db *bun.DB
}

// NewListMessageChannelsQuery 创建消息渠道列表查询。
func NewListMessageChannelsQuery(db *bun.DB) *ListMessageChannelsQuery {
	return &ListMessageChannelsQuery{db: db}
}

// ExecuteByType 返回当前企业支持管理的全部消息渠道，按渠道类型的既定顺序排列，同类型内按名称排序。
func (q *ListMessageChannelsQuery) ExecuteByType(ctx context.Context, identity *servermodels.Identity) ([]MessageChannelRecord, error) {
	records, err := q.Execute(ctx, identity)
	if err != nil {
		return nil, err
	}
	order := domain.MessageChannelTypes()
	slices.SortStableFunc(records, func(left, right MessageChannelRecord) int {
		if position := slices.Index(order, domain.ChannelType(left.Type)) - slices.Index(order, domain.ChannelType(right.Type)); position != 0 {
			return position
		}
		return strings.Compare(left.Name, right.Name)
	})
	return records, nil
}

// Execute 按创建时间返回当前企业支持管理的全部消息渠道。
func (q *ListMessageChannelsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]MessageChannelRecord, error) {
	channels := make([]servermodels.Channel, 0)
	if err := q.db.NewSelect().
		Model(&channels).
		Where("c.workspace_id = ?", identity.Workspace.ID).
		Where("c.type IN (?)", bun.List(domain.MessageChannelTypes())).
		OrderExpr("c.created_at ASC, c.id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list message channels: %w", err)
	}
	return arr.Map(channels, func(channel servermodels.Channel) MessageChannelRecord { return *NewMessageChannelRecord(&channel) }), nil
}
