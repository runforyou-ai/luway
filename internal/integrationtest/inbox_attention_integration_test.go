//go:build server

package integrationtest

import (
	"context"
	"slices"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// readAttention 读取已知消息之后计入成员提醒的未读消息。
func readAttention(t *testing.T, query *inboxaction.LoadInboxQuery, identity *servermodels.Identity, conversationID, afterMessageID string) []inboxaction.AttentionMessage {
	t.Helper()
	attention, err := query.ReadAttention(context.Background(), identity, conversationID, afterMessageID)
	if err != nil || attention == nil {
		t.Fatalf("attention=%v err=%v", attention, err)
	}
	return attention.Messages
}

// attentionMessageIDs 读取已知消息之后计入成员提醒的未读消息编号。
func attentionMessageIDs(t *testing.T, query *inboxaction.LoadInboxQuery, identity *servermodels.Identity, conversationID, afterMessageID string) []string {
	t.Helper()
	messages := readAttention(t, query, identity, conversationID, afterMessageID)
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}

// TestConversationAttentionCustomerPending 验证客户消息只计入待处理该会话的成员提醒，已读后不再返回，未给出已知消息时只返回最新一条。
func TestConversationAttentionCustomerPending(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	first, err := f.visitorMessage(ctx, "有人吗")
	if err != nil {
		t.Fatal(err)
	}
	// 公共队列中无人负责时，所有成员都待领取。
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		if ids := attentionMessageIDs(t, query, identity, f.conversationID, ""); !slices.Equal(ids, []string{first.Message.ID}) {
			t.Fatalf("queued identity=%s ids=%v", identity.User.ID, ids)
		}
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	second, err := f.visitorMessage(ctx, "还在吗")
	if err != nil {
		t.Fatal(err)
	}
	third, err := f.visitorMessage(ctx, "急")
	if err != nil {
		t.Fatal(err)
	}
	// 由群主负责后，客户来信只计入群主提醒。
	if ids := attentionMessageIDs(t, query, f.owner, f.conversationID, first.Message.ID); !slices.Equal(ids, []string{second.Message.ID, third.Message.ID}) {
		t.Fatalf("assignee ids=%v", ids)
	}
	if ids := attentionMessageIDs(t, query, f.member, f.conversationID, first.Message.ID); len(ids) != 0 {
		t.Fatalf("other member ids=%v", ids)
	}
	// 其他成员的内部备注计入负责人提醒，并标明可见范围与发送者。
	note, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "我来看看", Visibility: domain.MessageVisibilityInternal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if messages := readAttention(t, query, f.owner, f.conversationID, third.Message.ID); len(messages) != 1 || messages[0].ID != note.ID ||
		messages[0].Visibility != domain.MessageVisibilityInternal || messages[0].SenderName == nil || *messages[0].SenderName != "成员" {
		t.Fatalf("note=%+v", messages)
	}
	// 在其他端读到第二条后，只剩之后的未读。
	if _, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.owner, f.conversationID, second.Message.ID, false); err != nil {
		t.Fatal(err)
	}
	if ids := attentionMessageIDs(t, query, f.owner, f.conversationID, first.Message.ID); !slices.Equal(ids, []string{third.Message.ID, note.ID}) {
		t.Fatalf("after read ids=%v", ids)
	}
}

// TestConversationAttentionGroup 验证群聊计入他人发送的未读消息，本人发言推进已读，系统事件不计未读也不提醒，静音后只计入提醒本人或所有人的消息，未给出已知消息时只判断最新一条。
func TestConversationAttentionGroup(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	base := f.send(t, f.member, "基线", false)
	plain := f.send(t, f.owner, "普通消息", false)
	if ids := attentionMessageIDs(t, query, f.member, f.groupID, base.ID); !slices.Equal(ids, []string{plain.ID}) {
		t.Fatalf("unmuted ids=%v", ids)
	}
	// 本人发言推进已读水位，之前的消息不再计入。
	own := f.send(t, f.member, "本人消息", false)
	if ids := attentionMessageIDs(t, query, f.member, f.groupID, base.ID); len(ids) != 0 {
		t.Fatalf("after own message ids=%v", ids)
	}
	// 群主改名产生的系统事件既不增加未读，也不进入提醒。
	if _, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "改名后的群"}); err != nil {
		t.Fatal(err)
	}
	if ids := attentionMessageIDs(t, query, f.member, f.groupID, own.ID); len(ids) != 0 {
		t.Fatalf("system event ids=%v", ids)
	}
	if _, counts, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat}); err != nil || counts.Unread != 0 || counts.Attention != 0 {
		t.Fatalf("system event counts=%+v err=%v", counts, err)
	}
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	muted := f.send(t, f.owner, "静音后普通消息", false)
	mentioned := f.send(t, f.owner, "提醒成员", false, f.subjectID)
	all := f.send(t, f.owner, "提醒所有人", true)
	if ids := attentionMessageIDs(t, query, f.member, f.groupID, own.ID); !slices.Equal(ids, []string{mentioned.ID, all.ID}) || slices.Contains(ids, muted.ID) {
		t.Fatalf("muted ids=%v", ids)
	}
	// 没有已知消息时只判断最新一条：最新一条是普通消息时，不回溯更早的未读提及。
	latest := f.send(t, f.owner, "最新普通消息", false)
	if ids := attentionMessageIDs(t, query, f.member, f.groupID, ""); len(ids) != 0 {
		t.Fatalf("no baseline plain latest ids=%v", ids)
	}
	latestMention := f.send(t, f.owner, "最新提醒", false, f.subjectID)
	if ids := attentionMessageIDs(t, query, f.member, f.groupID, ""); !slices.Equal(ids, []string{latestMention.ID}) || slices.Contains(ids, latest.ID) {
		t.Fatalf("no baseline mention latest ids=%v", ids)
	}
}

// TestConversationAttentionDirect 验证单聊附件返回文件名与发送者，静音后不再计入提醒。
func TestConversationAttentionDirect(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := directchataction.NewSendAttachmentMessageAction(f.db, nil)
	sent, err := send.Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "spec.pdf", "application/pdf"),
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := readAttention(t, query, f.member, sent.ConversationID, "")
	if len(messages) != 1 || messages[0].ID != sent.Message.ID || messages[0].Type != domain.MessageTypeAttachment ||
		messages[0].AttachmentName == nil || *messages[0].AttachmentName != "spec.pdf" || messages[0].SenderName == nil || *messages[0].SenderName != "群主" {
		t.Fatalf("attachment=%+v", messages)
	}
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, sent.ConversationID, true); err != nil {
		t.Fatal(err)
	}
	next, err := send.Execute(ctx, f.owner, directchataction.AttachmentMessageInput{
		ConversationID: sent.ConversationID, ClientMessageID: uuid.NewV7().String(),
		FileID: uploadedAttachment(t, f.db, f.owner, "next.pdf", "application/pdf"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids := attentionMessageIDs(t, query, f.member, sent.ConversationID, sent.Message.ID); len(ids) != 0 || next.Message.ID == "" {
		t.Fatalf("muted direct ids=%v", ids)
	}
}
