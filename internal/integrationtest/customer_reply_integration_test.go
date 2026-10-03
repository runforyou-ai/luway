//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/messagepreview"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestCustomerReplies 验证客服引用、客户身份、公开摘要、幂等和删除后的展示。
func TestCustomerReplies(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "第一问 <script>alert(1)</script>\n第二行")
	if err != nil {
		t.Fatal(err)
	}
	name := "渠道客户"
	if _, err := f.db.NewUpdate().Table("contact_channel_identities").Set("display_name = ?", name).Where("channel_id = ?", f.channelID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	input := servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "回答第一问", ReplyToMessageID: original.Message.ID}
	reply, err := send.Execute(ctx, f.owner, input)
	if err != nil || reply.ReplyTo == nil || reply.ReplyTo.Body != original.Message.Body || reply.ReplyTo.Sender.Kind != domain.ChatSubjectKindContact || reply.ReplyTo.Sender.DisplayName == nil || *reply.ReplyTo.Sender.DisplayName != name {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
	replay, err := send.Execute(ctx, f.owner, input)
	if err != nil || replay.ID != reply.ID || replay.ReplyTo == nil || *replay.ReplyTo.Sender.DisplayName != name {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	for _, change := range []servicesessionaction.ServiceTextMessageInput{
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, Body: "改过的回答", ReplyToMessageID: original.Message.ID},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: reply.ID},
	} {
		_, err := send.Execute(ctx, f.owner, change)
		var conflict *conversationaction.ConflictError
		if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed retry=%v", err)
		}
	}
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	reference := history.Messages[len(history.Messages)-1].ReplyTo
	if reference == nil || reference.Body != original.Message.Body || *reference.Sender.DisplayName != name {
		t.Fatalf("history reference=%+v", reference)
	}
	own, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "补充说明", ReplyToMessageID: reply.ID})
	if err != nil || own.ReplyTo == nil || own.ReplyTo.Sender.SourceID != f.owner.OrganizationIdentity.ID {
		t.Fatalf("own reference=%+v err=%v", own.ReplyTo, err)
	}
	visitor := direct.NewWebsiteVisitorBackend(f.db, nil, newTestTasks(f.db), nil, serverfilecontent.S3Config{}, nil, nil)
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if r := page.Messages[len(page.Messages)-2].ReplyTo; r == nil || r.Author != "visitor" || r.Preview != messagepreview.Text(original.Message.Body, false) {
		t.Fatalf("visitor reference=%+v", r)
	}
	if r := page.Messages[len(page.Messages)-1].ReplyTo; r == nil || r.Author != "agent" || r.Preview != input.Body {
		t.Fatalf("agent reference=%+v", r)
	}
	if m := page.Messages[len(page.Messages)-1]; m.SenderIdentityID != f.owner.OrganizationIdentity.ID || m.SenderName != f.owner.OrganizationIdentity.DisplayName || m.SenderAvatarURL != "" {
		t.Fatalf("agent sender=%+v", m)
	}
	if m := page.Messages[0]; m.Author != "visitor" || m.SenderIdentityID != "" || m.SenderName != "" || m.SenderAvatarURL != "" {
		t.Fatalf("visitor sender=%+v", m)
	}
	// 访客历史按发送身份当前的有效头像返回公开地址。
	avatar := &servermodels.File{
		ID: uuid.NewV7().String(), OrganizationID: f.owner.Organization.ID, CreatedByUserID: f.owner.User.ID, Purpose: string(domain.FilePurposeUserAvatar),
		StorageBackend: string(domain.FileStorageBackendLocal), StorageKey: "avatars/" + uuid.NewV7().String() + ".png",
		OriginalName: "avatar.png", ContentType: "image/png", ByteSize: 1, Status: string(domain.FileStatusActive),
	}
	if _, err := f.db.NewInsert().Model(avatar).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).Set("avatar_file_id = ?", avatar.ID).Where("id = ?", f.owner.OrganizationIdentity.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if m := page.Messages[len(page.Messages)-1]; m.SenderAvatarURL != "/storage/"+avatar.StorageKey {
		t.Fatalf("agent avatar=%q", m.SenderAvatarURL)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err = send.Execute(ctx, f.owner, input)
	if err != nil || replay.ID != reply.ID || replay.ReplyTo == nil || !replay.ReplyTo.Deleted || replay.ReplyTo.Body != "" || replay.ReplyTo.Sender != nil {
		t.Fatalf("deleted replay=%+v err=%v", replay, err)
	}
	history, err = conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	if r := history.Messages[len(history.Messages)-2].ReplyTo; r == nil || !r.Deleted || r.Body != "" || r.Sender != nil {
		t.Fatalf("deleted member reference=%+v", r)
	}
	page, err = visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	if err != nil {
		t.Fatal(err)
	}
	if r := page.Messages[len(page.Messages)-2].ReplyTo; r == nil || !r.Deleted || r.Preview != "" || r.Author != "" {
		t.Fatalf("deleted visitor reference=%+v", r)
	}
}

// TestCustomerReplyBoundaries 验证无效引用的事务回滚及会话发送资格校验。
func TestCustomerReplyBoundaries(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	foreign := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "原文")
	if err != nil {
		t.Fatal(err)
	}
	foreignMessage, err := foreign.visitorMessage(ctx, "其他企业")
	if err != nil {
		t.Fatal(err)
	}
	other := f.send(t, f.owner, "同企业其他会话", false)
	system := &servermodels.Message{OrganizationID: f.owner.Organization.ID, ConversationID: f.conversationID, Type: "system", Body: "系统事件", OriginatedAt: time.Now().UTC()}
	appendTestMessage(t, f.db, system)
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	before, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{original.Message.ID, foreignMessage.Message.ID, other.ID, system.ID, uuid.NewV7().String()} {
		_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "无效回复", ReplyToMessageID: target})
		var conflict *conversationaction.ConflictError
		if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonReplyTargetInvalid {
			t.Fatalf("target=%s err=%v", target, err)
		}
	}
	after, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	if err != nil || after != before {
		t.Fatalf("invalid writes: %d -> %d, err=%v", before, after, err)
	}
	session := &servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(session).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if session.AssigneeIdentityID != nil {
		t.Fatalf("invalid reference claimed queue: %+v", session)
	}
	valid, err := f.visitorMessage(ctx, "有效问题")
	if err != nil {
		t.Fatal(err)
	}
	input := servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "有效回复", ReplyToMessageID: valid.Message.ID}
	if _, err := send.Execute(ctx, foreign.owner, input); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("foreign sender=%v", err)
	}
	if _, err := send.Execute(ctx, f.owner, input); err != nil {
		t.Fatal(err)
	}
	input.ClientMessageID = uuid.NewV7().String()
	_, err = send.Execute(ctx, f.member, input)
	var conflict *conversationaction.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonServiceSessionOwned {
		t.Fatalf("other assignee=%v", err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	_, err = send.Execute(ctx, f.owner, input)
	if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonServiceSessionNotReplyable {
		t.Fatalf("closed reply=%v", err)
	}
	_, err = customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: f.channelID, ExternalID: "web-session:ffffffffffffffffffffffffffffffff", ConversationID: f.conversationID})
	if !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("foreign visitor=%v", err)
	}
}

