//go:build server

package channel

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

const telegramAdapterName = "telegram_bot_api"

// withTelegramBotLocks 按固定顺序锁定 Bot，串行化本企业内跨渠道的 Webhook 生命周期。
func withTelegramBotLocks(ctx context.Context, conn bun.Conn, organizationID string, botIDs []int64, execute func() error) error {
	unique := make(map[int64]struct{}, len(botIDs))
	ordered := make([]int64, 0, len(botIDs))
	for _, botID := range botIDs {
		if botID <= 0 {
			continue
		}
		if _, exists := unique[botID]; exists {
			continue
		}
		unique[botID] = struct{}{}
		ordered = append(ordered, botID)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	for index, botID := range ordered {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended(?, 1))", telegramBotLockKey(organizationID, botID)); err != nil {
			releaseTelegramBotLocks(conn, organizationID, ordered[:index])
			return fmt.Errorf("lock Telegram bot: %w", err)
		}
	}
	defer releaseTelegramBotLocks(conn, organizationID, ordered)
	return execute()
}

// telegramBotLockKey 返回按企业隔离的 Bot 锁键。
func telegramBotLockKey(organizationID string, botID int64) string {
	return organizationID + ":" + strconv.FormatInt(botID, 10)
}

// releaseTelegramBotLocks 逆序释放 Bot 会话锁，失败时丢弃底层连接。
func releaseTelegramBotLocks(conn bun.Conn, organizationID string, botIDs []int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, botID := range slices.Backward(botIDs) {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtextextended(?, 1))", telegramBotLockKey(organizationID, botID)); err != nil {
			slog.Error("释放 Telegram Bot 锁失败", "organization_id", organizationID, "bot_id", botID)
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			return
		}
	}
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

// newTelegramWebhookSecret 生成 Telegram 允许字符范围内的随机 Secret。
func newTelegramWebhookSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// telegramBotUsedByOtherChannel 判断当前企业内是否仍有其它渠道引用该 Bot。
func telegramBotUsedByOtherChannel(ctx context.Context, db bun.IDB, organizationID string, botID int64, channelID string) (bool, error) {
	used, err := db.NewSelect().
		Model((*servermodels.TelegramChannelSetting)(nil)).
		Where("tcs.organization_id = ?", organizationID).
		Where("tcs.bot_id = ?", botID).
		Where("tcs.channel_id <> ?", channelID).
		Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check Telegram bot reuse: %w", err)
	}
	return used, nil
}

// optionalTelegramString 把空字符串转换为数据库空值。
func optionalTelegramString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
