//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// StatusUpdater 修改一种渠道类型的启用状态，并同步维护该类型在平台侧的连接。
type StatusUpdater interface {
	Execute(ctx context.Context, identity *servermodels.Identity, channelID string, enabled bool) (*MessageChannelRecord, error)
}

// UpdateMessageChannelStatusAction 修改消息渠道启用状态，登记了状态操作的渠道类型交由该操作处理。
type UpdateMessageChannelStatusAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
	updaters map[domain.ChannelType]StatusUpdater
}

// NewUpdateMessageChannelStatusAction 创建消息渠道状态操作，updaters 按渠道类型登记需要维护平台侧连接的状态操作。
func NewUpdateMessageChannelStatusAction(db *bun.DB, enqueuer servertask.TxEnqueuer, updaters map[domain.ChannelType]StatusUpdater) *UpdateMessageChannelStatusAction {
	return &UpdateMessageChannelStatusAction{db: db, enqueuer: enqueuer, updaters: updaters}
}

// Execute 修改当前企业中已支持消息渠道的启用状态，登记了状态操作的渠道类型由该操作处理；同一公众号已有另一种接入方式的渠道启用时返回 ErrWechatAccountEnabledElsewhere。
func (a *UpdateMessageChannelStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, enabled bool) (*MessageChannelRecord, error) {
	var channelType domain.ChannelType
	err := a.db.NewSelect().Model((*servermodels.Channel)(nil)).Column("c.type").
		Where("c.id = ? AND c.workspace_id = ? AND c.type IN (?)", channelID, identity.Workspace.ID, bun.List(domain.MessageChannelTypes())).
		Scan(ctx, &channelType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load message channel type: %w", err)
	}
	if updater, ok := a.updaters[channelType]; ok {
		return updater.Execute(ctx, identity, channelID, enabled)
	}
	var channel *servermodels.Channel
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 锁定渠道行并读取修改前的启用状态。
		var previous bool
		err := tx.NewSelect().Model((*servermodels.Channel)(nil)).Column("c.enabled").
			Where("c.id = ? AND c.workspace_id = ? AND c.type = ?", channelID, identity.Workspace.ID, channelType).
			For("UPDATE").
			Scan(ctx, &previous)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock message channel: %w", err)
		}
		channel = &servermodels.Channel{}
		_, err = tx.NewUpdate().
			Model(channel).
			Set("enabled = ?", enabled).
			Where("c.id = ?", channelID).
			Where("c.workspace_id = ?", identity.Workspace.ID).
			Where("c.type = ?", channelType).
			Returning("*").
			Exec(ctx)
		if pgerr.UniqueViolationOn(err, WechatEnabledAccountIndex) {
			return ErrWechatAccountEnabledElsewhere
		}
		if err != nil {
			return fmt.Errorf("update message channel status: %w", err)
		}
		// 启停切换改变待发送投递的暂停状态，批量推进相关客户会话版本。
		if previous != enabled {
			if _, err := chatstate.TouchConversations(ctx, tx, identity.Workspace.ID, tx.NewSelect().TableExpr("channel_message_deliveries").Column("conversation_id").
				Where("workspace_id = ? AND channel_id = ? AND status IN (?, ?)", identity.Workspace.ID, channel.ID, domain.ChannelDeliveryPending, domain.ChannelDeliveryRetryWait), domain.ConversationChangeTimeline); err != nil {
				return err
			}
		}
		// 启用渠道时恢复推进该渠道暂停的投递。
		if enabled {
			if err := channeldelivery.WakeChannel(ctx, tx, a.enqueuer, identity.Workspace.ID, channel.ID); err != nil {
				return err
			}
		}
		// 停用网站渠道时结束该渠道全部访客事件流，受众 ID 取更新结果中的规范渠道 ID。
		if !enabled && domain.ChannelType(channel.Type) == domain.ChannelTypeWebsite {
			realtime.Notify(ctx, realtime.WebsiteChannelDisabled(identity.Workspace.ID, channel.ID))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewMessageChannelRecord(channel), nil
}
