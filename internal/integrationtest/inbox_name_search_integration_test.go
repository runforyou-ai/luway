//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	contactprofileaction "github.com/runforyou-ai/cervi/internal/actions/contactprofile"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
		group, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: fmt.Sprintf("周报 汇总 %02d", index), MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
		if err != nil {
			t.Fatal(err)
		}
		matched = append(matched, group.ID)
	}
	other, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "项目例会", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// 固定活动时间，第 0 条最新；未匹配的群排在最前，确认搜索不只筛选首页。
	base := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	for index, id := range matched {
		if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base.Add(-time.Duration(index)*time.Minute)).Where("id = ?", id).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", base.Add(time.Hour)).Where("id = ?", other.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	search := inboxaction.LoadInput{Search: "  周报　 汇总 ", SearchRange: inboxaction.SearchRangeReadable, Limit: 25}
	var ids []string
	var cursor string
	for {
		input := search
		input.Cursor = cursor
		page, _, err := query.Execute(ctx, f.owner, input)
		if err != nil {
			t.Fatal(err)
		}
		for _, conversation := range page.Conversations {
			ids = append(ids, conversation.ID)
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if !slices.Equal(ids, matched) {
		t.Fatalf("搜索分页结果不完整或顺序错误：得到 %d 条，期望 %d 条", len(ids), len(matched))
	}

	result, err := query.Search(ctx, f.owner, inboxaction.SearchInput{Text: "周报 汇总", Range: inboxaction.SearchRangeReadable})
	if err != nil {
		t.Fatal(err)
	}
	var grouped []string
	for _, conversation := range result.Conversations {
		grouped = append(grouped, conversation.ID)
	}
	if !slices.Equal(grouped, matched[:6]) {
		t.Fatalf("检索会话分组应为分页首页前 6 条：%v", grouped)
	}

	first, _, err := query.Execute(ctx, f.owner, search)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []inboxaction.LoadInput{
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Limit: 25},
		{Scope: domain.InboxScopeChat, Search: "周报 汇总", SearchRange: inboxaction.SearchRangeList, Limit: 25},
		{Scope: domain.InboxScopeChat, Limit: 25},
	} {
		changed.Cursor = first.NextCursor
		if _, _, err := query.Execute(ctx, f.owner, changed); !errors.Is(err, inboxaction.ErrCursorInvalid) {
			t.Fatalf("搜索条件变化后的游标应被拒绝：%+v err=%v", changed, err)
		}
	}

	// 已加载的会话改名为不匹配后退出，未加载的会话改名为匹配后进入。
	rename := groupchataction.NewUpdateGroupConversationAction(f.db)
	if _, err := rename.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: matched[0], Title: "月度复盘"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rename.Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: other.ID, Title: "周报 汇总 新增"}); err != nil {
		t.Fatal(err)
	}
	window, err := query.ReadWindow(ctx, f.owner, inboxaction.ReadWindowInput{Query: search, StartCursor: first.StartCursor, EndCursor: first.EndCursor})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(window.Conversations, func(summary inboxaction.ConversationSummary) bool { return summary.ID == matched[0] }) || !window.HasBefore {
		t.Fatalf("改名后窗口重读应移除不匹配会话并发现新的首部会话：hasBefore=%v", window.HasBefore)
	}
	rows, err := query.ReadByIDs(ctx, f.owner, []string{matched[0], other.ID}, &search)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].MatchesQuery || !rows[1].MatchesQuery {
		t.Fatalf("按 ID 核对的匹配资格不正确：%+v %+v", rows[0], rows[1])
	}
	anchor, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: search, AnchorID: matched[0], BeforeLimit: 5, AfterLimit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if anchor.Anchor.MatchesQuery {
		t.Fatal("不匹配的锚点应返回查询范围之外")
	}
	head, _, err := query.Execute(ctx, f.owner, search)
	if err != nil {
		t.Fatal(err)
	}
	if head.Conversations[0].ID != other.ID {
		t.Fatalf("改名为匹配的会话应出现在首页：%s", head.Conversations[0].ID)
	}
}

