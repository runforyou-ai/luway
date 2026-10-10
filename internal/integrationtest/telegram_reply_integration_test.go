//go:build server

package integrationtest

import (
	"context"
	"strconv"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
)

// receiveReply 通过真实入站事务保存引用消息。
func (f channelDeliveryFixture) receiveReply(t *testing.T, id int64, body string, reply *telegram.InboundReply) {
	t.Helper()
	receiver := newTelegramWebhook(f.db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	require.NoError(t, receiver.Execute(context.Background(), f.channelID, telegramUpdate{Secret: "secret", UpdateID: id,
		Message: &telegram.InboundMessage{ChatID: 12345, SenderID: 12345, MessageID: id, DisplayName: "Telegram 客户", Body: body, OriginatedAt: time.Now().UTC(), Reply: reply}}))
}

// replyHistory 读取带引用关系和可用状态的真实会话窗口。
func (f channelDeliveryFixture) replyHistory(t *testing.T) []conversationaction.ConversationMessage {
	t.Helper()
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(context.Background(), f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	// 客服周期系统事件不参与引用与投递断言。
	return arr.Filter(history.Messages, func(message conversationaction.ConversationMessage) bool {
		return message.Type != domain.MessageTypeSystem
	})
}

// sendReply 保存引用回复并读取其固定投递目标。
func (f channelDeliveryFixture) sendReply(t *testing.T, target string) models.ChannelMessageDelivery {
	t.Helper()
	msg, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(context.Background(), f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "引用回答", ReplyToMessageID: target})
	require.NoError(t, err)
	var delivery models.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&delivery).Where("message_id = ?", msg.ID).Scan(context.Background()))
	return delivery
}

// TestTelegramReplyRoundTrip 验证引用客户和客服消息、平台参数以及幂等入站。
func TestTelegramReplyRoundTrip(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	original := f.replyHistory(t)[0]
	require.False(t, original.ReplyUnavailable, "inbound message cannot be replied to")
	delivery := f.sendReply(t, original.ID)
	require.NotNil(t, delivery.ReplyProviderMessageID, "target")
	require.Equal(t, "1", *delivery.ReplyProviderMessageID, "target")
	pendingState := readWindowMessage(t, f.db, f.owner, f.conversationID, delivery.MessageID)
	require.True(t, pendingState.ReplyUnavailable, "pending state")
	require.NotNil(t, pendingState.Delivery, "pending state")
	require.Equal(t, domain.ChannelDeliveryPending, pendingState.Delivery.Status, "pending state")
	sent := f.execute(t, delivery.ID)
	sentState := readWindowMessage(t, f.db, f.owner, f.conversationID, delivery.MessageID)
	require.False(t, sentState.ReplyUnavailable, "sent state")
	require.NotNil(t, sentState.Delivery, "sent state")
	require.Equal(t, domain.ChannelDeliverySent, sentState.Delivery.Status, "sent state")
	require.Equal(t, domain.ChannelDeliverySent, sent.Status, "sent")
	require.NotNil(t, f.sender.replies[0], "sent")
	require.Equal(t, "1", *f.sender.replies[0], "sent")
	sentProviderID, err := strconv.ParseInt(f.providerMessageID(t, sent.ID), 10, 64)
	require.NoError(t, err)
	reply := &telegram.InboundReply{MessageID: sentProviderID, Body: "引用回答", SenderName: "Bot", SenderIsBot: true}
	f.receiveReply(t, 2, "我引用客服的回答", reply)
	f.receiveReply(t, 2, "我引用客服的回答", reply)
	history := f.replyHistory(t)
	require.Len(t, history, 3, "history")
	require.NotNil(t, history[2].ReplyTo, "history")
	require.Equal(t, delivery.MessageID, history[2].ReplyTo.ID, "history")
	require.Equal(t, "引用回答", history[2].ReplyTo.Body, "history")
	next := f.sendReply(t, delivery.MessageID)
	f.execute(t, next.ID)
	require.NotNil(t, next.ReplyProviderMessageID, "outbound target lost")
	require.Equal(t, strconv.FormatInt(sentProviderID, 10), *next.ReplyProviderMessageID, "outbound target lost")
}

// TestTelegramInternalNoteReplyEligibility 验证 Telegram 客户会话中的内部备注可被引用、不产生投递，且备注引用不受渠道投递条件限制。
func TestTelegramInternalNoteReplyEligibility(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	f.receiveReply(t, 5001, "客户问题", nil)
	note, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
		Body: "内部备注：核对过工单", Visibility: domain.MessageVisibilityInternal,
	})
	require.NoError(t, err)
	messages := f.replyHistory(t)
	saved := messages[len(messages)-1]
	require.Equal(t, note.ID, saved.ID, "telegram internal note")
	require.False(t, saved.ReplyUnavailable, "telegram internal note")
	deliveries, err := f.db.NewSelect().Model((*models.ChannelMessageDelivery)(nil)).Where("message_id = ?", note.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, deliveries, "telegram internal note deliveries")

	t.Run("尚未投递的对客消息仍可被内部备注引用", func(t *testing.T) {
		send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
		pending, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "稍等，我确认一下",
		})
		require.NoError(t, err)
		noteQuote, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "这条还没发出去", ReplyToMessageID: pending.ID, Visibility: domain.MessageVisibilityInternal,
		})
		require.NoError(t, err)
		require.NotNil(t, noteQuote.ReplyTo, "note reply to pending message")
		require.Equal(t, pending.ID, noteQuote.ReplyTo.ID, "note reply to pending message")
	})
}

