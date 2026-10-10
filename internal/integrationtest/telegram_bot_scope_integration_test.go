//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestTelegramBotReuseScope 验证 Bot 占用确认和 Webhook 清理守卫都限定在当前企业。
func TestTelegramBotReuseScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()

	const sharedToken = "123456:shared_bot_token"
	telegramAPI := &telegramBotAPIFake{bot: telegramintegration.Bot{
		ID: 555000111, IsBot: true, FirstName: "Shared", Username: "shared_scope_bot",
	}}
	runner := connectiontest.NewRunner(time.Second)
	saveTelegram := telegramaction.NewSaveConnectionAction(db, runner, telegramAPI)
	updateStatus := telegramaction.NewUpdateChannelStatusAction(db, runner, telegramAPI, testEnqueuer)
	connection := telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect, BotToken: sharedToken, WebhookBaseURL: "http://127.0.0.1:34115/app"}

	first := newTelegramScopeWorkspace(t, db, "第一企业")
	second := newTelegramScopeWorkspace(t, db, "第二企业")

	_, err = saveTelegram.Execute(ctx, first.identity, first.channelID, connection)
	require.NoError(t, err, "首次绑定 Bot 失败")

	// 其它企业绑定同一个 Bot 不触发占用确认，不暴露该 Bot 在别处的绑定情况。
	_, err = saveTelegram.Execute(ctx, second.identity, second.channelID, connection)
	require.NoError(t, err, "其它企业绑定同一 Bot，期望直接成功")

	// 同企业内的第二个渠道复用同一个 Bot 仍要求确认。
	handoverChannel := newTelegramScopeChannel(t, db, second.identity, "同企业交接渠道")
	_, err = saveTelegram.Execute(ctx, second.identity, handoverChannel, connection)
	require.ErrorIs(t, err, telegramaction.ErrBotReuseConfirmationRequired, "同企业复用 Bot，期望要求确认")
	confirmed := connection
	confirmed.ConfirmBotReuse = true
	_, err = saveTelegram.Execute(ctx, second.identity, handoverChannel, confirmed)
	require.NoError(t, err)

	// 同企业内仍有渠道引用该 Bot，停用其中一个渠道保留共享 Webhook。
	deletedBefore := len(telegramAPI.deletedTokens())
	_, err = updateStatus.Execute(ctx, second.identity, handoverChannel, false)
	require.NoError(t, err)
	require.Len(t, telegramAPI.deletedTokens(), deletedBefore, "本企业仍引用该 Bot 时清理了 Webhook")

	// 本企业已无其它渠道引用该 Bot，停用后清理 Webhook；其它企业的引用不参与判断。
	_, err = updateStatus.Execute(ctx, first.identity, first.channelID, false)
	require.NoError(t, err)
	deleted := telegramAPI.deletedTokens()
	require.Len(t, deleted, deletedBefore+1, "本企业无其它引用时的 Webhook 清理记录")
	require.Equal(t, sharedToken, deleted[deletedBefore], "本企业无其它引用时的 Webhook 清理记录")
}

// telegramScopeWorkspace 保存一个测试企业及其 Telegram 渠道。
type telegramScopeWorkspace struct {
	identity  *servermodels.Identity
	channelID string
}

// newTelegramScopeWorkspace 创建独立测试工作区并建立 Telegram 渠道。
func newTelegramScopeWorkspace(t *testing.T, db *bun.DB, name string) telegramScopeWorkspace {
	t.Helper()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: name, DisplayName: "管理员", Email: servertest.UniqueEmail("admin"), Password: "password123",
		Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	return telegramScopeWorkspace{
		identity:  installed.Identity,
		channelID: newTelegramScopeChannel(t, db, installed.Identity, name+" 渠道"),
	}
}

// newTelegramScopeChannel 在指定企业下创建启用的 Telegram 渠道。
func newTelegramScopeChannel(t *testing.T, db *bun.DB, identity *servermodels.Identity, name string) string {
	t.Helper()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(context.Background(), identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeTelegram, Name: name, DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	return channel.ID
}
