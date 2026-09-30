//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateMessageChannelStatusAction 修改消息渠道启用状态，Telegram 渠道交由 Telegram 状态操作同步 Webhook。
type UpdateMessageChannelStatusAction struct {
	db       *bun.DB
	telegram *UpdateTelegramChannelStatusAction
}

// NewUpdateMessageChannelStatusAction 创建消息渠道状态操作。
func NewUpdateMessageChannelStatusAction(db *bun.DB, telegram *UpdateTelegramChannelStatusAction) *UpdateMessageChannelStatusAction {
	return &UpdateMessageChannelStatusAction{db: db, telegram: telegram}
}

// Execute 修改当前企业中已支持消息渠道的启用状态，Telegram 渠道由 Telegram 状态操作处理。
func (a *UpdateMessageChannelStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, enabled bool) (*MessageChannelRecord, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	var channelType domain.ChannelType
	err := a.db.NewSelect().Model((*servermodels.Channel)(nil)).Column("c.type").
		Where("c.id = ? AND c.organization_id = ? AND c.type IN (?)", channelID, identity.Organization.ID, bun.In(domain.MessageChannelTypes())).
		Scan(ctx, &channelType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load message channel type: %w", err)
	}
	if channelType == domain.ChannelTypeTelegram {
		return a.telegram.Execute(ctx, identity, channelID, enabled)
	}
	var channel *servermodels.Channel
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		channel = &servermodels.Channel{}
		result, err := tx.NewUpdate().
			Model(channel).
			Set("enabled = ?", enabled).
			Set("updated_at = now()").
			Where("c.id = ?", channelID).
			Where("c.organization_id = ?", identity.Organization.ID).
			Where("c.type IN (?) AND c.type <> ?", bun.In(domain.MessageChannelTypes()), domain.ChannelTypeTelegram).
			Returning("*").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("update message channel status: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read updated message channel count: %w", err)
		}
		if rows == 0 {
			return ErrNotFound
		}
		// 停用网站渠道时结束该渠道全部访客事件流，受众 ID 取更新结果中的规范渠道 ID。
		if !enabled && domain.ChannelType(channel.Type) == domain.ChannelTypeWebsite {
			realtime.Notify(ctx, realtime.WebsiteChannelDisabled(identity.Organization.ID, channel.ID))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return messageChannelRecord(channel), nil
}