// TestInboxNameSearchRules 验证通配符按字面匹配、搜索范围、客户名称、失权与跨企业隔离及无效输入。
func TestInboxNameSearchRules(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	outsider := newNavigationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	createGroup := groupchataction.NewCreateGroupConversationAction(f.db)
	percent, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "完成率 100%", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	underscore, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "a_b 协作", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createGroup.Execute(ctx, outsider.owner, groupchataction.GroupConversationInput{Title: "完成率 100% 外部", MemberIdentityIDs: []string{outsider.member.OrganizationIdentity.ID}}); err != nil {
		t.Fatal(err)
	}
	// 客户会话没有渠道身份名称时以联系人名称展示。
	if _, err := f.db.NewUpdate().TableExpr("contacts AS c").Set("display_name = ?", "名称搜索客户").
		Where("c.organization_id = ?", f.owner.Organization.ID).
		Where("c.id = (SELECT cci.contact_id FROM channel_conversations AS cc JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id WHERE cc.conversation_id = ?)", f.conversationID).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().TableExpr("contact_channel_identities AS cci").Set("display_name = NULL").
		Where("cci.id = (SELECT cc.contact_channel_identity_id FROM channel_conversations AS cc WHERE cc.conversation_id = ?)", f.conversationID).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}

	load := func(identity *servermodels.Identity, input inboxaction.LoadInput) []string {
		t.Helper()
		page, _, err := query.Execute(ctx, identity, input)
		if err != nil {
			t.Fatalf("Execute(%+v) err=%v", input, err)
		}
		ids := []string{}
		for _, conversation := range page.Conversations {
			ids = append(ids, conversation.ID)
		}
		return ids
	}
	readable := func(text string) inboxaction.LoadInput {
		return inboxaction.LoadInput{Search: text, SearchRange: inboxaction.SearchRangeReadable}
	}
	if ids := load(f.owner, readable("%")); !slices.Equal(ids, []string{percent.ID}) {
		t.Fatalf("%% 应按字面匹配：%v", ids)
	}
	if ids := load(f.owner, readable("_")); !slices.Equal(ids, []string{underscore.ID}) {
		t.Fatalf("_ 应按字面匹配：%v", ids)
	}
	// 名称与搜索词采用同一规范化规则，全角字符与连续空白的完整名称可以直接搜索。
	fullWidth, err := createGroup.Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "PR378 ＡＢＣ　 复核", MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"PR378 ＡＢＣ　 复核", "abc 复核"} {
		if ids := load(f.owner, readable(text)); !slices.Equal(ids, []string{fullWidth.ID}) {
			t.Fatalf("搜索 %q 应命中全角名称：%v", text, ids)
		}
	}
	if ids := load(f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat, Search: "   ", SearchRange: inboxaction.SearchRangeList}); len(ids) <= 2 {
		t.Fatalf("空白搜索词应按未搜索读取完整列表：%v", ids)
	}

	// 队列中未领取的客户会话属于可读范围、待处理与全部服务会话，不在聊天列表内。
	if ids := load(f.owner, readable("名称搜索客户")); !slices.Equal(ids, []string{f.conversationID}) {
		t.Fatalf("可读范围应覆盖队列中的客户会话：%v", ids)
	}

	// 没有名称的客户会话按展示的邮箱或带编号的访客名称命中。
	var customer struct {
		ID     string `bun:"id"`
		Number int64  `bun:"number"`
	}
	if err := f.db.NewSelect().TableExpr("contacts AS c").ColumnExpr("c.id::text AS id, c.number").
		Where("c.id = (SELECT cci.contact_id FROM channel_conversations AS cc JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id WHERE cc.conversation_id = ?)", f.conversationID).
		Scan(ctx, &customer); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().TableExpr("contacts AS c").Set("display_name = NULL").Where("c.id = ?", customer.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fmt.Sprintf("访客 #%d", customer.Number), fmt.Sprintf("Visitor #%d", customer.Number)} {
		if ids := load(f.owner, readable(text)); !slices.Equal(ids, []string{f.conversationID}) {
			t.Fatalf("搜索 %q 应按编号命中客户会话：%v", text, ids)
		}
	}
	if _, err := contactprofileaction.AddMethod(ctx, f.db, f.owner.Organization.ID, customer.ID, domain.ContactMethodTypeEmail, "visitor.search@example.com"); err != nil {
		t.Fatal(err)
	}
	if ids := load(f.owner, readable("visitor.search@")); !slices.Equal(ids, []string{f.conversationID}) {
		t.Fatalf("没有名称时应按邮箱命中客户会话：%v", ids)
	}
	if _, err := f.db.NewUpdate().TableExpr("contacts AS c").Set("display_name = ?", "名称搜索客户").Where("c.id = ?", customer.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	listAll := inboxaction.LoadInput{Scope: domain.InboxScopeAll, ServiceStatus: domain.ServiceSessionStatusClosed, Search: "名称搜索客户", SearchRange: inboxaction.SearchRangeList}
	if ids := load(f.owner, listAll); len(ids) != 0 {
		t.Fatalf("列表范围应只在当前筛选内匹配：%v", ids)
	}
	for _, scope := range []domain.InboxScope{domain.InboxScopeAll, domain.InboxScopePending} {
		list := inboxaction.LoadInput{Scope: scope, Search: "名称搜索客户", SearchRange: inboxaction.SearchRangeList}
		if ids := load(f.owner, list); !slices.Equal(ids, []string{f.conversationID}) {
			t.Fatalf("服务会话列表范围 %s 应命中联系人名称：%v", scope, ids)
		}
	}
	listAll = inboxaction.LoadInput{Scope: domain.InboxScopeChat, Search: "名称搜索客户", SearchRange: inboxaction.SearchRangeList}
	if ids := load(f.owner, listAll); len(ids) != 0 {
		t.Fatalf("聊天列表范围不应命中客户会话：%v", ids)
	}

	// 已退出的群聊不再命中。
	if err := groupchataction.NewLeaveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, percent.ID); err != nil {
		t.Fatal(err)
	}
	if ids := load(f.member, readable("完成率")); len(ids) != 0 {
		t.Fatalf("已退出的群聊不应命中：%v", ids)
	}
	if ids := load(f.owner, readable("完成率")); !slices.Equal(ids, []string{percent.ID}) {
		t.Fatalf("搜索不应跨企业：%v", ids)
	}

	for _, input := range []inboxaction.LoadInput{
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Scope: domain.InboxScopeAll},
		{Search: "周报", SearchRange: inboxaction.SearchRangeList},
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Kinds: []domain.ConversationType{domain.ConversationTypeGroup}},
		{Search: "周报", SearchRange: inboxaction.SearchRangeReadable, Partition: domain.InboxPartitionPinned},
		{Search: "周报", SearchRange: inboxaction.SearchRangeConversation},
	} {
		if _, _, err := query.Execute(ctx, f.owner, input); !errors.Is(err, inboxaction.ErrQueryInvalid) {
			t.Fatalf("无效搜索输入应被拒绝：%+v err=%v", input, err)
		}
	}
}
