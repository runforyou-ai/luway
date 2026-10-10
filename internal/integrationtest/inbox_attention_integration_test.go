//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	"github.com/google/go-cmp/cmp"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// messageAttention 返回指定消息作为成员提醒的通知摘要，消息不计入该成员提醒时返回 nil。
func messageAttention(t *testing.T, query *inboxaction.LoadInboxQuery, identity *servermodels.Identity, conversationID, messageID string) *inboxaction.AttentionMessage {
	t.Helper()
	attention, err := query.ReadMessageAttention(context.Background(), inboxaction.ViewerOf(identity), conversationID, messageID)
	require.NoError(t, err)
	require.NotNil(t, attention)
	if len(attention.Messages) == 0 {
		return nil
	}
	return &attention.Messages[0]
}

// attended 返回指定消息中计入成员提醒的消息编号。
func attended(t *testing.T, query *inboxaction.LoadInboxQuery, identity *servermodels.Identity, conversationID string, messageIDs ...string) []string {
	t.Helper()
	return arr.Filter(messageIDs, func(messageID string) bool {
		return messageAttention(t, query, identity, conversationID, messageID) != nil
	})
}

// TestConversationAttentionCustomerPending 验证客户消息只计入待处理该会话的成员提醒，内部备注计入负责人提醒，已读后不再计入。
func TestConversationAttentionCustomerPending(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	first, err := f.visitorMessage(ctx, "有人吗")
	require.NoError(t, err)
	// 公共队列中无人负责时，所有成员都待领取。
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		require.Equal(t, []string{first.Message.ID}, attended(t, query, identity, f.conversationID, first.Message.ID), "queued identity=%s", identity.User.ID)
	}
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	second, err := f.visitorMessage(ctx, "还在吗")
	require.NoError(t, err)
	third, err := f.visitorMessage(ctx, "急")
	require.NoError(t, err)
	// 由群主负责后，客户来信只计入群主提醒。
	require.Equal(t, []string{second.Message.ID, third.Message.ID}, attended(t, query, f.owner, f.conversationID, second.Message.ID, third.Message.ID), "assignee")
	require.Empty(t, attended(t, query, f.member, f.conversationID, second.Message.ID, third.Message.ID), "other member")
	// 其他成员的内部备注计入负责人提醒，并标明可见范围与发送者。
	note, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "我来看看", Visibility: domain.MessageVisibilityInternal,
	})
	require.NoError(t, err)
	message := messageAttention(t, query, f.owner, f.conversationID, note.ID)
	require.NotNil(t, message)
	require.Equal(t, domain.MessageVisibilityInternal, message.Visibility)
	require.NotNil(t, message.SenderName)
	require.Equal(t, "成员", *message.SenderName)
	// 在其他端读到第二条后，第二条不再计入提醒。
	_, err = conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.owner, f.conversationID, second.Message.ID, false)
	require.NoError(t, err)
	require.Equal(t, []string{third.Message.ID, note.ID}, attended(t, query, f.owner, f.conversationID, second.Message.ID, third.Message.ID, note.ID), "after read")
}

// TestConversationAttentionGroup 验证群聊计入他人发送的未读消息，本人发言推进已读，系统事件不计未读，静音后只计入提醒本人或所有人的消息。
func TestConversationAttentionGroup(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	plain := f.send(t, f.owner, "普通消息", false)
	require.Equal(t, []string{plain.ID}, attended(t, query, f.member, f.groupID, plain.ID), "unmuted")
	// 本人发言推进已读水位，之前的消息不再计入。
	f.send(t, f.member, "本人消息", false)
	require.Empty(t, attended(t, query, f.member, f.groupID, plain.ID), "after own message")
	// 群主改名产生的系统事件不增加未读。
	_, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "改名后的群"})
	require.NoError(t, err)
	_, counts, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Zero(t, counts.Unread, "system event")
	require.Zero(t, counts.Attention, "system event")
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true)
	require.NoError(t, err)
	muted := f.send(t, f.owner, "静音后普通消息", false)
	mentioned := f.send(t, f.owner, "提醒成员", false, f.subjectID)
	all := f.send(t, f.owner, "提醒所有人", true)
	require.Equal(t, []string{mentioned.ID, all.ID}, attended(t, query, f.member, f.groupID, muted.ID, mentioned.ID, all.ID), "muted")
}

