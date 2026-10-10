//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"testing"
	"time"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/messagepreview"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestCustomerReplies 验证客服引用、客户身份、公开摘要、幂等和删除后的展示。
func TestCustomerReplies(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "第一问 <script>alert(1)</script>\n第二行")
	require.NoError(t, err)
	name := "渠道客户"
	_, err = f.db.NewUpdate().Table("channel_identities").Set("display_name = ?", name).Where("channel_id = ?", f.channelID).Exec(ctx)
	require.NoError(t, err)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	input := servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "回答第一问", ReplyToMessageID: original.Message.ID}
	reply, err := send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.NotNil(t, reply.ReplyTo)
	require.Equal(t, original.Message.Body, reply.ReplyTo.Body)
	require.Equal(t, domain.ChatSubjectKindContact, reply.ReplyTo.Sender.Kind)
	require.NotNil(t, reply.ReplyTo.Sender.DisplayName)
	require.Equal(t, name, *reply.ReplyTo.Sender.DisplayName)
	replay, err := send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, reply.ID, replay.ID)
	require.NotNil(t, replay.ReplyTo)
	require.Equal(t, name, *replay.ReplyTo.Sender.DisplayName)
	for _, change := range []servicesessionaction.ServiceTextMessageInput{
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, Body: "改过的回答", ReplyToMessageID: original.Message.ID},
		{ConversationID: f.conversationID, ClientMessageID: input.ClientMessageID, Body: input.Body, ReplyToMessageID: reply.ID},
	} {
		_, err := send.Execute(ctx, f.owner, change)
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "changed retry")
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "changed retry")
	}
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	reference := history.Messages[len(history.Messages)-1].ReplyTo
	require.NotNil(t, reference)
	require.Equal(t, original.Message.Body, reference.Body)
	require.Equal(t, name, *reference.Sender.DisplayName)
	own, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "补充说明", ReplyToMessageID: reply.ID})
	require.NoError(t, err)
	require.NotNil(t, own.ReplyTo)
	require.Equal(t, f.owner.WorkspaceIdentity.ID, own.ReplyTo.Sender.SourceID)
	visitor := direct.NewWebsiteVisitorBackend(f.db, nil, testEnqueuer, nil, servertest.TestDeployment(t, f.db).S3, servertest.DisabledMail{}, nil)
	page, err := visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	visitorReference := page.Messages[len(page.Messages)-2].ReplyTo
	require.NotNil(t, visitorReference)
	require.Equal(t, "visitor", visitorReference.Author)
	require.Equal(t, messagepreview.Text(original.Message.Body, false), visitorReference.Preview)
	agentReference := page.Messages[len(page.Messages)-1].ReplyTo
	require.NotNil(t, agentReference)
	require.Equal(t, "agent", agentReference.Author)
	require.Equal(t, input.Body, agentReference.Preview)
	agentSender := page.Messages[len(page.Messages)-1]
	require.Equal(t, f.owner.WorkspaceIdentity.ID, agentSender.SenderIdentityID)
	require.Equal(t, f.owner.WorkspaceIdentity.DisplayName, agentSender.SenderName)
	require.Empty(t, agentSender.SenderAvatarURL)
	visitorSender := page.Messages[0]
	require.Equal(t, "visitor", visitorSender.Author)
	require.Empty(t, visitorSender.SenderIdentityID)
	require.Empty(t, visitorSender.SenderName)
	require.Empty(t, visitorSender.SenderAvatarURL)
	// 访客历史按发送身份当前的有效头像返回公开地址。
	avatar := &servermodels.File{
		ID: uuid.NewV7().String(), WorkspaceID: f.owner.Workspace.ID, CreatedByUserID: f.owner.User.ID, Purpose: string(domain.FilePurposeUserAvatar),
		StorageBackend: string(domain.FileStorageBackendLocal), StorageKey: "avatars/" + uuid.NewV7().String() + ".png",
		OriginalName: "avatar.png", ContentType: "image/png", ByteSize: 1, Status: string(domain.FileStatusActive),
	}
	_, err = f.db.NewInsert().Model(avatar).Exec(ctx)
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).Set("avatar_file_id = ?", avatar.ID).Where("id = ?", f.owner.WorkspaceIdentity.ID).Exec(ctx)
	require.NoError(t, err)
	page, err = visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	require.Equal(t, "/storage/"+avatar.StorageKey, page.Messages[len(page.Messages)-1].SenderAvatarURL)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx)
	require.NoError(t, err)
	replay, err = send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.Equal(t, reply.ID, replay.ID)
	require.NotNil(t, replay.ReplyTo)
	require.True(t, replay.ReplyTo.Deleted)
	require.Empty(t, replay.ReplyTo.Body)
	require.Nil(t, replay.ReplyTo.Sender)
	history, err = conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	memberReference := history.Messages[len(history.Messages)-2].ReplyTo
	require.NotNil(t, memberReference)
	require.True(t, memberReference.Deleted)
	require.Empty(t, memberReference.Body)
	require.Nil(t, memberReference.Sender)
	page, err = visitor.ListMessages(ctx, appservice.WebsiteVisitorMeta{}, f.channelID, "web-session:0123456789abcdef0123456789abcdef", f.conversationID, appservice.WebsiteVisitorMessageHistoryInput{})
	require.NoError(t, err)
	deletedReference := page.Messages[len(page.Messages)-2].ReplyTo
	require.NotNil(t, deletedReference)
	require.True(t, deletedReference.Deleted)
	require.Empty(t, deletedReference.Preview)
	require.Empty(t, deletedReference.Author)
}

