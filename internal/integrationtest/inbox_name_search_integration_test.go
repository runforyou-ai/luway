//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestInboxNameSearchPagination 验证会话名称搜索的完整分页、检索分组与分页首页一致、改名后的资格变化和游标绑定。
func TestInboxNameSearchPagination(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	createGroup := groupchataction.NewCreateGroupConversationAction(f.db)
	var matched []string
	for index := range 60 {
		group, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: fmt.Sprintf("周报 汇总 %02d", index), MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
		require.NoError(t, err)
		matched = append(matched, group.ID)
	}
	other, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "项目例会", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	// 固定活动时间，第 0 条最新；未匹配的群排在最前，确认搜索不只筛选首页。
	base := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	for index, id := range matched {
		_, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base.Add(-time.Duration(index)*time.Minute)).Where("id = ?", id).Exec(ctx)
		require.NoError(t, err)
	}
	_, err = f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base.Add(time.Hour)).Where("id = ?", other.ID).Exec(ctx)
	require.NoError(t, err)

	// 批量造数后刷新收件箱相关表的统计信息。
	analyzeInboxTables(ctx, t, f.db)

	search := inboxaction.LoadInput{Search: "  周报　 汇总 ", SearchRange: inboxaction.SearchRangeReadable, Limit: 25}
	var ids []string
	var cursor string
	for {
		input := search
		input.Cursor = cursor
		page, _, err := query.Execute(ctx, f.owner, input)
		require.NoError(t, err)
		for _, conversation := range page.Conversations {
			ids = append(ids, conversation.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	require.Equal(t, matched, ids, "搜索分页结果不完整或顺序错误")

	result, err := query.Search(ctx, f.owner, inboxaction.SearchInput{Text: "周报 汇总", Range: inboxaction.SearchRangeReadable})
	require.NoError(t, err)
	var grouped []string
	for _, conversation := range result.Conversations {
		grouped = append(grouped, conversation.ID)
	}
	require.Equal(t, matched[:6], grouped, "检索会话分组应为分页首页前 6 条")

	first, _, err := query.Execute(ctx, f.owner, search)
	require.NoError(t, err)
	for _, changed := range []inboxaction.LoadInput{
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Limit: 25},
		{Scope: domain.InboxScopeChat, Search: "周报 汇总", SearchRange: inboxaction.SearchRangeList, Limit: 25},
		{Scope: domain.InboxScopeChat, Limit: 25},
	} {
		changed.Cursor = first.NextCursor
		_, _, err := query.Execute(ctx, f.owner, changed)
		require.ErrorIs(t, err, inboxaction.ErrCursorInvalid, "搜索条件变化后的游标应被拒绝：%+v", changed)
	}

	// 已加载的会话改名为不匹配后退出，未加载的会话改名为匹配后进入。
	rename := groupchataction.NewUpdateGroupConversationAction(f.db)
	_, err = rename.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: matched[0], Title: "月度复盘"})
	require.NoError(t, err)
	_, err = rename.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: other.ID, Title: "周报 汇总 新增"})
	require.NoError(t, err)
	window, err := query.ReadWindow(ctx, f.owner, inboxaction.ReadWindowInput{Query: search, StartCursor: first.StartCursor, EndCursor: first.EndCursor})
	require.NoError(t, err)
	require.False(t, slices.ContainsFunc(window.Conversations, func(summary inboxaction.ConversationSummary) bool { return summary.ID == matched[0] }), "改名后窗口重读应移除不匹配会话")
	require.True(t, window.HasBefore, "改名后窗口重读应发现新的首部会话")
	rows, err := query.ReadByIDs(ctx, f.owner, []string{matched[0], other.ID}, &search)
	require.NoError(t, err)
	require.False(t, rows[0].MatchesQuery, "按 ID 核对的匹配资格不正确：%+v", rows[0])
	require.True(t, rows[1].MatchesQuery, "按 ID 核对的匹配资格不正确：%+v", rows[1])
	anchor, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: search, AnchorID: matched[0], BeforeLimit: 5, AfterLimit: 5})
	require.NoError(t, err)
	require.False(t, anchor.Anchor.MatchesQuery, "不匹配的锚点应返回查询范围之外")
	head, _, err := query.Execute(ctx, f.owner, search)
	require.NoError(t, err)
	require.Equal(t, other.ID, head.Conversations[0].ID, "改名为匹配的会话应出现在首页")
}