// requireBatchMatchesSingle 核对批量读取的消息提醒与逐个读取的结果一致，并返回批量结果。
func requireBatchMatchesSingle(t *testing.T, query *inboxaction.LoadInboxQuery, identities []*servermodels.Identity, conversationID, messageID string) []*inboxaction.ConversationAttention {
	t.Helper()
	ctx := context.Background()
	viewers := make([]inboxaction.Viewer, len(identities))
	single := make([]*inboxaction.ConversationAttention, len(identities))
	for index, identity := range identities {
		viewers[index] = inboxaction.ViewerOf(identity)
		attention, err := query.ReadMessageAttention(ctx, viewers[index], conversationID, messageID)
		require.NoError(t, err)
		single[index] = attention
	}
	batch, err := query.ReadMessageAttentions(ctx, viewers, conversationID, messageID)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(single, batch), "batch differs from single reads")
	return batch
}

// TestMessageAttentionsGroupBatch 验证群聊中多名成员的批量提醒与逐个读取一致：被提及的静音成员、未读成员、发送者与群外成员各按本人口径返回。
func TestMessageAttentionsGroupBatch(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	// 新增一名群内未读成员与一名不在群中的成员。
	addMember := func(name string) *servermodels.Identity {
		email := servertest.UniqueEmail(name)
		_, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{DisplayName: name, Email: email, Password: "password123", RoleID: f.owner.User.RoleID})
		require.NoError(t, err)
		return servertest.LoginMember(t, f.db, f.owner.Workspace.ID, email, "password123").Identity
	}
	reader, outsider := addMember("reader"), addMember("outsider")
	group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{
		Title: "批量提醒群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID, reader.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	detail, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.owner, group.ID)
	require.NoError(t, err)
	var memberSubjectID string
	for _, participant := range detail.Participants {
		if participant.IdentityID == f.member.WorkspaceIdentity.ID {
			memberSubjectID = participant.ChatSubjectID
		}
	}
	require.NotEmpty(t, memberSubjectID)
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, group.ID, true)
	require.NoError(t, err)
	send := func(body string, subjects ...string) string {
		message, err := newGroupSendAction(f.db).Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: body, MentionSubjectIDs: subjects})
		require.NoError(t, err)
		return message.ID
	}
	identities := []*servermodels.Identity{f.member, reader, f.owner, outsider}
	mentioned := send("提醒成员", memberSubjectID)
	batch := requireBatchMatchesSingle(t, query, identities, group.ID, mentioned)
	require.Len(t, batch, len(identities))
	// 静音成员被提及时计入提醒。
	require.NotNil(t, batch[0])
	require.Len(t, batch[0].Messages, 1)
	require.Equal(t, mentioned, batch[0].Messages[0].ID)
	require.Equal(t, 1, batch[0].Conversation.MentionedUnreadCount)
	// 未静音的未读成员计入提醒。
	require.NotNil(t, batch[1])
	require.Len(t, batch[1].Messages, 1)
	require.Equal(t, 1, batch[1].Conversation.UnreadCount)
	require.Zero(t, batch[1].Conversation.MentionedUnreadCount)
	// 发送者可读会话但消息不计入提醒，群外成员无权阅读。
	require.NotNil(t, batch[2])
	require.Empty(t, batch[2].Messages)
	require.Nil(t, batch[3])
	// 未提及本人的消息不计入静音成员的提醒，仍计入未读成员的提醒。
	plain := send("普通消息")
	batch = requireBatchMatchesSingle(t, query, identities, group.ID, plain)
	require.Empty(t, batch[0].Messages)
	require.Equal(t, 2, batch[0].Conversation.UnreadCount)
	require.Len(t, batch[1].Messages, 1)
	require.Nil(t, batch[3])
}

// TestMessageAttentionsCustomerBatch 验证客户会话中多名成员的批量提醒与逐个读取一致，待处理条目按各自负责关系给出。
func TestMessageAttentionsCustomerBatch(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	identities := []*servermodels.Identity{f.owner, f.member}
	first, err := f.visitorMessage(ctx, "有人吗")
	require.NoError(t, err)
	batch := requireBatchMatchesSingle(t, query, identities, f.conversationID, first.Message.ID)
	for index := range identities {
		require.NotNil(t, batch[index])
		require.NotNil(t, batch[index].Conversation.Pending)
		require.Equal(t, domain.InboxPendingKindQueue, batch[index].Conversation.Pending.Kind)
		require.Len(t, batch[index].Messages, 1)
	}
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	second, err := f.visitorMessage(ctx, "还在吗")
	require.NoError(t, err)
	batch = requireBatchMatchesSingle(t, query, identities, f.conversationID, second.Message.ID)
	require.Len(t, batch[0].Messages, 1)
	require.NotNil(t, batch[1])
	require.Nil(t, batch[1].Conversation.Pending)
	require.Empty(t, batch[1].Messages)
}