// TestCustomerReplyBoundaries 验证无效引用的事务回滚及会话发送资格校验。
func TestCustomerReplyBoundaries(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	foreign := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "原文")
	require.NoError(t, err)
	foreignMessage, err := foreign.visitorMessage(ctx, "其他企业")
	require.NoError(t, err)
	other := f.send(t, f.owner, "同企业其他会话", false)
	system := &servermodels.Message{WorkspaceID: f.owner.Workspace.ID, ConversationID: f.conversationID, Type: "system", Body: "系统事件", OriginatedAt: time.Now().UTC()}
	appendTestMessage(t, f.db, system)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx)
	require.NoError(t, err)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	before, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	require.NoError(t, err)
	for _, target := range []string{original.Message.ID, foreignMessage.Message.ID, other.ID, system.ID, uuid.NewV7().String()} {
		_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "无效回复", ReplyToMessageID: target})
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "target=%s", target)
		require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, conflict.Reason, "target=%s", target)
	}
	after, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "invalid writes")
	session := &servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(session).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Scan(ctx))
	require.Nil(t, session.AssigneeIdentityID, "invalid reference claimed queue")
	valid, err := f.visitorMessage(ctx, "有效问题")
	require.NoError(t, err)
	input := servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "有效回复", ReplyToMessageID: valid.Message.ID}
	_, err = send.Execute(ctx, foreign.owner, input)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "foreign sender")
	_, err = send.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	input.ClientMessageID = uuid.NewV7().String()
	_, err = send.Execute(ctx, f.member, input)
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict, "other assignee")
	require.Equal(t, conversationaction.ConflictReasonServiceSessionOwned, conflict.Reason, "other assignee")
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = send.Execute(ctx, f.owner, input)
	require.ErrorAs(t, err, &conflict, "closed reply")
	require.Equal(t, conversationaction.ConflictReasonServiceSessionNotReplyable, conflict.Reason, "closed reply")
	_, err = customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: f.channelID, ExternalID: "web-session:ffffffffffffffffffffffffffffffff", ConversationID: f.conversationID})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "foreign visitor")
}

// TestCustomerReplyEarlierSession 验证跨客服周期引用和窗口外定位后保留个人阅读状态。
func TestCustomerReplyEarlierSession(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "上一处理周期的问题")
	require.NoError(t, err)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	for i := range 60 {
		result, err := f.visitorMessage(ctx, fmt.Sprintf("后续问题 %d", i))
		require.NoError(t, err, "new cycle %d", i)
		if i == 0 {
			require.True(t, result.OpenedNewServiceSession, "new cycle %d", i)
		}
	}
	reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "针对早期问题", ReplyToMessageID: original.Message.ID})
	require.NoError(t, err)
	require.NotNil(t, reply.ReplyTo)
	require.Equal(t, original.Message.ID, reply.ReplyTo.ID)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	latest, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	require.True(t, latest.HasEarlier)
	require.NotEqual(t, original.Message.ID, latest.Messages[0].ID)
	window, err := query.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID, AroundMessageID: original.Message.ID})
	require.NoError(t, err)
	require.True(t, window.HasLater)
	require.Len(t, window.Messages, 27)
	require.Equal(t, original.Message.ID, window.Messages[1].ID)
	count, err := f.db.NewSelect().Model((*servermodels.ConversationUserState)(nil)).Where("cus.conversation_id = ? AND cus.user_id = ?", f.conversationID, f.member.User.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "location changed read")
}