// TestInboxNameSearchRules 验证通配符按字面匹配、搜索范围、客户名称、失权与跨企业隔离及无效输入。
func TestInboxNameSearchRules(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	outsider := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	createGroup := groupchataction.NewCreateGroupConversationAction(f.db)
	percent, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "完成率 100%", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	underscore, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "a_b 协作", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	_, err = createGroup.Execute(ctx, outsider.owner, groupchataction.GroupConversationInput{Title: "完成率 100% 外部", MemberIdentityIDs: []string{outsider.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	// 客户会话没有渠道身份名称时以联系人名称展示。
	_, err = f.db.NewUpdate().TableExpr("contacts AS c").Set("display_name = ?", "名称搜索客户").
		Where("c.workspace_id = ?", f.owner.Workspace.ID).
		Where("c.id = (SELECT ci.contact_id FROM channel_conversations AS cc JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id WHERE cc.conversation_id = ?)", f.conversationID).
		Exec(ctx)
	require.NoError(t, err)
	_, err = f.db.NewUpdate().TableExpr("channel_identities AS ci").Set("display_name = NULL").
		Where("ci.id = (SELECT cc.channel_identity_id FROM channel_conversations AS cc WHERE cc.conversation_id = ?)", f.conversationID).
		Exec(ctx)
	require.NoError(t, err)

	load := func(identity *servermodels.Identity, input inboxaction.LoadInput) []string {
		t.Helper()
		page, _, err := query.Execute(ctx, identity, input)
		require.NoError(t, err, "Execute(%+v)", input)
		ids := []string{}
		for _, conversation := range page.Conversations {
			ids = append(ids, conversation.ID)
		}
		return ids
	}
	readable := func(text string) inboxaction.LoadInput {
		return inboxaction.LoadInput{Search: text, SearchRange: inboxaction.SearchRangeReadable}
	}
	require.Equal(t, []string{percent.ID}, load(f.owner, readable("%")), "%% 应按字面匹配")
	require.Equal(t, []string{underscore.ID}, load(f.owner, readable("_")), "_ 应按字面匹配")
	// 名称与搜索词采用同一规范化规则，全角字符与连续空白的完整名称可以直接搜索。
	fullWidth, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "PR378 ＡＢＣ　 复核", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	for _, text := range []string{"PR378 ＡＢＣ　 复核", "abc 复核"} {
		require.Equal(t, []string{fullWidth.ID}, load(f.owner, readable(text)), "搜索 %q 应命中全角名称", text)
	}
	require.Greater(t, len(load(f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat, Search: "   ", SearchRange: inboxaction.SearchRangeList})), 2, "空白搜索词应按未搜索读取完整列表")

	// 队列中未领取的客户会话属于可读范围、待处理与全部服务会话，不在聊天列表内。
	require.Equal(t, []string{f.conversationID}, load(f.owner, readable("名称搜索客户")), "可读范围应覆盖队列中的客户会话")

	// 没有名称的客户会话按展示的邮箱或带编号的访客名称命中。
	var customer struct {
		ID     string `bun:"id"`
		Number int64  `bun:"number"`
	}
	require.NoError(t, f.db.NewSelect().TableExpr("contacts AS c").ColumnExpr("c.id::text AS id, c.number").
		Where("c.id = (SELECT ci.contact_id FROM channel_conversations AS cc JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id WHERE cc.conversation_id = ?)", f.conversationID).
		Scan(ctx, &customer))
	_, err = f.db.NewUpdate().TableExpr("contacts AS c").Set("display_name = NULL").Where("c.id = ?", customer.ID).Exec(ctx)
	require.NoError(t, err)
	for _, text := range []string{fmt.Sprintf("访客 #%d", customer.Number), fmt.Sprintf("Visitor #%d", customer.Number)} {
		require.Equal(t, []string{f.conversationID}, load(f.owner, readable(text)), "搜索 %q 应按编号命中客户会话", text)
	}
	_, err = contactprofileaction.AddMethod(ctx, f.db, f.owner.Workspace.ID, customer.ID, domain.ContactMethodTypeEmail, "visitor.search@example.com")
	require.NoError(t, err)
	require.Equal(t, []string{f.conversationID}, load(f.owner, readable("visitor.search@")), "没有名称时应按邮箱命中客户会话")
	_, err = f.db.NewUpdate().TableExpr("contacts AS c").Set("display_name = ?", "名称搜索客户").Where("c.id = ?", customer.ID).Exec(ctx)
	require.NoError(t, err)
	listAll := inboxaction.LoadInput{Scope: domain.InboxScopeAll, ServiceStatus: domain.ServiceSessionStatusClosed, Search: "名称搜索客户", SearchRange: inboxaction.SearchRangeList}
	require.Empty(t, load(f.owner, listAll), "列表范围应只在当前筛选内匹配")
	for _, scope := range []domain.InboxScope{domain.InboxScopeAll, domain.InboxScopePending} {
		list := inboxaction.LoadInput{Scope: scope, Search: "名称搜索客户", SearchRange: inboxaction.SearchRangeList}
		require.Equal(t, []string{f.conversationID}, load(f.owner, list), "服务会话列表范围 %s 应命中联系人名称", scope)
	}
	listAll = inboxaction.LoadInput{Scope: domain.InboxScopeChat, Search: "名称搜索客户", SearchRange: inboxaction.SearchRangeList}
	require.Empty(t, load(f.owner, listAll), "聊天列表范围不应命中客户会话")

	// 已退出的群聊不再命中。
	require.NoError(t, groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, percent.ID))
	require.Empty(t, load(f.member, readable("完成率")), "已退出的群聊不应命中")
	require.Equal(t, []string{percent.ID}, load(f.owner, readable("完成率")), "搜索不应跨企业")

	for _, input := range []inboxaction.LoadInput{
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Scope: domain.InboxScopeAll},
		{Search: "周报", SearchRange: inboxaction.SearchRangeList},
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Kinds: []domain.ConversationType{domain.ConversationTypeGroup}},
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Partition: domain.InboxPartitionPinned},
		{Search: "周报", SearchRange: inboxaction.SearchRangeConversation},
	} {
		_, _, err := query.Execute(ctx, f.owner, input)
		require.ErrorIs(t, err, inboxaction.ErrQueryInvalid, "无效搜索输入应被拒绝：%+v", input)
	}
}
