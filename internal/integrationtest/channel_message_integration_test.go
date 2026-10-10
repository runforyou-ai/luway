//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	"github.com/runforyou-ai/luway/internal/actions/channelmessage"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/realtime"
	models "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestChannelMessageOpaqueIdentifiers 验证通用入站保存非数字编号，并按账号与外部会话关联迟到引用。
func TestChannelMessageOpaqueIdentifiers(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	channel := &models.Channel{}
	require.NoError(t, f.db.NewSelect().Model(channel).Where("c.id = ?", f.channelID).Scan(ctx))
	// 使用渠道通用入站契约核验平台消息编号原值。
	receive := func(input channelmessage.Inbound, body string) (channelinboundaction.Result, error) {
		var result channelinboundaction.Result
		err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			var err error
			result, err = channelinboundaction.Receive(ctx, tx, testEnqueuer, channel, channelinboundaction.Input{
				ExternalID: "contact:opaque", SingleConversation: true, Body: body, OriginatedAt: time.Now().UTC(),
				IdempotencyKey: f.channelID + ":" + input.AccountID + "|" + input.ConversationID + "|" + input.MessageID,
				ChannelMessage: &input,
			})
			return err
		})
		return result, err
	}
	input := channelmessage.Inbound{
		AccountID: "account:alpha/001", ConversationID: "conversation:abc@service", MessageID: "message:reply/002",
		Reply: &channelmessage.Reply{MessageID: "message:original/α", Body: "外部原文", SenderName: "外部发送者"},
	}
	source, err := receive(input, "引用消息")
	require.NoError(t, err)
	state := readWindowMessage(t, f.db, f.owner, source.Message.ConversationID, source.Message.ID)
	require.NotNil(t, state.ReplyTo, "external snapshot=%+v", state)
	require.Empty(t, state.ReplyTo.ID)
	require.Equal(t, input.Reply.Body, state.ReplyTo.Body)
	for _, target := range []channelmessage.Inbound{
		{AccountID: "account:alpha/1", ConversationID: input.ConversationID, MessageID: input.Reply.MessageID},
		{AccountID: input.AccountID, ConversationID: "conversation:other@service", MessageID: input.Reply.MessageID},
	} {
		_, err := receive(target, "其他命名空间的原文")
		require.NoError(t, err)
	}
	var stored models.Message
	require.NoError(t, f.db.NewSelect().Model(&stored).Where("msg.id = ?", source.Message.ID).Scan(ctx))
	require.Nil(t, stored.ReplyToMessageID, "cross-namespace reply")
	original, err := receive(channelmessage.Inbound{AccountID: input.AccountID, ConversationID: input.ConversationID, MessageID: input.Reply.MessageID}, "外部原文")
	require.NoError(t, err)
	replayed, err := receive(input, "引用消息")
	require.NoError(t, err)
	require.False(t, replayed.Inserted, "late mapping replay=%+v", replayed)
	require.Equal(t, source.Message.ID, replayed.Message.ID)
	require.NotNil(t, replayed.Message.ReplyToMessageID)
	require.Equal(t, original.Message.ID, *replayed.Message.ReplyToMessageID)
	state = readWindowMessage(t, f.db, f.owner, source.Message.ConversationID, source.Message.ID)
	require.NotNil(t, state.ReplyTo, "linked state=%+v", state)
	require.Equal(t, original.Message.ID, state.ReplyTo.ID)
	input.Reply = &channelmessage.Reply{MessageID: "message:different"}
	_, err = receive(input, "引用消息")
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict, "changed external reply accepted")
	require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason)
}