// TestCustomerReplyEarlierSession 验证跨客服周期引用和窗口外定位后保留个人阅读状态。
func TestCustomerReplyEarlierSession(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "上一处理周期的问题")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		result, err := f.visitorMessage(ctx, fmt.Sprintf("后续问题 %d", i))
		if err != nil || (i == 0 && !result.OpenedNewServiceSession) {
			t.Fatalf("new cycle=%+v err=%v", result, err)
		}
	}
	reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "针对早期问题", ReplyToMessageID: original.Message.ID})
	if err != nil || reply.ReplyTo == nil || reply.ReplyTo.ID != original.Message.ID {
		t.Fatalf("earlier reply=%+v err=%v", reply, err)
	}
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	latest, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil || !latest.HasEarlier || latest.Messages[0].ID == original.Message.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	window, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, AroundMessageID: original.Message.ID})
	if err != nil || !window.HasLater || len(window.Messages) != 27 || window.Messages[1].ID != original.Message.ID {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	count, err := f.db.NewSelect().Model((*servermodels.ConversationUserState)(nil)).Where("cus.conversation_id = ? AND cus.user_id = ?", f.conversationID, f.member.User.ID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("location changed read: count=%d err=%v", count, err)
	}
}

// TestWebsiteVisitorReplies 验证访客引用双方消息、重试、删除和客服历史展示。
func TestWebsiteVisitorReplies(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	agent, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服说明"})
	if err != nil {
		t.Fatal(err)
	}
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "引用客服说明", ReplyToMessageID: agent.ID}
	reply, err := f.receive.Execute(ctx, input)
	if err != nil || reply.Message.ReplyTo == nil || reply.Message.ReplyTo.Author != domain.MessageAuthorAgent || reply.Message.ReplyTo.Body != agent.Body || reply.Message.ReplyTo.SenderIdentityType == nil || *reply.Message.ReplyTo.SenderIdentityType != domain.OrganizationIdentityTypeUser {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
	replay, err := f.receive.Execute(ctx, input)
	if err != nil || replay.Message.ID != reply.Message.ID || replay.Message.ReplyTo == nil {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	for _, target := range []string{"", reply.Message.ID} {
		changed := input
		changed.ReplyToMessageID = target
		_, err := f.receive.Execute(ctx, changed)
		var conflict *conversationaction.ConflictError
		if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed reference=%v", err)
		}
	}
	ownInput := input
	ownInput.ClientMessageID = uuid.NewV7().String()
	ownInput.ReplyToMessageID = reply.Message.ID
	own, err := f.receive.Execute(ctx, ownInput)
	if err != nil || own.Message.ReplyTo == nil || own.Message.ReplyTo.Author != domain.MessageAuthorVisitor || own.Message.ReplyTo.Body != input.Body {
		t.Fatalf("own=%+v err=%v", own, err)
	}
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	if r := history.Messages[len(history.Messages)-2].ReplyTo; r == nil || r.ID != agent.ID || r.Body != agent.Body {
		t.Fatalf("staff reference=%+v", r)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", agent.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err = f.receive.Execute(ctx, input)
	if err != nil || replay.Message.ID != reply.Message.ID || replay.Message.ReplyTo == nil || !replay.Message.ReplyTo.Deleted || replay.Message.ReplyTo.Body != "" || replay.Message.ReplyTo.Author != "" {
		t.Fatalf("deleted replay=%+v err=%v", replay, err)
	}
}

// TestWebsiteVisitorReplyBoundaries 验证引用的会话和访客边界，失败时不重新打开客服周期。
func TestWebsiteVisitorReplyBoundaries(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	foreign := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "将删除的原文")
	if err != nil {
		t.Fatal(err)
	}
	foreignMessage, err := foreign.visitorMessage(ctx, "其他企业消息")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "同一访客另一会话"})
	if err != nil {
		t.Fatal(err)
	}
	internal := f.send(t, f.owner, "内部消息", false)
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	system := &servermodels.Message{OrganizationID: f.owner.Organization.ID, ConversationID: f.conversationID, Type: "system", Body: "系统事件", OriginatedAt: time.Now().UTC()}
	appendTestMessage(t, f.db, system)
	before, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "无效引用"}
	for _, target := range []string{original.Message.ID, foreignMessage.Message.ID, other.Message.ID, internal.ID, system.ID, uuid.NewV7().String()} {
		input.ReplyToMessageID = target
		_, err := f.receive.Execute(ctx, input)
		var conflict *conversationaction.ConflictError
		if !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonReplyTargetInvalid {
			t.Fatalf("target=%s err=%v", target, err)
		}
	}
	after, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	if err != nil || after != before {
		t.Fatalf("invalid writes: %d -> %d err=%v", before, after, err)
	}
	open, err := f.db.NewSelect().Model((*servermodels.ServiceSession)(nil)).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Count(ctx)
	if err != nil || open != 0 {
		t.Fatalf("invalid reference reopened session: %d err=%v", open, err)
	}
	input.ExternalID = "web-session:ffffffffffffffffffffffffffffffff"
	_, err = f.receive.Execute(ctx, input)
	if !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("foreign visitor=%v", err)
	}
	// 已关闭周期中的有效原文仍属于当前会话，访客引用可打开新周期。
	input.ExternalID = "web-session:0123456789abcdef0123456789abcdef"
	input.ConversationID = &other.Conversation.ID
	input.ReplyToMessageID = other.Message.ID
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, other.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, other.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	valid, err := f.receive.Execute(ctx, input)
	if err != nil || !valid.OpenedNewServiceSession || valid.Message.ReplyTo == nil || valid.Message.ReplyTo.ID != other.Message.ID {
		t.Fatalf("earlier cycle reply=%+v err=%v", valid, err)
	}
}
