//go:build server

package channel

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/channelstate"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// UpdateTelegramChannelStatusAction 修改 Telegram 渠道状态并维护 Webhook。
type UpdateTelegramChannelStatusAction struct {
	db     *bun.DB
	runner *connectiontest.Runner
	api    telegram.BotAPI
}

// NewUpdateTelegramChannelStatusAction 创建 Telegram 渠道状态操作。
func NewUpdateTelegramChannelStatusAction(db *bun.DB, runner *connectiontest.Runner, api telegram.BotAPI) *UpdateTelegramChannelStatusAction {
	return &UpdateTelegramChannelStatusAction{db: db, runner: runner, api: api}
}

// Execute 修改渠道状态，并在事务提交后注册或删除 Webhook。
func (a *UpdateTelegramChannelStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, enabled bool) (*MessageChannelRecord, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	var output *MessageChannelRecord
	err := channelstate.WithTelegramLock(ctx, a.db, channelID, func(conn bun.Conn) error {
		current, err := loadTelegramChannelDetail(ctx, conn, identity.Organization.ID, channelID, false)
		if err != nil {
			return err
		}
		var botIDs []int64
		if current.Connection.BotID != nil {
			botIDs = append(botIDs, *current.Connection.BotID)
		}

		return withTelegramBotLocks(ctx, conn, identity.Organization.ID, botIDs, func() error {
			var token string
			var botUsedByOtherChannel bool
			var webhookURL string
			var secret string
			err := realtime.RunInTx(ctx, conn, func(ctx context.Context, tx bun.Tx) error {
				if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
					return err
				}
				detail, err := loadTelegramChannelDetail(ctx, tx, identity.Organization.ID, channelID, true)
				if err != nil {
					return err
				}
				token = detail.Connection.BotToken
				if !enabled && detail.Connection.BotID != nil {
					botUsedByOtherChannel, err = telegramBotUsedByOtherChannel(ctx, tx, identity.Organization.ID, *detail.Connection.BotID, channelID)
					if err != nil {
						return err
					}
				}
				// 尚未保存连接配置时仍可启用，Webhook 留待保存连接后注册。
				if enabled && token != "" && detail.Connection.WebhookBaseURL != "" {
					webhookURL, err = telegramWebhookURL(detail.Connection.WebhookBaseURL, channelID)
					if err != nil {
						slog.Warn("Telegram Webhook 地址无效，跳过注册", "channel_id", channelID, "error", err)
						webhookURL = ""
					} else {
						secret, err = newTelegramWebhookSecret()
						if err != nil {
							return fmt.Errorf("generate Telegram webhook secret: %w", err)
						}
					}
				}

				channel := &servermodels.Channel{ID: channelID}
				result, err := tx.NewUpdate().
					Model(channel).
					Set("enabled = ?", enabled).
					Set("updated_at = now()").
					Where("id = ?", channelID).
					Where("organization_id = ?", identity.Organization.ID).
					Where("type = ?", domain.ChannelTypeTelegram).
					Exec(ctx)
				if err != nil {
					return err
				}
				rows, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if rows == 0 {
					return ErrNotFound
				}

				// 启停切换改变待发送投递的暂停状态，批量推进相关客户会话版本。
				if detail.Enabled != enabled {
					if _, err := chatstate.TouchConversations(ctx, tx, identity.Organization.ID, tx.NewSelect().TableExpr("customer_message_deliveries").Column("conversation_id").
						Where("organization_id = ? AND channel_id = ? AND status IN (?, ?)", identity.Organization.ID, channelID, domain.CustomerDeliveryPending, domain.CustomerDeliveryRetryWait), domain.ConversationChangeTimeline); err != nil {
						return err
					}
				}

				setting := &servermodels.TelegramChannelSetting{ChannelID: channelID}
				query := tx.NewUpdate().
					Model(setting).
					Set("updated_at = now()").
					Set("webhook_connected_at = NULL").
					Where("channel_id = ?", channelID).
					Where("organization_id = ?", identity.Organization.ID)
				if secret != "" {
					query = query.
						Set("webhook_secret = ?", secret).
						Set("webhook_status = ?", domain.TelegramWebhookStatusWaiting)
				} else {
					query = query.
						Set("webhook_secret = NULL").
						Set("webhook_status = NULL")
				}
				result, err = query.Exec(ctx)
				if err != nil {
					return err
				}
				rows, err = result.RowsAffected()
				if err != nil {
					return err
				}
				if rows == 0 {
					return ErrNotFound
				}
				return nil
			})
			if err != nil {
				return err
			}

			if webhookURL != "" {
				if err := runTelegramSetWebhook(ctx, a.runner, a.api, token, webhookURL, secret); err != nil {
					logTelegramRemoteFailure("注册 Telegram Webhook 失败", channelID, err)
				} else {
					slog.Info("Telegram Webhook 注册成功", "channel_id", channelID)
				}
			} else if !enabled && token != "" && !botUsedByOtherChannel {
				if err := runTelegramDeleteWebhook(ctx, a.runner, a.api, token); err != nil {
					logTelegramRemoteFailure("删除 Telegram Webhook 失败", channelID, err)
				}
			}
			detail, err := loadTelegramChannelDetail(ctx, conn, identity.Organization.ID, channelID, false)
			if err != nil {
				return err
			}
			output = &detail.MessageChannelRecord
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}