// TestWebsiteVisitorReplies 验证访客引用双方消息、重试、删除和客服历史展示。
func TestWebsiteVisitorReplies(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	agent, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服说明"})
	require.NoError(t, err)
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "引用客服说明", ReplyToMessageID: agent.ID}
	reply, err := f.receive.Execute(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, reply.Message.ReplyTo)
	require.Equal(t, domain.MessageAuthorAgent, reply.Message.ReplyTo.Author)
	require.Equal(t, agent.Body, reply.Message.ReplyTo.Body)
	require.NotNil(t, reply.Message.ReplyTo.SenderIdentityType)
	require.Equal(t, domain.WorkspaceIdentityTypeUser, *reply.Message.ReplyTo.SenderIdentityType)
	replay, err := f.receive.Execute(ctx, input)
	require.NoError(t, err)
	require.Equal(t, reply.Message.ID, replay.Message.ID)
	require.NotNil(t, replay.Message.ReplyTo)
	for _, target := range []string{"", reply.Message.ID} {
		changed := input
		changed.ReplyToMessageID = target
		_, err := f.receive.Execute(ctx, changed)
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "changed reference")
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason, "changed reference")
	}
	ownInput := input
	ownInput.ClientMessageID = uuid.NewV7().String()
	ownInput.ReplyToMessageID = reply.Message.ID
	own, err := f.receive.Execute(ctx, ownInput)
	require.NoError(t, err)
	require.NotNil(t, own.Message.ReplyTo)
	require.Equal(t, domain.MessageAuthorVisitor, own.Message.ReplyTo.Author)
	require.Equal(t, input.Body, own.Message.ReplyTo.Body)
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	staffReference := history.Messages[len(history.Messages)-2].ReplyTo
	require.NotNil(t, staffReference)
	require.Equal(t, agent.ID, staffReference.ID)
	require.Equal(t, agent.Body, staffReference.Body)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", agent.ID).Exec(ctx)
	require.NoError(t, err)
	replay, err = f.receive.Execute(ctx, input)
	require.NoError(t, err)
	require.Equal(t, reply.Message.ID, replay.Message.ID)
	require.NotNil(t, replay.Message.ReplyTo)
	require.True(t, replay.Message.ReplyTo.Deleted)
	require.Empty(t, replay.Message.ReplyTo.Body)
	require.Empty(t, replay.Message.ReplyTo.Author)
}

// TestWebsiteVisitorReplyBoundaries 验证引用的会话和访客边界，失败时不重新打开客服周期。
func TestWebsiteVisitorReplyBoundaries(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	foreign := newCustomerReadFixture(t)
	ctx := context.Background()
	original, err := f.visitorMessage(ctx, "将删除的原文")
	require.NoError(t, err)
	foreignMessage, err := foreign.visitorMessage(ctx, "其他企业消息")
	require.NoError(t, err)
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ClientMessageID: uuid.NewV7().String(), Body: "同一访客另一会话"})
	require.NoError(t, err)
	internal := f.send(t, f.owner, "内部消息", false)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", original.Message.ID).Exec(ctx)
	require.NoError(t, err)
	system := &servermodels.Message{WorkspaceID: f.owner.Workspace.ID, ConversationID: f.conversationID, Type: "system", Body: "系统事件", OriginatedAt: time.Now().UTC()}
	appendTestMessage(t, f.db, system)
	before, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	require.NoError(t, err)
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "无效引用"}
	for _, target := range []string{original.Message.ID, foreignMessage.Message.ID, other.Message.ID, internal.ID, system.ID, uuid.NewV7().String()} {
		input.ReplyToMessageID = target
		_, err := f.receive.Execute(ctx, input)
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "target=%s", target)
		require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, conflict.Reason, "target=%s", target)
	}
	after, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", f.conversationID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "invalid writes")
	open, err := f.db.NewSelect().Model((*servermodels.ServiceSession)(nil)).Where("ss.conversation_id = ? AND ss.status = ?", f.conversationID, domain.ServiceSessionStatusOpen).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, open, "invalid reference reopened session")
	input.ExternalID = "web-session:ffffffffffffffffffffffffffffffff"
	_, err = f.receive.Execute(ctx, input)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "foreign visitor")
	// 已关闭周期中的有效原文仍属于当前会话，访客引用可打开新周期。
	input.ExternalID = "web-session:0123456789abcdef0123456789abcdef"
	input.ConversationID = &other.Conversation.ID
	input.ReplyToMessageID = other.Message.ID
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, other.Conversation.ID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, other.Conversation.ID)
	require.NoError(t, err)
	valid, err := f.receive.Execute(ctx, input)
	require.NoError(t, err)
	require.True(t, valid.OpenedNewServiceSession)
	require.NotNil(t, valid.Message.ReplyTo)
	require.Equal(t, other.Message.ID, valid.Message.ReplyTo.ID)
}
