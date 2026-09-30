//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/actions/channelmessage"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	"github.com/runforyou-ai/cervi/internal/realtime"
	models "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestChannelMessageOpaqueIdentifiers 验证通用入站保存非数字编号，并按账号与外部会话关联迟到引用。
func TestChannelMessageOpaqueIdentifiers(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	channel := &models.Channel{}
	if err := f.db.NewSelect().Model(channel).Where("c.id = ?", f.channelID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// 使用渠道通用入站契约核验平台消息编号原值。
	receive := func(input channelmessage.Inbound, body string) (customerchataction.InboundCustomerMessageResult, error) {
		var result customerchataction.InboundCustomerMessageResult
		err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			var err error
			result, err = customerchataction.ReceiveInboundCustomerMessage(ctx, tx, newTestTasks(f.db), channel, customerchataction.InboundCustomerMessageInput{
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
	if err != nil {
		t.Fatal(err)
	}
	if state := readWindowMessage(t, f.db, f.owner, source.Message.ConversationID, source.Message.ID); state.ReplyTo == nil || state.ReplyTo.ID != "" || state.ReplyTo.Body != input.Reply.Body {
		t.Fatalf("external snapshot=%+v", state)
	}
	for _, target := range []channelmessage.Inbound{
		{AccountID: "account:alpha/1", ConversationID: input.ConversationID, MessageID: input.Reply.MessageID},
		{AccountID: input.AccountID, ConversationID: "conversation:other@service", MessageID: input.Reply.MessageID},
	} {
		if _, err := receive(target, "其他命名空间的原文"); err != nil {
			t.Fatal(err)
		}
	}
	var stored models.Message
	if err := f.db.NewSelect().Model(&stored).Where("msg.id = ?", source.Message.ID).Scan(ctx); err != nil || stored.ReplyToMessageID != nil {
		t.Fatalf("cross-namespace reply=%+v err=%v", stored.ReplyToMessageID, err)
	}
	original, err := receive(channelmessage.Inbound{AccountID: input.AccountID, ConversationID: input.ConversationID, MessageID: input.Reply.MessageID}, "外部原文")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := receive(input, "引用消息")
	if err != nil || replayed.Inserted || replayed.Message.ID != source.Message.ID || replayed.Message.ReplyToMessageID == nil || *replayed.Message.ReplyToMessageID != original.Message.ID {
		t.Fatalf("late mapping replay=%+v err=%v", replayed, err)
	}
	if state := readWindowMessage(t, f.db, f.owner, source.Message.ConversationID, source.Message.ID); state.ReplyTo == nil || state.ReplyTo.ID != original.Message.ID {
		t.Fatalf("linked state=%+v", state)
	}
	input.Reply = &channelmessage.Reply{MessageID: "message:different"}
	_, err = receive(input, "引用消息")
	var conflict *conversationaction.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
		t.Fatalf("changed external reply accepted: %v", err)
	}
}
