//go:build server

package telegram

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/agentcancel"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// SaveConnectionAction 保存 Telegram 接入方式、Token、机器人身份和回调密钥。
type SaveConnectionAction struct {
	db     *bun.DB
	runner *connectiontest.Runner
	api    telegram.BotAPI
}

// NewSaveConnectionAction 创建 Telegram 连接保存操作。
func NewSaveConnectionAction(db *bun.DB, runner *connectiontest.Runner, api telegram.BotAPI) *SaveConnectionAction {
	return &SaveConnectionAction{db: db, runner: runner, api: api}
}

// Execute 校验 Token，保存接入方式与机器人信息；直连时尝试注册 Webhook，网关转发时不注册也不删除 Webhook。
func (a *SaveConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input ConnectionInput) (*ChannelDetail, error) {
	input, fields := normalizeConnectionInput(input)
	if len(fields) > 0 {
		return nil, &channelaction.ValidationError{Fields: fields}
	}
	webhookURL, err := buildWebhookURL(input.WebhookBaseURL, channelID)
	if err != nil {
		return nil, &channelaction.ValidationError{Fields: map[string]channelaction.ValidationCode{"webhookBaseURL": ValidationBaseURLInvalid}}
	}

	var detail *ChannelDetail
	err = serverstorage.WithSessionLock(ctx, a.db, serverstorage.LockTelegramChannel, []string{channelID}, func(conn bun.Conn) error {
		current, err := loadChannelDetail(ctx, conn, identity.Workspace.ID, channelID, false)
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

		return withTelegramBotLocks(ctx, conn, identity.Workspace.ID, botIDs, func() error {
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
					Where("c.workspace_id = ?", identity.Workspace.ID).
					Where("c.type = ?", domain.ChannelTypeTelegram).
					For("UPDATE").
					Scan(ctx); err != nil {
					return normalizeTelegramChannelNotFound(err)
				}
				setting := &servermodels.TelegramChannelSetting{}
				if err := tx.NewSelect().
					Model(setting).
					Where("tcs.channel_id = ?", channelID).
					Where("tcs.workspace_id = ?", identity.Workspace.ID).
					For("UPDATE").
					Scan(ctx); err != nil {
					return normalizeTelegramChannelNotFound(err)
				}
				botAlreadyUsed, err := telegramBotUsedByOtherChannel(ctx, tx, identity.Workspace.ID, bot.ID, channelID)
				if err != nil {
					return err
				}
				if botAlreadyUsed && !input.ConfirmBotReuse {
					return ErrBotReuseConfirmationRequired
				}

				oldToken = support.Deref(setting.BotToken)
				oldBotID = telegramBotID(channel)
				oldMode = domain.TelegramConnectionMode(setting.ConnectionMode)
				if oldBotID != nil && *oldBotID != bot.ID {
					cancelledRuns, err = agentcancel.CancelChannelRuns(ctx, tx, identity.Workspace.ID, channelID, domain.AgentRunErrorCodeAccountChanged)
					if err != nil {
						return err
					}
					// 更换机器人时终止旧机器人尚未发出的消息。
					if _, err := tx.ExecContext(ctx, "UPDATE channel_message_deliveries SET status = 'failed', last_error = 'account_changed' WHERE channel_id = ? AND workspace_id = ? AND status IN ('pending', 'retry_wait')", channelID, identity.Workspace.ID); err != nil {
						return err
					}
					oldBotUsedByOtherChannel, err = telegramBotUsedByOtherChannel(ctx, tx, identity.Workspace.ID, *oldBotID, channelID)
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
					secret = random.Hex(32)
				}
				status := string(domain.TelegramWebhookStatusWaiting)
				// 合并 getMe 返回的机器人姓名。
				botDisplayName := strings.TrimSpace(strings.TrimSpace(bot.FirstName) + " " + strings.TrimSpace(bot.LastName))
				setting.BotToken = support.NilIfZero(strings.TrimSpace(input.BotToken))
				setting.BotUsername = support.NilIfZero(strings.TrimSpace(bot.Username))
				setting.BotDisplayName = support.NilIfZero(botDisplayName)
				setting.WebhookBaseURL = support.NilIfZero(strings.TrimSpace(input.WebhookBaseURL))
				setting.ConnectionMode = string(input.ConnectionMode)
				// 网关转发在密钥与机器人都未变化时保留已建立的连接状态。
				if !keepSecret || oldBotID == nil || *oldBotID != bot.ID {
					setting.WebhookConnectedAt = nil
					setting.WebhookStatus = nil
					if enabled {
						setting.WebhookStatus = &status
					}
				}
				setting.WebhookSecret = support.NilIfZero(secret)
				botAccountID := strconv.FormatInt(bot.ID, 10)
				if _, err := tx.NewUpdate().Model(channel).Set("provider_account_id = ?", botAccountID).WherePK().Exec(ctx); err != nil {
					return err
				}
				_, err = tx.NewUpdate().
					Model(setting).
					Column("connection_mode", "bot_token", "bot_username", "bot_display_name", "webhook_base_url", "webhook_secret", "webhook_status", "webhook_connected_at").
					WherePK().
					Exec(ctx)
				return err
			})
			if err != nil {
				return err
			}

			if cancelledRuns > 0 {
				slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "Telegram 更换机器人后取消客服运行", "channel_id", channelID, "cancelled_run_count", cancelledRuns, "reason", domain.AgentRunErrorCodeAccountChanged)
			}

			// 只清理本服务直连时为旧机器人注册的 Webhook，网关转发的 Webhook 由业务系统维护。
			if oldMode == domain.TelegramConnectionDirect && oldBotID != nil && *oldBotID != bot.ID && oldToken != "" && !oldBotUsedByOtherChannel {
				if err := runTelegramDeleteWebhook(ctx, a.runner, a.api, oldToken); err != nil {
					logTelegramRemoteFailure(ctx, "清理旧 Telegram Webhook 失败", channelID, err)
				} else {
					slog.InfoContext(ctx, "已清理旧 Telegram Webhook", "channel_id", channelID)
				}
			}
			if enabled && input.ConnectionMode == domain.TelegramConnectionDirect {
				if err := runTelegramSetWebhook(ctx, a.runner, a.api, input.BotToken, webhookURL, secret); err != nil {
					logTelegramRemoteFailure(ctx, "注册 Telegram Webhook 失败", channelID, err)
				} else {
					slog.InfoContext(ctx, "Telegram Webhook 注册成功", "channel_id", channelID)
				}
			}
			detail, err = loadChannelDetail(ctx, conn, identity.Workspace.ID, channelID, false)
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
		return channelaction.ErrNotFound
	}
	return err
}

// logTelegramRemoteFailure 记录不含 Token 和远端 URL 的 Telegram 失败信息。
func logTelegramRemoteFailure(ctx context.Context, message, channelID string, err error) {
	stage, kind, classified := connectiontest.Details(err)
	if !classified {
		slog.WarnContext(ctx, message, "channel_id", channelID)
		return
	}
	slog.WarnContext(ctx, message, "channel_id", channelID, "stage", stage, "kind", kind)
}
