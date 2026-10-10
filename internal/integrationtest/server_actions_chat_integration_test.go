//go:build server

package integrationtest

import (
	"context"
	"testing"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// testDirectChat 覆盖单聊首发、双方收件箱、免打扰未读和内部文本消息。
func (s *serverActionsFixture) testDirectChat(t *testing.T) {
	db, loggedIn := s.db, s.loggedIn
	memberLogin := servertest.LoginMember(t, db, loggedIn.Identity.Workspace.ID, s.createdMember.Email, "password123")

	started, err := directchataction.NewSendFirstDirectTextMessageAction(db, testEnqueuer).Execute(context.Background(), loggedIn.Identity, directchataction.FirstDirectTextMessageInput{TargetIdentityID: memberLogin.Identity.WorkspaceIdentity.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f63", Body: "首发"})
	require.NoError(t, err)
	conversationID := started.Conversation.ID
	requests := []struct {
		identity *servermodels.Identity
		targetID string
	}{
		{identity: loggedIn.Identity, targetID: memberLogin.Identity.WorkspaceIdentity.ID},
		{identity: memberLogin.Identity, targetID: loggedIn.Identity.WorkspaceIdentity.ID},
	}
	findDirect := directchataction.NewFindDirectConversationQuery(db)
	for _, request := range requests {
		foundConversation, findErr := findDirect.Execute(context.Background(), request.identity, request.targetID)
		require.NoError(t, findErr)
		require.NotNil(t, foundConversation)
		require.Equal(t, conversationID, foundConversation.ID)
	}

	inbox := inboxaction.NewLoadInboxQuery(db)
	for _, request := range requests {
		itemsPage, _, loadErr := inbox.Execute(context.Background(), request.identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		items := itemsPage.Conversations
		require.NoError(t, loadErr)
		found := false
		for _, item := range items {
			if item.ID == conversationID && item.Type == domain.ConversationTypeDirect && item.Direct != nil && item.Direct.PeerIdentityID == request.targetID && item.Direct.LastMessageAt != nil {
				found = true
				break
			}
		}
		require.True(t, found, "direct conversation missing from inbox: %#v", items)
	}

	notificationSettings := conversationaction.NewUpdateConversationNotificationSettingsAction(db)
	settings, err := notificationSettings.Execute(context.Background(), loggedIn.Identity, conversationID, true)
	require.NoError(t, err, "mute direct")
	require.True(t, settings.Muted, "mute direct")
	directItemsBeforeMessagePage, countsBeforeDirectMessage, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	directItemsBeforeMessage := directItemsBeforeMessagePage.Conversations
	require.NoError(t, err)
	var unreadBeforeDirectMessage int
	for _, item := range directItemsBeforeMessage {
		if item.ID == conversationID {
			unreadBeforeDirectMessage = item.UnreadCount
		}
	}
	send := directchataction.NewSendDirectTextMessageAction(db, testEnqueuer)
	message, err := send.Execute(context.Background(), memberLogin.Identity, directchataction.InternalTextMessageInput{
		ConversationID:  conversationID,
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
		Body:            "你好，管理员",
	})
	require.NoError(t, err)
	require.NotNil(t, message.Sender, "direct message sender")
	require.Equal(t, memberLogin.Identity.WorkspaceIdentity.ID, message.Sender.SourceID, "direct message sender")
	directItemsPage, countsAfterDirectMessage, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	directItems := directItemsPage.Conversations
	require.NoError(t, err)
	var unreadAfterDirectMessage int
	for _, item := range directItems {
		if item.ID == conversationID {
			require.True(t, item.Muted, "muted direct unread = %#v", item)
			require.Equal(t, unreadBeforeDirectMessage+1, item.UnreadCount, "muted direct unread")
			unreadAfterDirectMessage = item.UnreadCount
		}
	}
	require.Equal(t, countsBeforeDirectMessage.Unread+1, countsAfterDirectMessage.Unread, "muted direct unread count")
	require.Equal(t, countsBeforeDirectMessage.Attention, countsAfterDirectMessage.Attention, "muted direct attention count")
	settings, err = notificationSettings.Execute(context.Background(), loggedIn.Identity, conversationID, false)
	require.NoError(t, err, "unmute direct")
	require.False(t, settings.Muted, "unmute direct")
	_, countsAfterDirectUnmute, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Equal(t, countsAfterDirectMessage.Attention+unreadAfterDirectMessage, countsAfterDirectUnmute.Attention, "unmuted direct attention count")
	history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 2, "direct message history")
}

// testGroupChat 覆盖群聊创建、成员资料、双方收件箱、成员授权、提醒与解散归档。
func (s *serverActionsFixture) testGroupChat(t *testing.T) {
	db, otherInstalled, loggedIn := s.db, s.otherInstalled, s.loggedIn
	memberLogin := servertest.LoginMember(t, db, loggedIn.Identity.Workspace.ID, s.createdMember.Email, "password123")
	observer, err := newTestMemberCreator(db, testEnqueuer).Execute(context.Background(), loggedIn.Identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "群聊旁观者", Email: servertest.UniqueEmail("group-observer"), Password: "password123", RoleID: s.memberRole.ID,
	})
	require.NoError(t, err)
	observerLogin := servertest.LoginMember(t, db, loggedIn.Identity.Workspace.ID, observer.Email, "password123")

	create := groupchataction.NewCreateGroupConversationAction(db)
	group, err := create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
		Title:             "  产品讨论  ",
		MemberIdentityIDs: []string{memberLogin.Identity.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	require.Equal(t, "产品讨论", group.Title)
	require.Equal(t, 2, group.MemberCount)
	_, err = create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
		Title:             "跨企业群聊",
		MemberIdentityIDs: []string{otherInstalled.Identity.WorkspaceIdentity.ID},
	})
	require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "cross-workspace group member")

	get := groupchataction.NewGetGroupConversationQuery(db)
	for _, currentIdentity := range []*servermodels.Identity{loggedIn.Identity, memberLogin.Identity} {
		detail, getErr := get.Execute(context.Background(), currentIdentity, group.ID)
		require.NoError(t, getErr)
		require.Equal(t, group.Title, detail.Title)
		require.Len(t, detail.Participants, 2)
		require.Equal(t, loggedIn.Identity.WorkspaceIdentity.ID, detail.Participants[0].IdentityID)
		require.Equal(t, domain.ConversationParticipantRoleOwner, detail.Participants[0].Role)
		require.Equal(t, memberLogin.Identity.WorkspaceIdentity.ID, detail.Participants[1].IdentityID)
		require.Equal(t, domain.ConversationParticipantRoleMember, detail.Participants[1].Role)
	}
	_, err = get.Execute(context.Background(), otherInstalled.Identity, group.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross-workspace group detail")
	_, err = get.Execute(context.Background(), observerLogin.Identity, group.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "non-participant group detail")

	inbox := inboxaction.NewLoadInboxQuery(db)
	notificationSettings := conversationaction.NewUpdateConversationNotificationSettingsAction(db)
	settings, err := notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, true)
	require.NoError(t, err, "mute empty group")
	require.True(t, settings.Muted, "mute empty group")
	settings, err = notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, false)
	require.NoError(t, err, "unmute empty group")
	require.False(t, settings.Muted, "unmute empty group")
	_, err = notificationSettings.Execute(context.Background(), observerLogin.Identity, group.ID, true)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "non-participant group mute")
	_, err = notificationSettings.Execute(context.Background(), otherInstalled.Identity, group.ID, true)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross-workspace group mute")
	for _, currentIdentity := range []*servermodels.Identity{loggedIn.Identity, memberLogin.Identity} {
		itemsPage, _, loadErr := inbox.Execute(context.Background(), currentIdentity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		items := itemsPage.Conversations
		require.NoError(t, loadErr)
		found := false
		for _, item := range items {
			if item.ID == group.ID && item.Type == domain.ConversationTypeGroup && item.Group != nil && item.Group.Title == group.Title && item.Group.MemberCount == 2 && item.Group.LastMessageAt == nil {
				found = true
				break
			}
		}
		require.True(t, found, "group conversation missing from inbox: %#v", items)
	}

	send := newGroupSendAction(db)
	input := groupchataction.GroupTextMessageInput{
		ConversationID:  group.ID,
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f67",
		Body:            "你好，群聊",
	}
	message, err := send.Execute(context.Background(), memberLogin.Identity, input)
	require.NoError(t, err)
	require.NotNil(t, message.Sender, "group message sender")
	require.Equal(t, memberLogin.Identity.WorkspaceIdentity.ID, message.Sender.SourceID, "group message sender")
	ownerInboxPage, _, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	ownerInbox := ownerInboxPage.Conversations
	require.NoError(t, err)
	for _, item := range ownerInbox {
		if item.ID == group.ID {
			require.Equal(t, 1, item.UnreadCount, "owner unread group = %#v", item)
			require.Nil(t, item.LastReadMessageID, "owner unread group = %#v", item)
		}
	}
	history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 1)
	require.Equal(t, message.ID, history.Messages[0].ID)
	require.NotNil(t, history.Messages[0].Sender)
	require.Equal(t, memberLogin.Identity.WorkspaceIdentity.ID, history.Messages[0].Sender.SourceID)
	groupDetail, err := get.Execute(context.Background(), loggedIn.Identity, group.ID)
	require.NoError(t, err)
	var ownerSubjectID, memberSubjectID string
	for _, participant := range groupDetail.Participants {
		if participant.IdentityID == loggedIn.Identity.WorkspaceIdentity.ID {
			ownerSubjectID = participant.ChatSubjectID
		}
		if participant.IdentityID == memberLogin.Identity.WorkspaceIdentity.ID {
			memberSubjectID = participant.ChatSubjectID
		}
	}
	require.NotEmpty(t, ownerSubjectID, "group member chat subject missing: %#v", groupDetail.Participants)
	require.NotEmpty(t, memberSubjectID, "group member chat subject missing: %#v", groupDetail.Participants)
	relationInput := groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f75",
		Body: "请看前一条", ReplyToMessageID: message.ID, MentionSubjectIDs: []string{memberSubjectID},
	}
	relationMessage, err := send.Execute(context.Background(), loggedIn.Identity, relationInput)
	require.NoError(t, err)
	require.NotNil(t, relationMessage.ReplyTo)
	require.Equal(t, message.ID, relationMessage.ReplyTo.ID)
	require.Len(t, relationMessage.Mentions, 1)
	require.Equal(t, memberSubjectID, relationMessage.Mentions[0].ChatSubjectID)
	memberInboxPage, _, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	memberInbox := memberInboxPage.Conversations
	require.NoError(t, err)
	for _, item := range memberInbox {
		if item.ID == group.ID {
			require.Equal(t, 1, item.UnreadCount, "member unread group = %#v", item)
			require.Equal(t, 1, item.MentionedUnreadCount, "member unread group = %#v", item)
			require.Equal(t, &message.ID, item.LastReadMessageID, "member unread group = %#v", item)
		}
	}
	readState, err := conversationaction.NewMarkConversationReadAction(db).Execute(context.Background(), memberLogin.Identity, group.ID, relationMessage.ID, false)
	require.NoError(t, err)
	require.Equal(t, relationMessage.ID, readState.LastReadMessageID, "marked group read state")
	memberInboxPage, _, err = inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	memberInbox = memberInboxPage.Conversations
	require.NoError(t, err)
	for _, item := range memberInbox {
		if item.ID == group.ID {
			require.Equal(t, 0, item.UnreadCount, "member read group = %#v", item)
			require.Equal(t, 0, item.MentionedUnreadCount, "member read group = %#v", item)
			require.Equal(t, &relationMessage.ID, item.LastReadMessageID, "member read group = %#v", item)
		}
	}
	settings, err = notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, true)
	require.NoError(t, err, "mute group")
	require.True(t, settings.Muted, "mute group")
	beforeAttentionItemsPage, beforeAttentionCounts, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	beforeAttentionItems := beforeAttentionItemsPage.Conversations
	require.NoError(t, err)
	var beforeGroupUnread int
	for _, item := range beforeAttentionItems {
		if item.ID == group.ID {
			beforeGroupUnread = item.UnreadCount
		}
	}
	ordinaryMutedMessage, err := send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f80", Body: "静音后的普通消息",
	})
	require.NoError(t, err)
	mentionAllInput := groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f81", Body: "请所有人查看", MentionSubjectIDs: []string{memberSubjectID}, MentionAll: true,
	}
	mentionAllMessage, err := send.Execute(context.Background(), loggedIn.Identity, mentionAllInput)
	require.NoError(t, err)
	require.True(t, mentionAllMessage.MentionAll, "mention all message = %#v", mentionAllMessage)
	afterAttentionItemsPage, afterAttentionCounts, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	afterAttentionItems := afterAttentionItemsPage.Conversations
	require.NoError(t, err)
	for _, item := range afterAttentionItems {
		if item.ID == group.ID {
			require.True(t, item.Muted, "muted group unread = %#v", item)
			require.Equal(t, beforeGroupUnread+2, item.UnreadCount, "muted group unread = %#v", item)
			require.Equal(t, 1, item.MentionedUnreadCount, "muted group unread = %#v", item)
		}
	}
	require.Equal(t, beforeAttentionCounts.Unread+2, afterAttentionCounts.Unread, "muted group unread count")
	require.Equal(t, beforeAttentionCounts.Attention+1, afterAttentionCounts.Attention, "muted group attention count")
	history, err = conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), memberLogin.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history.Messages), 2)
	require.Equal(t, ordinaryMutedMessage.ID, history.Messages[len(history.Messages)-2].ID)
	require.Equal(t, mentionAllMessage.ID, history.Messages[len(history.Messages)-1].ID)
	require.True(t, history.Messages[len(history.Messages)-1].MentionAll)
	settings, err = notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, false)
	require.NoError(t, err, "unmute group")
	require.False(t, settings.Muted, "unmute group")
	_, afterGroupUnmuteCounts, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Equal(t, afterAttentionCounts.Attention+1, afterGroupUnmuteCounts.Attention, "unmuted group attention count")
	var relationConflict *conversationaction.ConflictError
	_, err = send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f76",
		Body: "无效引用", ReplyToMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f77",
	})
	require.ErrorAs(t, err, &relationConflict, "invalid group reply")
	require.Equal(t, conversationaction.ConflictReasonReplyTargetInvalid, relationConflict.Reason, "invalid group reply")
	_, err = send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f78",
		Body: "无效提醒", MentionSubjectIDs: []string{observerLogin.Identity.WorkspaceIdentity.ID},
	})
	require.ErrorAs(t, err, &relationConflict, "invalid group mention")
	require.Equal(t, groupchataction.ConflictReasonGroupMentionTargetInvalid, relationConflict.Reason, "invalid group mention")
	_, err = send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f79",
		Body: "提醒自己", MentionSubjectIDs: []string{ownerSubjectID},
	})
	require.ErrorAs(t, err, &relationConflict, "self group mention")
	require.Equal(t, groupchataction.ConflictReasonGroupMentionTargetInvalid, relationConflict.Reason, "self group mention")
	history, err = conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 4)
	require.NotNil(t, history.Messages[1].ReplyTo)
	require.Equal(t, message.ID, history.Messages[1].ReplyTo.ID)
	require.Len(t, history.Messages[1].Mentions, 1)
	require.Equal(t, memberSubjectID, history.Messages[1].Mentions[0].ChatSubjectID)
	require.True(t, history.Messages[3].MentionAll)
	_, err = conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), otherInstalled.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross-workspace group history")
	_, err = send.Execute(context.Background(), observerLogin.Identity, groupchataction.GroupTextMessageInput{
		ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f68", Body: "旁观者消息",
	})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "non-participant group send")
	_, err = conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), observerLogin.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "non-participant group history")

	dissolvedGroup, err := create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
		Title: "群聊解散测试", MemberIdentityIDs: []string{memberLogin.Identity.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	remainingGroup, err := groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, newGroupAgentCoordinator(db)).Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationMemberInput{
		ConversationID: dissolvedGroup.ID, MemberIdentityID: memberLogin.Identity.WorkspaceIdentity.ID,
	})
	require.NoError(t, err)
	require.Len(t, remainingGroup.Participants, 1, "remaining group")
	_, err = groupchataction.NewDissolveGroupConversationAction(db, newGroupAgentCoordinator(db)).Execute(context.Background(), loggedIn.Identity, dissolvedGroup.ID)
	require.NoError(t, err)
	itemsPage, _, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	items := itemsPage.Conversations
	require.NoError(t, err)
	foundDissolved := false
	for _, item := range items {
		if item.ID == dissolvedGroup.ID && item.Group != nil && item.Group.Status == domain.ConversationStatusArchived {
			foundDissolved = true
			break
		}
	}
	require.True(t, foundDissolved, "dissolved group missing from inbox: %#v", items)

	addedGroup, err := groupchataction.NewAddGroupConversationMembersAction(db).Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationMembersInput{
		ConversationID: group.ID, MemberIdentityIDs: []string{observerLogin.Identity.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	require.Len(t, addedGroup.Participants, 3, "added group member")
	observerState := &servermodels.ConversationUserState{}
	require.NoError(t, db.NewSelect().Model(observerState).
		Where("workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("conversation_id = ?", group.ID).
		Where("user_id = ?", observerLogin.Identity.User.ID).
		Scan(context.Background()))
	require.NotNil(t, observerState.LastReadMessageID, "added group member read state = %#v", observerState)
	require.NotNil(t, observerState.LastReadAt, "added group member read state = %#v", observerState)
	_, err = conversationaction.NewMarkConversationReadAction(db).Execute(context.Background(), observerLogin.Identity, group.ID, *observerState.LastReadMessageID, false)
	require.NoError(t, err, "mark added group member read")

	_, err = testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, observer.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	_, err = create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
		Title: "停用成员群聊", MemberIdentityIDs: []string{observer.IdentityID},
	})
	require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "inactive group member")
}
