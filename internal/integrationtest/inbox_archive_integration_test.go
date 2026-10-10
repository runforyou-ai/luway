//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// archive 写入指定会话的个人归档状态。
func (f pinFixture) archive(t *testing.T, identity *servermodels.Identity, conversationID string, archived bool) {
	t.Helper()
	require.NoError(t, conversationaction.NewUpdateConversationArchiveAction(f.db).Execute(context.Background(), identity, conversationID, archived), "归档写入失败 %s %v", conversationID, archived)
}

// archived 读取本人已归档聊天的编号顺序。
func (f pinFixture) archived(t *testing.T, identity *servermodels.Identity, input inboxaction.ArchivedInput) []string {
	t.Helper()
	page, err := inboxaction.NewLoadInboxQuery(f.db).ListArchived(context.Background(), identity, input)
	require.NoError(t, err, "读取已归档聊天失败")
	require.GreaterOrEqual(t, page.Page.Total, len(page.Conversations), "已归档总数少于本页条数")
	return arr.Map(page.Conversations, func(conversation inboxaction.ConversationSummary) string { return conversation.ID })
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
	_, err := send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupA, ClientMessageID: uuid.NewV7().String(), Body: "归档前的未读"})
	require.NoError(t, err)
	_, before, err := query.Execute(ctx, f.owner, all)
	require.NoError(t, err)
	f.archive(t, f.owner, f.groupA, true)
	var firstArchivedAt time.Time
	require.NoError(t, f.db.NewSelect().Table("conversation_user_states").Column("archived_at").
		Where("workspace_id = ? AND conversation_id = ? AND user_id = ?", f.owner.Workspace.ID, f.groupA, f.owner.User.ID).
		Scan(ctx, &firstArchivedAt))
	// 重复归档保持原归档时间。
	f.archive(t, f.owner, f.groupA, true)

	got := f.partition(t, f.owner, all)
	require.NotContains(t, got, f.groupA, "聊天列表应不含已归档群")
	require.Len(t, got, 2)
	require.Empty(t, f.partition(t, f.owner, pinned), "置顶区应在归档后取消置顶")
	list, err := query.ListArchived(ctx, f.owner, inboxaction.ArchivedInput{})
	require.NoError(t, err)
	require.Len(t, list.Conversations, 1)
	require.Equal(t, 1, list.Page.Total)
	require.Equal(t, f.groupA, list.Conversations[0].ID)
	require.NotNil(t, list.Conversations[0].ArchivedAt)
	require.False(t, list.Conversations[0].Pinned)
	require.True(t, list.Conversations[0].ArchivedAt.Equal(firstArchivedAt), "重复归档后的归档时间 = %v，want %v", list.Conversations[0].ArchivedAt, firstArchivedAt)
	// 改群名等系统事件不让已归档的聊天回到列表。
	_, err = groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: f.groupA, Title: "改名后的群 A"})
	require.NoError(t, err)
	pinnedPage, after, err := query.Execute(ctx, f.owner, pinned)
	require.NoError(t, err)
	require.Equal(t, version+1, pinnedPage.PinOrderVersion, "置顶顺序版本")
	unread := list.Conversations[0].UnreadCount
	require.NotZero(t, unread)
	require.Equal(t, before.Unread-unread, after.Unread, "归档后未读")
	// 已归档聊天可按名称搜索和类型筛选。
	require.Equal(t, []string{f.groupA}, f.archived(t, f.owner, inboxaction.ArchivedInput{Search: "改名后的群 A"}), "已归档搜索")
	require.Empty(t, f.archived(t, f.owner, inboxaction.ArchivedInput{Kinds: []domain.ConversationType{domain.ConversationTypeDirect}}), "单聊已归档")
	_, err = query.ListArchived(ctx, f.owner, inboxaction.ArchivedInput{Kinds: []domain.ConversationType{domain.ConversationTypeChannel}})
	require.ErrorIs(t, err, inboxaction.ErrQueryInvalid, "客户会话类型")
	// 可读范围搜索仍能找到已归档的聊天。
	readable := inboxaction.LoadInput{Search: "改名后的群 A", SearchRange: inboxaction.SearchRangeReadable}
	require.Equal(t, []string{f.groupA}, f.partition(t, f.owner, readable), "可读范围搜索")
	// 归档只影响本人列表。
	require.Contains(t, f.partition(t, f.member, all), f.groupA, "其他成员的列表应含群 A")

	require.Len(t, f.archived(t, f.owner, inboxaction.ArchivedInput{}), 1, "改群名后的已归档聊天应仍含群 A")

	// 新消息让已归档的聊天回到列表。
	_, err = send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupA, ClientMessageID: uuid.NewV7().String(), Body: "新消息"})
	require.NoError(t, err)
	got = f.partition(t, f.owner, all)
	require.Len(t, got, 3)
	require.Equal(t, f.groupA, got[0], "新消息后群 A 应在最前")
	require.Empty(t, f.archived(t, f.owner, inboxaction.ArchivedInput{}), "新消息后的已归档分区")

	// 取消归档与置顶都让聊天回到列表。
	f.archive(t, f.owner, f.directID, true)
	f.archive(t, f.owner, f.directID, false)
	f.archive(t, f.owner, f.groupB, true)
	pinnedPage, _, err = query.Execute(ctx, f.owner, pinned)
	require.NoError(t, err)
	f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: pinnedPage.PinOrderVersion})
	require.Empty(t, f.archived(t, f.owner, inboxaction.ArchivedInput{}), "取消归档与置顶后的已归档分区")
	require.Len(t, f.partition(t, f.owner, all), 3, "取消归档与置顶后的聊天列表")

	// 解散事件不让已归档的群回到列表。
	f.archive(t, f.owner, f.groupA, true)
	_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupA)
	require.NoError(t, err)
	require.Equal(t, []string{f.groupA}, f.archived(t, f.owner, inboxaction.ArchivedInput{}), "解散后的已归档聊天")
}
