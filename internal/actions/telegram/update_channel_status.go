//go:build server

package telegram

import (
	"context"
	"log/slog"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"

	"github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// UpdateChannelStatusAction 修改 Telegram 渠道状态并维护 Webhook。
type UpdateChannelStatusAction struct {
	db       *bun.DB
	runner   *connectiontest.Runner
	api      telegram.BotAPI
	enqueuer servertask.TxEnqueuer
}

// NewUpdateChannelStatusAction 创建 Telegram 渠道状态操作。
func NewUpdateChannelStatusAction(db *bun.DB, runner *connectiontest.Runner, api telegram.BotAPI, enqueuer servertask.TxEnqueuer) *UpdateChannelStatusAction {
	return &UpdateChannelStatusAction{db: db, runner: runner, api: api, enqueuer: enqueuer}
}

// Execute 修改渠道状态；直连时在事务提交后注册或删除 Webhook，网关转发时保留转发密钥。
func (a *UpdateChannelStatusAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, enabled bool) (*channelaction.MessageChannelRecord, error) {
	var output *channelaction.MessageChannelRecord
	err := serverstorage.WithSessionLock(ctx, a.db, serverstorage.LockTelegramChannel, []string{channelID}, func(conn bun.Conn) error {
		current, err := loadChannelDetail(ctx, conn, identity.Workspace.ID, channelID, false)
		if err != nil {
			return err
		}
		var botIDs []int64
		if current.Connection.BotID != nil {
			botIDs = append(botIDs, *current.Connection.BotID)
		}

		return withTelegramBotLocks(ctx, conn, identity.Workspace.ID, botIDs, func() error {
			var token string
			var gateway bool
			var botUsedByOtherChannel bool
			var webhookURL string
			var secret string
			err := realtime.RunInTx(ctx, conn, func(ctx context.Context, tx bun.Tx) error {
				if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
					return err
				}
				detail, err := loadChannelDetail(ctx, tx, identity.Workspace.ID, channelID, true)
				if err != nil {
					return err
				}
				token = detail.Connection.BotToken
				gateway = detail.Connection.ConnectionMode == string(domain.TelegramConnectionGateway)
				if !enabled && !gateway && detail.Connection.BotID != nil {
					botUsedByOtherChannel, err = telegramBotUsedByOtherChannel(ctx, tx, identity.Workspace.ID, *detail.Connection.BotID, channelID)
					if err != nil {
						return err
					}
				}
				// 尚未保存连接配置时仍可启用，Webhook 留待保存连接后注册；网关转发不注册 Webhook。
				if enabled && !gateway && token != "" && detail.Connection.WebhookBaseURL != "" {
					webhookURL, err = buildWebhookURL(detail.Connection.WebhookBaseURL, channelID)
					if err != nil {
						slog.WarnContext(ctx, "Telegram Webhook 地址无效，跳过注册", "channel_id", channelID, "error", err)
						webhookURL = ""
					} else {
						secret = random.Hex(32)
					}
				}

				if _, err := tx.NewUpdate().
					Model((*servermodels.Channel)(nil)).
					Set("enabled = ?", enabled).
					Where("id = ?", channelID).
					Where("workspace_id = ?", identity.Workspace.ID).
					Exec(ctx); err != nil {
					return err
				}

				// 启停切换改变待发送投递的暂停状态，批量推进相关客户会话版本；启用时恢复推进该渠道暂停的投递。
				if detail.Enabled != enabled {
					if _, err := chatstate.TouchConversations(ctx, tx, identity.Workspace.ID, tx.NewSelect().TableExpr("channel_message_deliveries").Column("conversation_id").
						Where("workspace_id = ? AND channel_id = ? AND status IN (?, ?)", identity.Workspace.ID, channelID, domain.ChannelDeliveryPending, domain.ChannelDeliveryRetryWait), domain.ConversationChangeTimeline); err != nil {
						return err
					}
					if enabled {
						if err := channeldelivery.WakeChannel(ctx, tx, a.enqueuer, identity.Workspace.ID, channelID); err != nil {
							return err
						}
					}
				}

				setting := &servermodels.TelegramChannelSetting{ChannelID: channelID}
				query := tx.NewUpdate().
					Model(setting).
					Set("webhook_connected_at = NULL").
					Where("channel_id = ?", channelID).
					Where("workspace_id = ?", identity.Workspace.ID)
				switch {
				case gateway:
					// 网关转发保留业务系统已配置的转发密钥，启用后等待下一次转发。
					if detail.Connection.WebhookSecret == "" {
						secret = random.Hex(32)
						query = query.Set("webhook_secret = ?", secret)
					}
					if enabled {
						query = query.Set("webhook_status = ?", domain.TelegramWebhookStatusWaiting)
					} else {
						query = query.Set("webhook_status = NULL")
					}
				case secret != "":
					query = query.
						Set("webhook_secret = ?", secret).
						Set("webhook_status = ?", domain.TelegramWebhookStatusWaiting)
				default:
					query = query.
						Set("webhook_secret = NULL").
						Set("webhook_status = NULL")
				}
				_, err = query.Exec(ctx)
				return err
			})
			if err != nil {
				return err
			}

			// 网关转发的 Webhook 由业务系统维护，只在直连时注册或删除。
			if webhookURL != "" {
				if err := runTelegramSetWebhook(ctx, a.runner, a.api, token, webhookURL, secret); err != nil {
					logTelegramRemoteFailure(ctx, "注册 Telegram Webhook 失败", channelID, err)
				} else {
					slog.InfoContext(ctx, "Telegram Webhook 注册成功", "channel_id", channelID)
				}
			} else if !enabled && !gateway && token != "" && !botUsedByOtherChannel {
				if err := runTelegramDeleteWebhook(ctx, a.runner, a.api, token); err != nil {
					logTelegramRemoteFailure(ctx, "删除 Telegram Webhook 失败", channelID, err)
				} else {
					slog.InfoContext(ctx, "Telegram Webhook 已删除", "channel_id", channelID)
				}
			}
			detail, err := loadChannelDetail(ctx, conn, identity.Workspace.ID, channelID, false)
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
