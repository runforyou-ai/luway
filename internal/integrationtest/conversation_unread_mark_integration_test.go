//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/stretchr/testify/require"
)

// TestConversationUnreadMark 验证个人标记、静音和两个阅读水位相互独立。
func TestConversationUnreadMark(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	read := conversationaction.NewMarkConversationReadAction(f.db)
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	mute := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db)
	inbox := inboxaction.NewLoadInboxQuery(f.db)
	first := f.send(t, f.owner, "提及", false, f.subjectID)
	_, err := review.Execute(ctx, f.member, f.groupID, first.ID)
	require.NoError(t, err)
	_, err = read.Execute(ctx, f.member, f.groupID, first.ID, false)
	require.NoError(t, err)
	before := f.state(t)
	for range 2 {
		require.NoError(t, mark.Execute(ctx, f.member, f.groupID, true))
	}
	state := f.state(t)
	require.True(t, state.MarkedUnread)
	require.Equal(t, *before.LastReadMessageID, *state.LastReadMessageID)
	require.True(t, state.LastReadAt.Equal(*before.LastReadAt))
	require.Equal(t, *before.LastReviewedMentionMessageID, *state.LastReviewedMentionMessageID)
	rowsPage, counts, err := inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].MarkedUnread)
	require.Zero(t, counts.Unread)
	require.Equal(t, 1, counts.Attention)
	f.send(t, f.owner, "普通消息", false)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Equal(t, 1, counts.Unread, "mark double counted")
	require.Equal(t, 1, counts.Attention, "mark double counted")
	_, err = mute.Execute(ctx, f.member, f.groupID, true)
	require.NoError(t, err)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Zero(t, counts.Attention, "muted mark")
	last := f.send(t, f.owner, "再次提及", true, f.subjectID)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Equal(t, 2, counts.Unread, "muted mention")
	require.Equal(t, 1, counts.Attention, "muted mention")
	_, err = read.Execute(ctx, f.member, f.groupID, last.ID, false)
	require.NoError(t, err)
	require.True(t, f.state(t).MarkedUnread, "automatic read cleared manual mark")
	_, err = mute.Execute(ctx, f.member, f.groupID, false)
	require.NoError(t, err)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Zero(t, counts.Unread, "manual reminder lost")
	require.Equal(t, 1, counts.Attention, "manual reminder lost")
	require.NoError(t, mark.Execute(ctx, f.member, f.groupID, false))
	state = f.state(t)
	require.False(t, state.MarkedUnread)
	require.Equal(t, last.ID, *state.LastReadMessageID)
	require.Equal(t, first.ID, *state.LastReviewedMentionMessageID)
	_, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	require.NoError(t, err)
	require.Zero(t, counts.Unread, "read inbox")
	require.Zero(t, counts.Attention, "read inbox")
	require.NoError(t, mark.Execute(ctx, f.member, f.groupID, true))
	_, err = read.Execute(ctx, f.member, f.groupID, uuid.NewV7().String(), true)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "invalid read target")
	require.True(t, f.state(t).MarkedUnread, "failed read cleared unread mark")
	_, err = read.Execute(ctx, f.member, f.groupID, last.ID, true)
	require.NoError(t, err)
	require.False(t, f.state(t).MarkedUnread, "explicit read did not clear unread mark at unchanged watermark")

	other := newNavigationFixture(t)
	require.ErrorIs(t, mark.Execute(ctx, other.member, f.groupID, true), conversationaction.ErrConversationNotFound, "cross workspace")
	_, err = f.db.NewUpdate().Table("conversation_participants").Set("left_at = now()").Where("workspace_id = ? AND conversation_id = ? AND subject_id = ?", f.member.Workspace.ID, f.groupID, f.subjectID).Exec(ctx)
	require.NoError(t, err)
	require.ErrorIs(t, mark.Execute(ctx, f.member, f.groupID, true), conversationaction.ErrConversationNotFound, "former member")
}

// TestEmptyConversationUnreadMarksIgnoreListLimit 验证空群标记、跨筛选汇总和列表条数上限。
func TestEmptyConversationUnreadMarksIgnoreListLimit(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	create := groupchataction.NewCreateGroupConversationAction(f.db)
	require.NoError(t, mark.Execute(ctx, f.member, f.groupID, true))
	state := f.state(t)
	require.True(t, state.MarkedUnread)
	require.Nil(t, state.LastReadMessageID)
	require.Nil(t, state.LastReadAt)
	require.Nil(t, state.LastReviewedMentionMessageID)
	require.NoError(t, mark.Execute(ctx, f.member, f.groupID, false))
	for range 51 {
		group, err := create.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "未读标记测试", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
		require.NoError(t, err)
		require.NoError(t, mark.Execute(ctx, f.member, group.ID, true))
	}
	// 批量造数后刷新收件箱相关表的统计信息。
	analyzeInboxTables(ctx, t, f.db)
	inbox := inboxaction.NewLoadInboxQuery(f.db)
	rowsPage, counts, err := inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	rows := rowsPage.Conversations
	require.NoError(t, err)
	require.Len(t, rows, 50)
	require.Zero(t, counts.Unread)
	require.Equal(t, 51, counts.Attention)
	rowsPage, counts, err = inbox.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll})
	rows = rowsPage.Conversations
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Equal(t, 51, counts.Attention)
}
