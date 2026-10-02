//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// archive 写入指定会话的个人归档状态。
func (f pinFixture) archive(t *testing.T, identity *servermodels.Identity, conversationID string, archived bool) {
	t.Helper()
	if err := conversationaction.NewUpdateConversationArchiveAction(f.db).Execute(context.Background(), identity, conversationID, archived); err != nil {
		t.Fatalf("归档写入失败 %s %v: %v", conversationID, archived, err)
	}
}

// archived 读取本人已归档聊天的编号顺序。
func (f pinFixture) archived(t *testing.T, identity *servermodels.Identity, input inboxaction.ArchivedInput) []string {
	t.Helper()
	page, err := inboxaction.NewLoadInboxQuery(f.db).ListArchived(context.Background(), identity, input)
	if err != nil {
		t.Fatalf("读取已归档聊天失败: %v", err)
	}
	if page.Page.Total < len(page.Conversations) {
		t.Fatalf("已归档总数 %d 少于本页 %d 条", page.Page.Total, len(page.Conversations))
	}
	ids := make([]string, 0, len(page.Conversations))
	for _, conversation := range page.Conversations {
		ids = append(ids, conversation.ID)
	}
	return ids
}

// TestConversationArchive 验证归档移出聊天列表并取消置顶、已归档聊天的读取与搜索、提醒不计入，以及新消息、置顶和取消归档恢复列表。
func TestConversationArchive(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	all := inboxaction.LoadInput{Scope: domain.InboxScopeChat}
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	send := newGroupSendAction(f.db)

	version := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	if _, err := send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupA, ClientMessageID: uuid.NewV7().String(), Body: "归档前的未读"}); err != nil {
		t.Fatal(err)
	}
	_, before, err := query.Execute(ctx, f.owner, all)
	if err != nil {
		t.Fatal(err)
	}
	f.archive(t, f.owner, f.groupA, true)
	var firstArchivedAt time.Time
	if err := f.db.NewSelect().Table("conversation_user_states").Column("archived_at").
		Where("organization_id = ? AND conversation_id = ? AND user_id = ?", f.owner.Organization.ID, f.groupA, f.owner.User.ID).
		Scan(ctx, &firstArchivedAt); err != nil {
		t.Fatal(err)
	}
	// 重复归档保持原归档时间。
	f.archive(t, f.owner, f.groupA, true)

	if got := f.partition(t, f.owner, all); slices.Contains(got, f.groupA) || len(got) != 2 {
		t.Fatalf("聊天列表 = %v，want 不含已归档群", got)
	}
	if got := f.partition(t, f.owner, pinned); len(got) != 0 {
		t.Fatalf("置顶区 = %v，want 归档后取消置顶", got)
	}
	list, err := query.ListArchived(ctx, f.owner, inboxaction.ArchivedInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Conversations) != 1 || list.Page.Total != 1 || list.Conversations[0].ID != f.groupA || list.Conversations[0].ArchivedAt == nil || list.Conversations[0].Pinned {
		t.Fatalf("已归档聊天 = %+v，want 只含未置顶的群 A", list)
	}
	if !list.Conversations[0].ArchivedAt.Equal(firstArchivedAt) {
		t.Fatalf("重复归档后的归档时间 = %v，want %v", list.Conversations[0].ArchivedAt, firstArchivedAt)
	}
	// 改群名等系统事件不让已归档的聊天回到列表。
	if _, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: f.groupA, Title: "改名后的群 A"}); err != nil {
		t.Fatal(err)
	}
	pinnedPage, after, err := query.Execute(ctx, f.owner, pinned)
	if err != nil {
		t.Fatal(err)
	}
	if pinnedPage.PinOrderVersion != version+1 {
		t.Fatalf("置顶顺序版本 = %d，want %d", pinnedPage.PinOrderVersion, version+1)
	}
	if unread := list.Conversations[0].UnreadCount; unread == 0 || after.Unread != before.Unread-unread {
		t.Fatalf("归档后未读 = %d，归档前 %d，群 A 未读 %d", after.Unread, before.Unread, unread)
	}
	// 已归档聊天可按名称搜索和类型筛选。
	if got := f.archived(t, f.owner, inboxaction.ArchivedInput{Search: "改名后的群 A"}); len(got) != 1 || got[0] != f.groupA {
		t.Fatalf("已归档搜索 = %v，want [群 A]", got)
	}
	if got := f.archived(t, f.owner, inboxaction.ArchivedInput{Kinds: []domain.ConversationType{domain.ConversationTypeDirect}}); len(got) != 0 {
		t.Fatalf("单聊已归档 = %v，want 空", got)
	}
	if _, err := query.ListArchived(ctx, f.owner, inboxaction.ArchivedInput{Kinds: []domain.ConversationType{domain.ConversationTypeChannel}}); !errors.Is(err, inboxaction.ErrQueryInvalid) {
		t.Fatalf("客户会话类型 err = %v，want ErrQueryInvalid", err)
	}
	// 可读范围搜索仍能找到已归档的聊天。
	readable := inboxaction.LoadInput{Search: "改名后的群 A", SearchRange: inboxaction.SearchRangeReadable}
	if got := f.partition(t, f.owner, readable); len(got) != 1 || got[0] != f.groupA {
		t.Fatalf("可读范围搜索 = %v，want [群 A]", got)
	}
	// 归档只影响本人列表。
	if got := f.partition(t, f.member, all); !slices.Contains(got, f.groupA) {
		t.Fatalf("其他成员的列表 = %v，want 含群 A", got)
	}

	if got := f.archived(t, f.owner, inboxaction.ArchivedInput{}); len(got) != 1 {
		t.Fatalf("改群名后的已归档聊天 = %v，want 仍含群 A", got)
	}

	// 新消息让已归档的聊天回到列表。
	if _, err := send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupA, ClientMessageID: uuid.NewV7().String(), Body: "新消息"}); err != nil {
		t.Fatal(err)
	}
	if got := f.partition(t, f.owner, all); len(got) != 3 || got[0] != f.groupA {
		t.Fatalf("新消息后的聊天列表 = %v，want 群 A 在最前", got)
	}
	if got := f.archived(t, f.owner, inboxaction.ArchivedInput{}); len(got) != 0 {
		t.Fatalf("新消息后的已归档分区 = %v，want 空", got)
	}

	// 取消归档与置顶都让聊天回到列表。
	f.archive(t, f.owner, f.directID, true)
	f.archive(t, f.owner, f.directID, false)
	f.archive(t, f.owner, f.groupB, true)
	pinnedPage, _, err = query.Execute(ctx, f.owner, pinned)
	if err != nil {
		t.Fatal(err)
	}
	f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: pinnedPage.PinOrderVersion})
	if got := f.archived(t, f.owner, inboxaction.ArchivedInput{}); len(got) != 0 {
		t.Fatalf("取消归档与置顶后的已归档分区 = %v，want 空", got)
	}
	if got := f.partition(t, f.owner, all); len(got) != 3 {
		t.Fatalf("取消归档与置顶后的聊天列表 = %v，want 三条", got)
	}

	// 解散事件不让已归档的群回到列表。
	f.archive(t, f.owner, f.groupA, true)
	if _, err := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupA); err != nil {
		t.Fatal(err)
	}
	if got := f.archived(t, f.owner, inboxaction.ArchivedInput{}); len(got) != 1 || got[0] != f.groupA {
		t.Fatalf("解散后的已归档聊天 = %v，want [群 A]", got)
	}
}

// TestConversationArchiveAgentChat 验证 AI 聊天的归档与新消息恢复。
func TestConversationArchiveAgentChat(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	f := pinFixture{db: db, owner: identity}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		DisplayName: "归档测试助手",
		Execution:   agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "回答问题"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "第一个问题",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.archive(t, identity, first.Conversation.ID, true)
	agentOnly := inboxaction.ArchivedInput{Kinds: []domain.ConversationType{domain.ConversationTypeAgent}}
	if got := f.archived(t, identity, agentOnly); len(got) != 1 || got[0] != first.Conversation.ID {
		t.Fatalf("已归档的 AI 聊天 = %v，want [%s]", got, first.Conversation.ID)
	}
	if got := f.partition(t, identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat}); slices.Contains(got, first.Conversation.ID) {
		t.Fatalf("聊天列表 = %v，want 不含已归档的 AI 聊天", got)
	}
	if _, err := directchataction.NewSendAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.InternalTextMessageInput{
		ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "第二个问题",
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.archived(t, identity, agentOnly); len(got) != 0 {
		t.Fatalf("新消息后的已归档 AI 聊天 = %v，want 空", got)
	}
}
