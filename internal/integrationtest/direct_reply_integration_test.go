//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestDirectMessageReplies 验证单聊双方引用、幂等重放及删除后的引用摘要。
func TestDirectMessageReplies(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	first, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "原文",
	})
	if err != nil {
		t.Fatal(err)
	}
	send := directchataction.NewSendDirectTextMessageAction(f.db)
	input := directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "回复", ReplyToMessageID: first.Message.ID}
	reply, err := send.Execute(ctx, f.member, input)
	if err != nil || reply.ReplyTo == nil || reply.ReplyTo.Body != "原文" || reply.ReplyTo.Sender.SourceID != f.owner.OrganizationIdentity.ID {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
	replay, err := send.Execute(ctx, f.member, input)
	if err != nil || replay.ID != reply.ID || replay.ReplyTo == nil || replay.ReplyTo.ID != first.Message.ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	for _, target := range []string{"", reply.ID} {
		changed := input
		changed.ReplyToMessageID = target
		_, err := send.Execute(ctx, f.member, changed)
		var conflict *conversationaction.ConflictError
		if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed target accepted: %v", err)
		}
	}
	input.ClientMessageID = uuid.NewV7().String()
	input.ReplyToMessageID = reply.ID
	ownReply, err := send.Execute(ctx, f.member, input)
	if err != nil || ownReply.ReplyTo == nil || ownReply.ReplyTo.ID != reply.ID || ownReply.ReplyTo.Sender.SourceID != f.member.OrganizationIdentity.ID {
		t.Fatalf("own reply=%+v err=%v", ownReply, err)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", reply.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err = send.Execute(ctx, f.member, input)
	if err != nil || replay.ID != ownReply.ID || replay.ReplyTo == nil || !replay.ReplyTo.Deleted || replay.ReplyTo.Body != "" || replay.ReplyTo.Sender != nil {
		t.Fatalf("deleted replay=%+v err=%v", replay, err)
	}
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
	if err != nil {
		t.Fatal(err)
	}
	last := history.Messages[len(history.Messages)-1]
	if last.ID != ownReply.ID || last.ReplyTo == nil || !last.ReplyTo.Deleted || last.ReplyTo.Body != "" || last.ReplyTo.Sender != nil {
		t.Fatalf("history reference=%+v", last.ReplyTo)
	}
}

// TestDirectReplyBoundaries 验证引用目标的会话、企业、消息类型和删除边界。
func TestDirectReplyBoundaries(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	foreign := newNavigationFixture(t)
	ctx := context.Background()
	first, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "原文",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherConversation := f.send(t, f.owner, "同企业群消息", false)
	otherOrganization := foreign.send(t, foreign.owner, "其他企业消息", false)
	system := &servermodels.Message{OrganizationID: f.owner.Organization.ID, ConversationID: first.Conversation.ID, Type: "system", Body: "系统事件", OriginatedAt: time.Now().UTC()}
	appendTestMessage(t, f.db, system)
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", first.Message.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	send := directchataction.NewSendDirectTextMessageAction(f.db)
	before, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", first.Conversation.ID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{first.Message.ID, otherConversation.ID, otherOrganization.ID, system.ID, uuid.NewV7().String()} {
		_, err := send.Execute(ctx, f.member, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "不允许", ReplyToMessageID: target})
		var conflict *conversationaction.ConflictError
		if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonReplyTargetInvalid {
			t.Fatalf("target=%s err=%v", target, err)
		}
	}
	_, err = send.Execute(ctx, foreign.owner, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "越权", ReplyToMessageID: first.Message.ID})
	if !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("foreign sender error=%v", err)
	}
	after, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", first.Conversation.ID).Count(ctx)
	if err != nil || after != before {
		t.Fatalf("failed send persisted: before=%d after=%d err=%v", before, after, err)
	}
}
