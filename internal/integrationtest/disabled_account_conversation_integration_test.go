//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// testDisabledAgentConversation 验证禁用 AI 员工后会话保留在列表与未读汇总中、禁止新消息，恢复后允许发送。
func testDisabledAgentConversation(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	first, _ := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	query := inboxaction.NewLoadInboxQuery(db)
	input := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeAgent}}
	unreadMark := conversationaction.NewUpdateConversationUnreadMarkAction(db)
	require.NoError(t, unreadMark.Execute(ctx, identity, first.Conversation.ID, true))
	t.Cleanup(func() {
		_ = unreadMark.Execute(context.Background(), identity, first.Conversation.ID, false)
	})
	_, before, err := query.Execute(ctx, identity, input)
	require.NoError(t, err)
	require.NotZero(t, before.Attention, "unread counts before disable")
	updateStatus := agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db))
	_, err = updateStatus.Execute(ctx, identity, agentID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = updateStatus.Execute(context.Background(), identity, agentID, domain.IdentityStatusActive)
	})
	assertAgentStatus := func(want domain.IdentityStatus) {
		t.Helper()
		page, counts, err := query.Execute(ctx, identity, input)
		require.NoError(t, err)
		require.Equal(t, before, counts)
		listed := false
		for _, row := range page.Conversations {
			if row.ID == first.Conversation.ID {
				listed = row.Agent != nil && row.Agent.AgentStatus == want
			}
		}
		require.True(t, listed, "disabled agent conversation not listed with status %s: %+v", want, page.Conversations)
		results, err := query.ReadByIDs(ctx, identity, []string{first.Conversation.ID}, &input)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.True(t, results[0].MatchesQuery)
		require.NotNil(t, results[0].Conversation)
		require.Equal(t, want, results[0].Conversation.Agent.AgentStatus)
	}
	assertAgentStatus(domain.IdentityStatusInactive)
	send := directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks))
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后发送"})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "send to disabled agent")
	_, err = directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后新建"})
	require.ErrorIs(t, err, conversationaction.ErrAgentTargetNotFound, "start chat with disabled agent")
	_, err = updateStatus.Execute(ctx, identity, agentID, domain.IdentityStatusActive)
	require.NoError(t, err)
	assertAgentStatus(domain.IdentityStatusActive)
	_, err = send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "恢复后发送"})
	require.NoError(t, err, "send after reactivation")
}

// TestDisabledMemberDirectConversation 验证真人成员禁用后对方仍保留单聊与未读汇总、不能发送或发起新消息，恢复后允许发送。
func TestDisabledMemberDirectConversation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	first, err := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用前消息"})
	require.NoError(t, err)
	send := directchataction.NewSendDirectTextMessageAction(f.db, testEnqueuer)
	_, err = send.Execute(ctx, f.member, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用前回复"})
	require.NoError(t, err)
	query := inboxaction.NewLoadInboxQuery(f.db)
	input := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeDirect}}
	_, before, err := query.Execute(ctx, f.owner, input)
	require.NoError(t, err)
	require.NotZero(t, before.Unread, "unread counts before disable")
	updateStatus := testUserStatusAction(f.db)
	_, err = updateStatus.Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	assertPeerStatus := func(want domain.IdentityStatus) {
		t.Helper()
		page, counts, err := query.Execute(ctx, f.owner, input)
		require.NoError(t, err)
		require.Equal(t, before, counts)
		require.Len(t, page.Conversations, 1)
		require.Equal(t, first.Conversation.ID, page.Conversations[0].ID)
		require.NotNil(t, page.Conversations[0].Direct)
		require.Equal(t, want, page.Conversations[0].Direct.PeerStatus)
		results, err := query.ReadByIDs(ctx, f.owner, []string{first.Conversation.ID}, &input)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.True(t, results[0].MatchesQuery)
		require.NotNil(t, results[0].Conversation)
		require.Equal(t, want, results[0].Conversation.Direct.PeerStatus)
	}
	assertPeerStatus(domain.IdentityStatusInactive)
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 2, "history after peer disabled")
	_, err = send.Execute(ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后发送"})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "send to disabled member")
	_, err = directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后发起"})
	require.ErrorIs(t, err, conversationaction.ErrDirectTargetNotFound, "start chat with disabled member")
	_, err = updateStatus.Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusActive)
	require.NoError(t, err)
	assertPeerStatus(domain.IdentityStatusActive)
	_, err = send.Execute(ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "恢复后发送"})
	require.NoError(t, err, "send after reactivation")
}
