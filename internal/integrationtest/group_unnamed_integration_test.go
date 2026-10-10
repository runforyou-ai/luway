//go:build server

package integrationtest

import (
	"context"
	"slices"
	"testing"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
)

// TestUnnamedGroupConversation 验证未命名群聊的创建、成员名称摘要、按成员名称搜索和清空群名称。
func TestUnnamedGroupConversation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	extra, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{
		MaxServiceSessions: 10, DisplayName: "阿尔法", Email: servertest.UniqueEmail("alpha"), Password: "password123", RoleID: f.owner.User.RoleID,
	})
	require.NoError(t, err)

	group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{
		Title: "  ", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID, extra.IdentityID},
	})
	require.NoError(t, err)
	require.Empty(t, group.Title)
	require.Equal(t, []string{"成员", "阿尔法"}, group.MemberPreviewNames)

	// 成员看到的名称排除自己。
	detail, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, group.ID)
	require.NoError(t, err)
	require.Empty(t, detail.Title)
	require.Equal(t, []string{"群主", "阿尔法"}, detail.MemberPreviewNames)

	query := inboxaction.NewLoadInboxQuery(f.db)
	page, _, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Search: "阿尔法", SearchRange: inboxaction.SearchRangeReadable, Limit: 25})
	require.NoError(t, err)
	require.Len(t, page.Conversations, 1)
	require.Equal(t, group.ID, page.Conversations[0].ID)
	require.NotNil(t, page.Conversations[0].Group)
	require.Empty(t, page.Conversations[0].Group.Title)
	require.Equal(t, []string{"成员", "阿尔法"}, page.Conversations[0].Group.MemberPreviewNames)
	// 查看者自己的名称不参与匹配。
	page, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Search: "群主", SearchRange: inboxaction.SearchRangeReadable, Limit: 25})
	require.NoError(t, err)
	require.False(t, slices.ContainsFunc(page.Conversations, func(summary inboxaction.ConversationSummary) bool { return summary.ID == group.ID }), "未命名群不应按查看者自己的名称匹配")

	update := groupchataction.NewUpdateGroupConversationAction(f.db)
	_, err = update.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: group.ID, Title: "季度规划"})
	require.NoError(t, err)
	cleared, err := update.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: group.ID, Title: ""})
	require.NoError(t, err)
	require.Empty(t, cleared.Title)
	var titleIsNull bool
	require.NoError(t, f.db.NewSelect().Table("conversations").ColumnExpr("title IS NULL").Where("id = ?", group.ID).Scan(ctx, &titleIsNull))
	require.True(t, titleIsNull, "清空群名称后应保存为空值")
	var renamed []conversationaction.ConversationSystemEvent
	messages, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	require.NoError(t, err)
	for _, message := range messages.Messages {
		if message.SystemEvent != nil && message.SystemEvent.Type == domain.ConversationSystemEventGroupRenamed {
			renamed = append(renamed, *message.SystemEvent)
		}
	}
	require.Len(t, renamed, 2)
	require.Empty(t, *renamed[0].PreviousTitle)
	require.Equal(t, "季度规划", *renamed[0].Title)
	require.Equal(t, "季度规划", *renamed[1].PreviousTitle)
	require.Empty(t, *renamed[1].Title)
}
