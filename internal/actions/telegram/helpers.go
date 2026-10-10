//go:build server

package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// telegramAdapterName 是连接探测记录中的 Telegram 适配器名称。
const telegramAdapterName = "telegram_bot_api"

// withTelegramBotLocks 在渠道锁所在连接上锁定工作区内的 Bot，串行化跨渠道的 Webhook 生命周期；没有有效 Bot 编号时不加锁直接执行。
func withTelegramBotLocks(ctx context.Context, conn bun.Conn, workspaceID string, botIDs []int64, execute func() error) error {
	keys := make([]string, 0, len(botIDs))
	for _, botID := range botIDs {
		if botID > 0 {
			keys = append(keys, serverstorage.LockKey(workspaceID, strconv.FormatInt(botID, 10)))
		}
	}
	if len(keys) == 0 {
		return execute()
	}
	return serverstorage.HoldSessionLock(logscope.WithWorkspace(ctx, workspaceID), conn, serverstorage.LockTelegramBot, keys, execute)
}

// runTelegramGetMe 使用通用连接探测语义读取机器人身份。
func runTelegramGetMe(ctx context.Context, runner *connectiontest.Runner, api telegram.BotAPI, token string) (telegram.Bot, error) {
	bot := telegram.Bot{}
	err := runner.Run(ctx, telegramTarget(), connectiontest.ProbeFunc(func(testCtx context.Context) error {
		var err error
		bot, err = api.GetMe(testCtx, token)
		return err
	}))
	return bot, err
}

// runTelegramSetWebhook 使用独立超时注册 Webhook。
func runTelegramSetWebhook(ctx context.Context, runner *connectiontest.Runner, api telegram.BotAPI, token, webhookURL, secret string) error {
	return runner.Run(ctx, telegramTarget(), connectiontest.ProbeFunc(func(testCtx context.Context) error {
		return api.SetWebhook(testCtx, token, telegram.Webhook{URL: webhookURL, Secret: secret})
	}))
}

// runTelegramDeleteWebhook 使用独立超时清理 Webhook。
func runTelegramDeleteWebhook(ctx context.Context, runner *connectiontest.Runner, api telegram.BotAPI, token string) error {
	return runner.Run(ctx, telegramTarget(), connectiontest.ProbeFunc(func(testCtx context.Context) error {
		return api.DeleteWebhook(testCtx, token)
	}))
}

// telegramTarget 返回可安全记录的 Telegram 探测目标。
func telegramTarget() connectiontest.Target {
	return connectiontest.Target{
		Category: string(domain.ConnectionProbeTelegram),
		Adapter:  telegramAdapterName,
		Location: string(domain.ConnectionProbeServer),
	}
}

// telegramBotUsedByOtherChannel 判断当前企业内是否仍有其它 Telegram 渠道连接该 Bot。
func telegramBotUsedByOtherChannel(ctx context.Context, db bun.IDB, workspaceID string, botID int64, channelID string) (bool, error) {
	used, err := db.NewSelect().
		Model((*servermodels.Channel)(nil)).
		Where("c.workspace_id = ? AND c.type = ?", workspaceID, domain.ChannelTypeTelegram).
		Where("c.provider_account_id = ?", strconv.FormatInt(botID, 10)).
		Where("c.id <> ?", channelID).
		Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check Telegram bot reuse: %w", err)
	}
	return used, nil
}

// telegramBotID 返回 Telegram 渠道当前连接的机器人编号，未连接时为空。
func telegramBotID(channel *servermodels.Channel) *int64 {
	if channel.ProviderAccountID == nil {
		return nil
	}
	botID, err := strconv.ParseInt(*channel.ProviderAccountID, 10, 64)
	if err != nil {
		return nil
	}
	return &botID
}
