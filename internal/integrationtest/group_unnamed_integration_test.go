//go:build server

package integrationtest

import (
	"context"
	"slices"
	"testing"

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
	extra, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, memberSpec{
		MaxServiceSessions: 10, DisplayName: "阿尔法", Email: uniqueEmail("alpha"), Password: "password123", RoleID: f.owner.User.RoleID,
	})
	if err != nil {
		t.Fatal(err)
	}

	group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{
		Title: "  ", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID, extra.IdentityID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if group.Title != "" || !slices.Equal(group.MemberPreviewNames, []string{"成员", "阿尔法"}) {
		t.Fatalf("未命名群创建结果 = %#v", group)
	}

	// 成员看到的名称排除自己。
	detail, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Title != "" || !slices.Equal(detail.MemberPreviewNames, []string{"群主", "阿尔法"}) {
		t.Fatalf("成员视角的未命名群资料 = %#v", detail)
	}

	query := inboxaction.NewLoadInboxQuery(f.db)
	page, _, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{Search: "阿尔法", SearchRange: inboxaction.SearchRangeReadable, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 1 || page.Conversations[0].ID != group.ID || page.Conversations[0].Group == nil ||
		page.Conversations[0].Group.Title != "" || !slices.Equal(page.Conversations[0].Group.MemberPreviewNames, []string{"成员", "阿尔法"}) {
		t.Fatalf("按成员名称搜索未命名群 = %#v", page.Conversations)
	}
	// 查看者自己的名称不参与匹配。
	page, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Search: "群主", SearchRange: inboxaction.SearchRangeReadable, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(page.Conversations, func(summary inboxaction.ConversationSummary) bool { return summary.ID == group.ID }) {
		t.Fatal("未命名群不应按查看者自己的名称匹配")
	}

	update := groupchataction.NewUpdateGroupConversationAction(f.db)
	if _, err := update.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: group.ID, Title: "季度规划"}); err != nil {
		t.Fatal(err)
	}
	cleared, err := update.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: group.ID, Title: ""})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Title != "" {
		t.Fatalf("清空后的群名称 = %q", cleared.Title)
	}
	var titleIsNull bool
	if err := f.db.NewSelect().Table("conversations").ColumnExpr("title IS NULL").Where("id = ?", group.ID).Scan(ctx, &titleIsNull); err != nil {
		t.Fatal(err)
	}
	if !titleIsNull {
		t.Fatal("清空群名称后应保存为空值")
	}
	var renamed []conversationaction.ConversationSystemEvent
	messages, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages.Messages {
		if message.SystemEvent != nil && message.SystemEvent.Type == domain.ConversationSystemEventGroupRenamed {
			renamed = append(renamed, *message.SystemEvent)
		}
	}
	if len(renamed) != 2 || *renamed[0].PreviousTitle != "" || *renamed[0].Title != "季度规划" ||
		*renamed[1].PreviousTitle != "季度规划" || *renamed[1].Title != "" {
		t.Fatalf("群名称变更事件 = %#v", renamed)
	}
}
