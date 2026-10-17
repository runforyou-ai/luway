//go:build server

package channel

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/servicepolicy"
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

// ExecuteForMembers 返回工作区成员可见的全部消息渠道，按渠道类型的既定顺序排列，同类型内按名称排序。
func (q *ListMessageChannelsQuery) ExecuteForMembers(ctx context.Context, identity *servermodels.Identity) ([]MessageChannelRecord, error) {
	records, err := q.list(ctx, identity, domain.MessageChannelTypes())
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
	types := ManageableTypes(identity)
	if len(types) == 0 {
		return nil, servicepolicy.ErrForbidden
	}
	return q.list(ctx, identity, types)
}

// list 按指定渠道类型读取工作区渠道。
func (q *ListMessageChannelsQuery) list(ctx context.Context, identity *servermodels.Identity, types []domain.ChannelType) ([]MessageChannelRecord, error) {
	channels := make([]servermodels.Channel, 0)
	if err := q.db.NewSelect().
		Model(&channels).
		Where("c.workspace_id = ?", identity.Workspace.ID).
		Where("c.type IN (?)", bun.List(types)).
		OrderExpr("c.created_at ASC, c.id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list message channels: %w", err)
	}
	return arr.Map(channels, func(channel servermodels.Channel) MessageChannelRecord { return *NewMessageChannelRecord(&channel) }), nil
}

// ManageableTypes 返回成员拥有管理权限的渠道类型。
func ManageableTypes(identity *servermodels.Identity) []domain.ChannelType {
	return arr.Filter(domain.MessageChannelTypes(), func(kind domain.ChannelType) bool {
		return servicepolicy.RequireManage(identity, domain.ChannelCapabilitiesOf(kind).Audience) == nil
	})
}
