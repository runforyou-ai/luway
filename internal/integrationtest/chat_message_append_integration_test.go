//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestTelegramAppendUsesLocalSequence 验证晚到渠道消息保留来源时间，并按本地顺序推进会话和周期摘要。
func TestTelegramAppendUsesLocalSequence(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	before := &servermodels.Conversation{ID: f.conversationID}
	require.NoError(t, f.db.NewSelect().Model(before).WherePK().Scan(ctx))
	input := telegramUpdate{Secret: "secret", UpdateID: 2, Message: &telegram.InboundMessage{
		ChatID: 12345, SenderID: 12345, MessageID: 2, DisplayName: "Telegram 客户", Body: "晚到的旧消息", OriginatedAt: before.LastMessageAt.Add(-time.Hour),
	}}
	receive := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	for range 2 {
		require.NoError(t, receive.Execute(ctx, f.channelID, input))
	}
	var messages []servermodels.Message
	require.NoError(t, f.db.NewSelect().Model(&messages).Where("conversation_id = ? AND body = ?", f.conversationID, input.Message.Body).Scan(ctx))
	require.Len(t, messages, 1)
	require.True(t, messages[0].OriginatedAt.Equal(input.Message.OriginatedAt), "late message=%+v", messages)
	after := &servermodels.Conversation{ID: f.conversationID}
	require.NoError(t, f.db.NewSelect().Model(after).WherePK().Scan(ctx))
	require.NotNil(t, after.LastMessageID, "late message did not advance summary: before=%+v after=%+v", before, after)
	require.Equal(t, messages[0].ID, *after.LastMessageID)
	require.Equal(t, before.LastMessageSeq+1, after.LastMessageSeq)
	require.Equal(t, after.LastMessageSeq, messages[0].MessageSeq)
	assertCustomerLockSummary(t, ctx, f.db, f.conversationID)
}

// testWebsiteAppendRollback 验证访客首发的消息、双摘要、周期、Trigger 和真实任务唤醒一起回滚。
func testWebsiteAppendRollback(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID string, tasks *servertest.Tasks) {
	t.Helper()
	ctx := context.Background()
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "访客消息回滚", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentID},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	failing := &failingMessageScheduler{inner: agentrunaction.NewScheduler(tasks), failure: errors.New("rollback visitor input")}
	_, err = customerchataction.NewReceiveWebsiteCustomerMessageAction(db, failing, testEnqueuer, servertest.DisabledMail{}).Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "访客首发",
	})
	require.ErrorIs(t, err, failing.failure)
	require.Len(t, failing.runIDs, 1)
	require.NotEmpty(t, failing.conversationID)
	for table, column := range map[string]string{
		"conversations": "id", "channel_conversations": "conversation_id", "service_sessions": "conversation_id",
		"messages": "conversation_id", "conversation_participants": "conversation_id", "agent_lanes": "conversation_id",
		"agent_runs": "conversation_id",
	} {
		count, err := db.NewSelect().TableExpr(table).Where("? = ?", bun.Ident(column), failing.conversationID).Count(ctx)
		require.NoError(t, err, "rollback %s", table)
		require.Zero(t, count, "rollback %s", table)
	}
	inputCount, err := db.NewSelect().TableExpr("agent_inputs AS ai").
		Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
		Where("al.conversation_id = ?", failing.conversationID).Count(ctx)
	require.NoError(t, err, "rollback agent_inputs")
	require.Zero(t, inputCount, "rollback agent_inputs")
	requireNoQueuedRuns(t, tasks, failing.runIDs)
}

// appendTestMessage 在会话锁内通过共用追加入口创建引用边界夹具。
func appendTestMessage(t *testing.T, db *bun.DB, message *servermodels.Message) {
	t.Helper()
	message.ID = uuid.NewV7().String()
	err := realtime.RunInTx(context.Background(), db, func(ctx context.Context, tx bun.Tx) error {
		cv := &servermodels.Conversation{ID: message.ConversationID}
		if err := tx.NewSelect().Model(cv).WherePK().For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		_, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, cv, message)
		return err
	})
	require.NoError(t, err)
}
