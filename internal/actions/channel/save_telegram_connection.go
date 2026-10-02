//go:build server

package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

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

// SaveTelegramConnectionAction 保存 Telegram 接入方式、Token、机器人身份和回调密钥。
type SaveTelegramConnectionAction struct {
	db     *bun.DB
	runner *connectiontest.Runner
	api    telegram.BotAPI
}

// NewSaveTelegramConnectionAction 创建 Telegram 连接保存操作。
func NewSaveTelegramConnectionAction(db *bun.DB, runner *connectiontest.Runner, api telegram.BotAPI) *SaveTelegramConnectionAction {
	return &SaveTelegramConnectionAction{db: db, runner: runner, api: api}
}

// Execute 校验 Token，保存接入方式与机器人信息；直连时尝试注册 Webhook，网关转发时不注册也不删除 Webhook。
func (a *SaveTelegramConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input TelegramChannelConnectionInput) (*TelegramChannelDetail, error) {
	if !common.ValidUUID(channelID) {
		return nil, ErrNotFound
	}
	input, fields := normalizeTelegramConnectionInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	webhookURL, err := telegramWebhookURL(input.WebhookBaseURL, channelID)
	if err != nil {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"webhookBaseURL": ValidationTelegramBaseURLInvalid}}
	}

	var detail *TelegramChannelDetail
	err = channelstate.WithTelegramLock(ctx, a.db, channelID, func(conn bun.Conn) error {
		current, err := loadTelegramChannelDetail(ctx, conn, identity.Organization.ID, channelID, false)
		if err != nil {
			return err
		}
		bot, err := runTelegramGetMe(ctx, a.runner, a.api, input.BotToken)
		if err != nil {
			return err
		}
		botIDs := []int64{bot.ID}
		if current.Connection.BotID != nil {
			botIDs = append(botIDs, *current.Connection.BotID)
		}

		return withTelegramBotLocks(ctx, conn, identity.Organization.ID, botIDs, func() error {
			var oldToken string
			var oldBotID *int64
			var oldMode domain.TelegramConnectionMode
			var oldBotUsedByOtherChannel bool
			var enabled bool
			var secret string
			var cancelledRuns int
			err := realtime.RunInTx(ctx, conn, func(ctx context.Context, tx bun.Tx) error {
				if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
					return err
				}
				channel := &servermodels.Channel{}
				if err := tx.NewSelect().
					Model(channel).
					Where("c.id = ?", channelID).
					Where("c.organization_id = ?", identity.Organization.ID).
					Where("c.type = ?", domain.ChannelTypeTelegram).
					For("UPDATE").
					Scan(ctx); err != nil {
					return normalizeTelegramChannelNotFound(err)
				}
				setting := &servermodels.TelegramChannelSetting{}
				if err := tx.NewSelect().
					Model(setting).
					Where("tcs.channel_id = ?", channelID).
					Where("tcs.organization_id = ?", identity.Organization.ID).
					For("UPDATE").
					Scan(ctx); err != nil {
					return normalizeTelegramChannelNotFound(err)
				}
				botAlreadyUsed, err := telegramBotUsedByOtherChannel(ctx, tx, identity.Organization.ID, bot.ID, channelID)
				if err != nil {
					return err
				}
				if botAlreadyUsed && !input.ConfirmBotReuse {
					return ErrTelegramBotReuseConfirmationRequired
				}

				oldToken = optionalStringValue(setting.BotToken)
				oldBotID = setting.BotID
				oldMode = domain.TelegramConnectionMode(setting.ConnectionMode)
				if oldBotID != nil && *oldBotID != bot.ID {
					cancelledRuns, err = chatstate.CancelChannelRuns(ctx, tx, identity.Organization.ID, channelID, domain.AgentRunErrorCodeBotChanged)
					if err != nil {
						return err
					}
					// 更换机器人时终止旧机器人尚未发出的消息。
					if _, err := tx.ExecContext(ctx, "UPDATE customer_message_deliveries SET status = 'failed', last_error = 'bot_changed', updated_at = now() WHERE channel_id = ? AND organization_id = ? AND status IN ('pending', 'retry_wait')", channelID, identity.Organization.ID); err != nil {
						return err
					}
					oldBotUsedByOtherChannel, err = telegramBotUsedByOtherChannel(ctx, tx, identity.Organization.ID, *oldBotID, channelID)
					if err != nil {
						return err
					}
				}
				enabled = channel.Enabled
				gateway := input.ConnectionMode == domain.TelegramConnectionGateway
				// 网关转发沿用业务系统已配置的转发密钥；直连在启用时为本次注册生成新密钥。
				keepSecret := gateway && oldMode == domain.TelegramConnectionGateway && setting.WebhookSecret != nil
				if keepSecret {
					secret = *setting.WebhookSecret
				} else if gateway || enabled {
					secret, err = newTelegramWebhookSecret()
					if err != nil {
						return fmt.Errorf("generate Telegram webhook secret: %w", err)
					}
				}
				status := string(domain.TelegramWebhookStatusWaiting)
				// 合并 getMe 返回的机器人姓名。
				botDisplayName := strings.TrimSpace(strings.TrimSpace(bot.FirstName) + " " + strings.TrimSpace(bot.LastName))
				setting.BotToken = optionalTelegramString(input.BotToken)
				setting.BotID = &bot.ID
				setting.BotUsername = optionalTelegramString(bot.Username)
				setting.BotDisplayName = optionalTelegramString(botDisplayName)
				setting.WebhookBaseURL = optionalTelegramString(input.WebhookBaseURL)
				setting.ConnectionMode = string(input.ConnectionMode)
				// 网关转发在密钥与机器人都未变化时保留已建立的连接状态。
				if !keepSecret || oldBotID == nil || *oldBotID != bot.ID {
					setting.WebhookConnectedAt = nil
					setting.WebhookStatus = nil
					if enabled {
						setting.WebhookStatus = &status
					}
				}
				setting.WebhookSecret = nil
				if secret != "" {
					setting.WebhookSecret = &secret
				}
				_, err = tx.NewUpdate().
					Model(setting).
					Column("connection_mode", "bot_token", "bot_id", "bot_username", "bot_display_name", "webhook_base_url", "webhook_secret", "webhook_status", "webhook_connected_at").
					Set("updated_at = now()").
					WherePK().
					Exec(ctx)
				return err
			})
			if err != nil {
				return err
			}

			if cancelledRuns > 0 {
				slog.Info("Telegram 更换机器人后取消客服运行", "organization_id", identity.Organization.ID, "channel_id", channelID, "cancelled_run_count", cancelledRuns, "reason", domain.AgentRunErrorCodeBotChanged)
			}

			// 只清理本服务直连时为旧机器人注册的 Webhook，网关转发的 Webhook 由业务系统维护。
			if oldMode == domain.TelegramConnectionDirect && oldBotID != nil && *oldBotID != bot.ID && oldToken != "" && !oldBotUsedByOtherChannel {
				if err := runTelegramDeleteWebhook(ctx, a.runner, a.api, oldToken); err != nil {
					logTelegramRemoteFailure("清理旧 Telegram Webhook 失败", channelID, err)
				}
			}
			if enabled && input.ConnectionMode == domain.TelegramConnectionDirect {
				if err := runTelegramSetWebhook(ctx, a.runner, a.api, input.BotToken, webhookURL, secret); err != nil {
					logTelegramRemoteFailure("注册 Telegram Webhook 失败", channelID, err)
				} else {
					slog.Info("Telegram Webhook 注册成功", "channel_id", channelID)
				}
			}
			detail, err = loadTelegramChannelDetail(ctx, conn, identity.Organization.ID, channelID, false)
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// normalizeTelegramChannelNotFound 统一转换渠道或设置缺失错误。
func normalizeTelegramChannelNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// logTelegramRemoteFailure 记录不含 Token 和远端 URL 的 Telegram 失败信息。
func logTelegramRemoteFailure(message, channelID string, err error) {
	stage, kind, classified := connectiontest.Details(err)
	if !classified {
		slog.Warn(message, "channel_id", channelID)
		return
	}
	slog.Warn(message, "channel_id", channelID, "stage", stage, "kind", kind)
}
