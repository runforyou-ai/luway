//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestTelegramInboundEvents 验证 Telegram 回调经通用入站链路处理：不支持的消息写入会话并只在首次写入时投递提示任务，提示任务在渠道接待时经渠道发送，渠道改连其他机器人后同一聊天与消息编号的新消息照常写入。
func TestTelegramInboundEvents(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	sender := &deliverySender{}
	adapters := telegramAdapters(f.db, sender, nil)
	webhook := newTelegramWebhookWith(f.db, adapters, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer, nil)
	countMessages := func() int {
		t.Helper()
		count, err := f.db.NewSelect().Model((*models.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
		require.NoError(t, err)
		return int(count)
	}
	require.Equal(t, 1, countMessages())

	unsupported := telegramUpdate{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: 2, DisplayName: "Telegram 客户", OriginatedAt: time.Now().UTC(), Unsupported: true,
	}}
	// 平台重推已处理的事件时不再重复提示。
	for range 2 {
		require.NoError(t, webhook.Execute(ctx, f.channelID, unsupported))
	}
	notices := queuedNotices(t, testEnqueuer, f.owner.Workspace.ID)
	require.Len(t, notices, 1)
	require.Equal(t, i18n.LocalizeCustomerTemplate(domain.CustomerLocaleChineseSimplified, i18n.ChannelUnsupportedMessage, nil), notices[0].Body)
	require.Equal(t, "12345", notices[0].Recipient)
	sendNotice := channelinboundaction.NewSendNoticeAction(f.db, adapters)
	require.NoError(t, sendNotice.Execute(ctx, notices[0]))
	require.Equal(t, []string{notices[0].Body}, sender.bodies)
	// 渠道停用后待发送的提示不再发送。
	_, err := f.db.ExecContext(ctx, "UPDATE channels SET enabled = false WHERE id = ?", f.channelID)
	require.NoError(t, err)
	require.NoError(t, sendNotice.Execute(ctx, notices[0]))
	require.Len(t, sender.bodies, 1)
	_, err = f.db.ExecContext(ctx, "UPDATE channels SET enabled = true WHERE id = ?", f.channelID)
	require.NoError(t, err)
	require.Equal(t, 2, countMessages())
	var latest models.Message
	require.NoError(t, f.db.NewSelect().Model(&latest).Where("msg.conversation_id = ?", f.conversationID).OrderExpr("msg.message_seq DESC").Limit(1).Scan(ctx))
	require.Equal(t, string(domain.MessageTypeUnsupported), latest.Type)
	require.Empty(t, latest.Body)

	// 新机器人的消息编号与原机器人已处理的消息相同时按新事件写入。
	require.NoError(t, connectTestTelegramBot(ctx, f.db, f.channelID, 456, "456:token"))
	require.NoError(t, webhook.Execute(ctx, f.channelID, telegramUpdate{Secret: "secret", UpdateID: 1, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: 2, DisplayName: "Telegram 客户", Body: "换机器人后的消息", OriginatedAt: time.Now().UTC(),
	}}))
	require.Equal(t, 3, countMessages())
}