// TestTelegramReplyLateMapping 验证乱序原消息、迟到回执、引用快照和重放关联稳定。
func TestTelegramReplyLateMapping(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	reply := &telegram.InboundReply{MessageID: 9, Body: "较早原文", SenderName: "外部客户"}
	f.receiveReply(t, 10, "原消息后到", reply)
	history := f.replyHistory(t)
	snapshot := history[1].ReplyTo
	require.NotNil(t, snapshot, "snapshot")
	require.Empty(t, snapshot.ID, "snapshot")
	require.Equal(t, reply.Body, snapshot.Body, "snapshot")
	require.Equal(t, reply.SenderName, snapshot.ExternalSenderName, "snapshot")
	f.receiveReply(t, 9, "较早原文", nil)
	f.receiveReply(t, 10, "原消息后到", reply)
	history = f.replyHistory(t)
	require.Len(t, history, 3, "late original")
	require.NotNil(t, history[1].ReplyTo, "late original")
	require.Equal(t, history[2].ID, history[1].ReplyTo.ID, "late original")
	delivery := f.send(t, "稍后返回的客服回执", uuid.NewV7().String())
	f.receiveReply(t, 11, "回复先于回执", &telegram.InboundReply{MessageID: 1001, Body: "稍后返回的客服回执", SenderName: "Bot", SenderIsBot: true})
	f.execute(t, delivery.ID)
	history = f.replyHistory(t)
	require.Equal(t, delivery.MessageID, history[len(history)-1].ReplyTo.ID, "late receipt not linked")
	// 核验重放时保留首次接收的平台引用信息。
	f.receiveReply(t, 10, "原消息后到", &telegram.InboundReply{MessageID: 1})
	history = f.replyHistory(t)
	require.Equal(t, history[2].ID, history[1].ReplyTo.ID, "conflicting replay changed target")
}

// TestTelegramReplyTargetBoundaries 验证无回执、跨会话、删除与换机器人时禁止引用。
func TestTelegramReplyTargetBoundaries(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	other := newChannelDeliveryFixture(t)
	original := f.replyHistory(t)[0]
	pending := f.send(t, "尚未投递", uuid.NewV7().String())
	for _, target := range []string{pending.MessageID, other.replyHistory(t)[0].ID, uuid.NewV7().String()} {
		_, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(context.Background(), f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "无效引用", ReplyToMessageID: target})
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "target=%s", target)
		require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, conflict.Reason, "target=%s", target)
	}
	_, err := f.db.ExecContext(context.Background(), "UPDATE channel_message_deliveries SET status = 'needs_review' WHERE id = ?", pending.ID)
	require.NoError(t, err)
	require.NoError(t, deliveryaction.NewManager(f.db, nil).Resolve(context.Background(), f.owner, f.conversationID, pending.ID, domain.ChannelDeliveryConfirmSent, false))
	require.True(t, f.replyHistory(t)[1].ReplyUnavailable, "manual confirmation invented platform identity")
	f.receiveReply(t, 2, "引用后删除", &telegram.InboundReply{MessageID: 1, Body: original.Body})
	_, err = f.db.ExecContext(context.Background(), "UPDATE messages SET deleted_at = now() WHERE id = ?", original.ID)
	require.NoError(t, err)
	history := f.replyHistory(t)
	deleted := history[len(history)-1].ReplyTo
	require.NotNil(t, deleted, "deleted")
	require.True(t, deleted.Deleted, "deleted")
	require.Empty(t, deleted.Body, "deleted")
	require.Empty(t, deleted.ExternalSenderName, "deleted")
	_, err = f.db.ExecContext(context.Background(), "UPDATE channels SET provider_account_id = '456' WHERE id = ?", f.channelID)
	require.NoError(t, err)
	for _, message := range f.replyHistory(t) {
		require.True(t, message.ReplyUnavailable, "old bot remains replyable")
	}
	// 核验引用消息的平台机器人归属。
	f.receiveReply(t, 3, "新机器人的引用", &telegram.InboundReply{MessageID: 2, Body: "新机器人原文"})
	history = f.replyHistory(t)
	require.Empty(t, history[len(history)-1].ReplyTo.ID, "cross-bot reference")
}

// TestTelegramReplyDeliveryFailures 验证限流重试保留引用及平台拒绝时的发送失败状态。
func TestTelegramReplyDeliveryFailures(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	delivery := f.sendReply(t, f.replyHistory(t)[0].ID)
	f.sender.err = &telegram.SendError{Code: "rate_limited", RetryAfter: time.Millisecond}
	require.Equal(t, domain.ChannelDeliveryRetryWait, f.execute(t, delivery.ID).Status, "delivery")
	_, err := f.db.ExecContext(context.Background(), "UPDATE channel_message_deliveries SET available_at = now() WHERE id = ?", delivery.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(context.Background(), "UPDATE channel_send_gates SET flood_wait_until = now() WHERE channel_id = ?", f.channelID)
	require.NoError(t, err)
	f.sender.err = &telegram.SendError{Code: "message_rejected"}
	require.Equal(t, domain.ChannelDeliveryFailed, f.execute(t, delivery.ID).Status, "delivery")
	require.Len(t, f.sender.replies, 2, "reply target changed")
	require.Equal(t, "1", *f.sender.replies[0], "reply target changed")
	require.Equal(t, "1", *f.sender.replies[1], "reply target changed")
	f.execute(t, delivery.ID)
	require.Len(t, f.sender.replies, 2, "rejected reply resent")
}
