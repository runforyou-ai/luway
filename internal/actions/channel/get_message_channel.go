//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetMessageChannelQuery 读取当前企业的消息渠道基础信息。
type GetMessageChannelQuery struct {
	db *bun.DB
}

// NewGetMessageChannelQuery 创建消息渠道详情查询。
func NewGetMessageChannelQuery(db *bun.DB) *GetMessageChannelQuery {
	return &GetMessageChannelQuery{db: db}
}

// Execute 返回当前企业中受支持的消息渠道基础信息。
func (q *GetMessageChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*MessageChannelRecord, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	channel := &servermodels.Channel{}
	err := q.db.NewSelect().
		Model(channel).
		Where("c.id = ?", channelID).
		Where("c.organization_id = ?", identity.Organization.ID).
		Where("c.type IN (?)", bun.In(domain.MessageChannelTypes())).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get message channel: %w", err)
	}
	return messageChannelRecord(channel), nil
}
