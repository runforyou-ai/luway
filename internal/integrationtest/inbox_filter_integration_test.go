//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// conversationIDs 返回列表结果的会话编号，用于比对筛选命中范围。
func conversationIDs(summaries []inboxaction.ConversationSummary) []string {
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.ID)
	}
	return ids
}

// TestInboxChannelFilter 验证来源筛选在未分配和已关闭的服务会话中收敛，且停用渠道仍在候选与结果中。
func TestInboxChannelFilter(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	second, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "渠道筛选测试", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: second.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "另一渠道客户消息",
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned}
	both, _, err := query.Execute(ctx, f.owner, queue)
	if err != nil || len(both.Conversations) != 2 {
		t.Fatalf("unfiltered=%v err=%v", conversationIDs(both.Conversations), err)
	}
	for channelID, expected := range map[string]string{f.channelID: f.conversationID, second.ID: other.Conversation.ID} {
		filtered := queue
		filtered.ChannelID = channelID
		page, _, err := query.Execute(ctx, f.owner, filtered)
		if err != nil || !slices.Equal(conversationIDs(page.Conversations), []string{expected}) {
			t.Fatalf("channel=%s got=%v err=%v", channelID, conversationIDs(page.Conversations), err)
		}
		// 筛选只决定列表资格，落选会话仍保留阅读资格。
		results, err := query.ReadByIDs(ctx, f.owner, []string{f.conversationID, other.Conversation.ID}, &filtered)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if result.Conversation == nil || result.MatchesQuery != (result.ID == expected) {
				t.Fatalf("channel=%s eligibility=%+v", channelID, result)
			}
		}
	}
	// 领取并关闭一条会话，核对渠道条件在已关闭视图同样生效。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, other.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.owner, other.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	closed := inboxaction.LoadInput{
		Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.owner.OrganizationIdentity.ID,
		ServiceStatus: domain.ServiceSessionStatusClosed, ChannelID: second.ID,
	}
	closedPage, _, err := query.Execute(ctx, f.owner, closed)
	if err != nil || !slices.Equal(conversationIDs(closedPage.Conversations), []string{other.Conversation.ID}) {
		t.Fatalf("closed by channel=%v err=%v", conversationIDs(closedPage.Conversations), err)
	}
	closed.ChannelID = f.channelID
	closedPage, _, err = query.Execute(ctx, f.owner, closed)
	if err != nil || len(closedPage.Conversations) != 0 {
		t.Fatalf("other channel closed=%v err=%v", conversationIDs(closedPage.Conversations), err)
	}
	// 停用渠道只影响后续接收，历史会话仍按原渠道筛出，候选中保留该渠道。
	if _, err := newTestChannelStatusAction(f.db).Execute(ctx, f.owner, second.ID, false); err != nil {
		t.Fatal(err)
	}
	closed.ChannelID = second.ID
	closedPage, _, err = query.Execute(ctx, f.owner, closed)
	if err != nil || !slices.Equal(conversationIDs(closedPage.Conversations), []string{other.Conversation.ID}) {
		t.Fatalf("disabled channel=%v err=%v", conversationIDs(closedPage.Conversations), err)
	}
	login := loginMember(t, f.db, f.owner.Organization.ID, f.owner.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	candidates, err := backend.ListInboxChannels(ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID})
	if err != nil || len(candidates.Channels) != 2 {
		t.Fatalf("channel candidates=%+v err=%v", candidates.Channels, err)
	}
	// 候选按渠道类型的既定顺序和名称排序，停用渠道保留在列表中。
	if candidates.Channels[0].Name != "客服未读测试" || candidates.Channels[1].ID != second.ID || candidates.Channels[1].Enabled {
		t.Fatalf("channel candidate order=%+v", candidates.Channels)
	}
}

// TestInboxKindFilter 验证聊天范围的会话类型筛选，并拒绝范围外的类型和条件。
func TestInboxKindFilter(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "内部单聊",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		input    inboxaction.LoadInput
		expected []string
	}{
		{"group", inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeGroup}}, []string{f.groupID}},
		{"direct", inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeDirect}}, []string{direct.Conversation.ID}},
		{"internal", inboxaction.LoadInput{Scope: domain.InboxScopeChat}, []string{direct.Conversation.ID, f.groupID}},
		{"two_kinds", inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeDirect, domain.ConversationTypeGroup}}, []string{direct.Conversation.ID, f.groupID}},
		{"service_kinds_ignored", inboxaction.LoadInput{Scope: domain.InboxScopeAll, Kinds: []domain.ConversationType{domain.ConversationTypeGroup}}, []string{f.conversationID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, _, err := query.Execute(ctx, f.owner, tc.input)
			ids := conversationIDs(page.Conversations)
			slices.Sort(ids)
			expected := slices.Clone(tc.expected)
			slices.Sort(expected)
			if err != nil || !slices.Equal(ids, expected) {
				t.Fatalf("got=%v want=%v err=%v", ids, expected, err)
			}
		})
	}
	for _, input := range []inboxaction.LoadInput{
		{},
		{Scope: "customer"},
		{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeChannel}},
		{Scope: domain.InboxScopeAll, ServiceStatus: "archived"},
		{Scope: domain.InboxScopeAll, ChannelID: "bad-id"},
		{Scope: domain.InboxScopeAll, Audience: "vendor"},
		{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity},
		{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned, AssigneeIdentityID: f.member.OrganizationIdentity.ID},
		{Scope: domain.InboxScopePending, PendingKind: "later"},
		{Scope: domain.InboxScopePending, Partition: domain.InboxPartitionPinned},
		{Scope: domain.InboxScopePending, PendingKind: domain.InboxPendingKindQueue, QueueFilter: domain.ServiceQueueFilterTeam},
	} {
		if _, _, err := query.Execute(ctx, f.owner, input); !errors.Is(err, inboxaction.ErrQueryInvalid) {
			t.Fatalf("accepted invalid filter=%+v err=%v", input, err)
		}
	}
	// 按 ID 读取时类型筛选只决定列表资格，落选会话仍可阅读。
	groupOnly := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeGroup}}
	results, err := query.ReadByIDs(ctx, f.owner, []string{f.groupID, direct.Conversation.ID}, &groupOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Conversation == nil || result.MatchesQuery != (result.ID == f.groupID) {
			t.Fatalf("kind eligibility=%+v", result)
		}
	}
	// 勾满聊天范围全部类型等同不限类型，游标身份也一致。
	full, _, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{
		Scope: domain.InboxScopeChat,
		Kinds: []domain.ConversationType{domain.ConversationTypeDirect, domain.ConversationTypeGroup, domain.ConversationTypeAgent},
	})
	unlimited, _, err2 := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || err2 != nil || !slices.Equal(conversationIDs(full.Conversations), conversationIDs(unlimited.Conversations)) || full.EndCursor != unlimited.EndCursor {
		t.Fatalf("full=%v unlimited=%v err=%v %v", conversationIDs(full.Conversations), conversationIDs(unlimited.Conversations), err, err2)
	}
	// 服务会话条件在聊天范围一律按空条件读取。
	carried, _, err := query.Execute(ctx, f.owner, inboxaction.LoadInput{
		Scope: domain.InboxScopeChat, PendingKind: domain.InboxPendingKindReply, AssigneeFilter: domain.InboxAssigneeFilterIdentity,
		AssigneeIdentityID: f.member.OrganizationIdentity.ID, ChannelID: f.channelID, Audience: domain.ServiceAudienceEmployee, ServiceStatus: domain.ServiceSessionStatusClosed,
	})
	plain, _, err2 := query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
	if err != nil || err2 != nil || !slices.Equal(conversationIDs(carried.Conversations), conversationIDs(plain.Conversations)) {
		t.Fatalf("carried=%v plain=%v err=%v %v", conversationIDs(carried.Conversations), conversationIDs(plain.Conversations), err, err2)
	}
}

// TestInboxPendingScope 验证待处理条目的类型优先级、等待起点排序、类型与队列筛选、服务对象筛选和待处理总数。
func TestInboxPendingScope(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	// pending 读取指定身份的待处理条目，返回按显示顺序排列的会话编号、条目摘要与待处理总数。
	pending := func(identity *servermodels.Identity, input inboxaction.LoadInput) ([]string, map[string]*inboxaction.PendingSummary, int) {
		t.Helper()
		input.Scope = domain.InboxScopePending
		page, counts, err := query.Execute(ctx, identity, input)
		if err != nil {
			t.Fatal(err)
		}
		items := make(map[string]*inboxaction.PendingSummary, len(page.Conversations))
		for _, row := range page.Conversations {
			items[row.ID] = row.Pending
		}
		// 按编号读取时同样返回条目摘要。
		results, err := query.ReadByIDs(ctx, identity, conversationIDs(page.Conversations), &input)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range results {
			if !result.MatchesQuery || result.Conversation.Pending == nil || result.Conversation.Pending.Kind != items[result.ID].Kind || !result.Conversation.Pending.Since.Equal(items[result.ID].Since) {
				t.Fatalf("read by id pending=%+v list=%+v", result.Conversation.Pending, items[result.ID])
			}
		}
		return conversationIDs(page.Conversations), items, counts.Pending
	}
	// session 读取目标会话当前处理周期。
	session := func(conversationID string) *servermodels.ServiceSession {
		t.Helper()
		current := &servermodels.ServiceSession{}
		if err := f.db.NewSelect().Model(current).
			Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.organization_id = ss.organization_id").
			Where("svc.conversation_id = ?", conversationID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return current
	}

	// 公共队列中客户正在等待的周期对所有成员都是待领取，从客户开始等待计起。
	opening := session(f.conversationID)
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		ids, items, count := pending(identity, inboxaction.LoadInput{})
		if !slices.Equal(ids, []string{f.conversationID}) || count != 1 || items[f.conversationID].Kind != domain.InboxPendingKindQueue || !items[f.conversationID].Since.Equal(*opening.AwaitingReplySince) {
			t.Fatalf("queue items=%v %+v count=%d", ids, items[f.conversationID], count)
		}
	}
	// 领取后成为负责人的等我回复，从获得周期的时间计起，同事不再看到。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	claimed := session(f.conversationID)
	ids, items, _ := pending(f.owner, inboxaction.LoadInput{})
	if !slices.Equal(ids, []string{f.conversationID}) || items[f.conversationID].Kind != domain.InboxPendingKindReply || !items[f.conversationID].Since.Equal(*claimed.AssigneeAssignedAt) {
		t.Fatalf("reply items=%v %+v", ids, items[f.conversationID])
	}
	if ids, _, count := pending(f.member, inboxaction.LoadInput{}); len(ids) != 0 || count != 0 {
		t.Fatalf("claimed session pending for coworker=%v count=%d", ids, count)
	}
	// 内部备注提醒同事后，同事的 @我 从提醒时间计起。
	note, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "帮忙看下",
		Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{f.member.OrganizationIdentity.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids, items, _ = pending(f.member, inboxaction.LoadInput{})
	if !slices.Equal(ids, []string{f.conversationID}) || items[f.conversationID].Kind != domain.InboxPendingKindMention || !items[f.conversationID].Since.Equal(note.OriginatedAt) {
		t.Fatalf("mention items=%v %+v", ids, items[f.conversationID])
	}
	// 负责人对客回复后客户不再等待，负责人的等我回复结束，同事的 @我 保留。
	if _, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "马上处理"}); err != nil {
		t.Fatal(err)
	}
	if ids, _, count := pending(f.owner, inboxaction.LoadInput{}); len(ids) != 0 || count != 0 {
		t.Fatalf("answered reply still pending=%v count=%d", ids, count)
	}
	// 另一条排队会话等待更久时排在前面。
	older, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "另一位客户",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("service_sessions").Set("awaiting_reply_since = ?", note.OriginatedAt.Add(-time.Hour)).
		Where("conversation_id = ?", older.Conversation.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ids, items, count := pending(f.member, inboxaction.LoadInput{})
	if !slices.Equal(ids, []string{older.Conversation.ID, f.conversationID}) || count != 2 || items[older.Conversation.ID].Kind != domain.InboxPendingKindQueue {
		t.Fatalf("waiting order=%v count=%d", ids, count)
	}
	// 分页按等待起点续读。
	first, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopePending, Limit: 1})
	if err != nil || !slices.Equal(conversationIDs(first.Conversations), []string{older.Conversation.ID}) || !first.HasMore {
		t.Fatalf("first page=%v err=%v", conversationIDs(first.Conversations), err)
	}
	next, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopePending, Limit: 1, Cursor: first.NextCursor})
	if err != nil || !slices.Equal(conversationIDs(next.Conversations), []string{f.conversationID}) || next.HasMore {
		t.Fatalf("next page=%v err=%v", conversationIDs(next.Conversations), err)
	}
	// 类型、队列与服务对象筛选只收窄列表，待处理总数保持不变。
	for _, tc := range []struct {
		input    inboxaction.LoadInput
		expected []string
	}{
		{inboxaction.LoadInput{PendingKind: domain.InboxPendingKindMention}, []string{f.conversationID}},
		{inboxaction.LoadInput{PendingKind: domain.InboxPendingKindQueue}, []string{older.Conversation.ID}},
		{inboxaction.LoadInput{PendingKind: domain.InboxPendingKindQueue, QueueFilter: domain.ServiceQueueFilterPublic}, []string{older.Conversation.ID}},
		{inboxaction.LoadInput{PendingKind: domain.InboxPendingKindQueue, QueueFilter: domain.ServiceQueueFilterTeam, QueueTeamID: uuid.NewV7().String()}, []string{}},
		{inboxaction.LoadInput{PendingKind: domain.InboxPendingKindReply}, []string{}},
		{inboxaction.LoadInput{Audience: domain.ServiceAudienceCustomer}, []string{older.Conversation.ID, f.conversationID}},
		{inboxaction.LoadInput{Audience: domain.ServiceAudienceEmployee}, []string{}},
	} {
		ids, _, count := pending(f.member, tc.input)
		if !slices.Equal(ids, tc.expected) || count != 2 {
			t.Fatalf("filter=%+v ids=%v count=%d", tc.input, ids, count)
		}
	}
	// 客户再次来信后负责人重新等我回复，从新的等待起点计起。
	if _, err := f.visitorMessage(ctx, "还在吗"); err != nil {
		t.Fatal(err)
	}
	waiting := session(f.conversationID)
	ids, items, _ = pending(f.owner, inboxaction.LoadInput{PendingKind: domain.InboxPendingKindReply})
	if !slices.Equal(ids, []string{f.conversationID}) || !items[f.conversationID].Since.Equal(*waiting.AwaitingReplySince) {
		t.Fatalf("reply after new message=%v %+v", ids, items[f.conversationID])
	}
}

// TestInboxPendingUnreadCount 验证待处理总数只随处理变化，待处理会话中的未读消息总数随本人阅读清零、按他人新消息逐条增加，本人发言不计入，且不受列表范围与筛选影响。
func TestInboxPendingUnreadCount(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	// counts 按各列表范围与筛选读取成员的待处理总数与待处理会话中的未读消息总数，各次读取结果一致时返回。
	counts := func() (int, int) {
		t.Helper()
		var pending, unread int
		for index, input := range []inboxaction.LoadInput{
			{Scope: domain.InboxScopePending},
			{Scope: domain.InboxScopePending, ChannelID: uuid.NewV7().String()},
			{Scope: domain.InboxScopeChat},
		} {
			_, counts, err := query.Execute(ctx, f.member, input)
			if err != nil {
				t.Fatal(err)
			}
			if index > 0 && (counts.Pending != pending || counts.PendingUnread != unread) {
				t.Fatalf("input=%+v pending=%d unread=%d want %d %d", input, counts.Pending, counts.PendingUnread, pending, unread)
			}
			pending, unread = counts.Pending, counts.PendingUnread
		}
		return pending, unread
	}

	received, err := f.visitorMessage(ctx, "有人吗")
	if err != nil {
		t.Fatal(err)
	}
	if pending, unread := counts(); pending != 1 || unread == 0 {
		t.Fatalf("before read pending=%d unread=%d", pending, unread)
	}
	// 阅读到最新消息后待处理仍在，未读消息数清零。
	if _, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, received.Message.ID, false); err != nil {
		t.Fatal(err)
	}
	if pending, unread := counts(); pending != 1 || unread != 0 {
		t.Fatalf("after read pending=%d unread=%d", pending, unread)
	}
	// 本人发出的内部备注不计入未读。
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	if _, err := send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "我先看看",
		Visibility: domain.MessageVisibilityInternal,
	}); err != nil {
		t.Fatal(err)
	}
	if pending, unread := counts(); pending != 1 || unread != 0 {
		t.Fatalf("after own note pending=%d unread=%d", pending, unread)
	}
	// 客户再次来信后按消息条数计入未读。
	for _, body := range []string{"还在吗", "急"} {
		if _, err := f.visitorMessage(ctx, body); err != nil {
			t.Fatal(err)
		}
	}
	if pending, unread := counts(); pending != 1 || unread != 2 {
		t.Fatalf("after new message pending=%d unread=%d", pending, unread)
	}
}

// TestInboxPendingMentionAlongsideOtherKinds 验证提醒本人独立于条目类型：待领取或等我回复的会话同时被提醒时，行上标明提醒，筛选 @我 时包含该会话。
func TestInboxPendingMentionAlongsideOtherKinds(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	// item 读取指定身份在给定类型筛选下的目标会话条目。
	item := func(identity *servermodels.Identity, kind domain.InboxPendingKind) *inboxaction.PendingSummary {
		t.Helper()
		page, _, err := query.Execute(ctx, identity, inboxaction.LoadInput{Scope: domain.InboxScopePending, PendingKind: kind})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Conversations {
			if row.ID == f.conversationID {
				return row.Pending
			}
		}
		return nil
	}
	// note 以指定身份写入提醒另一方的内部备注。
	note := func(author, target *servermodels.Identity) {
		t.Helper()
		if _, err := send.Execute(ctx, author, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "看下",
			Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{target.OrganizationIdentity.ID},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 公共队列中的会话提醒同事：类型仍是待领取，同时标明提醒，按 @我 与待领取都能筛出。
	note(f.owner, f.member)
	if pending := item(f.member, ""); pending == nil || pending.Kind != domain.InboxPendingKindQueue || !pending.Mentioned {
		t.Fatalf("queued mention=%+v", pending)
	}
	if item(f.member, domain.InboxPendingKindMention) == nil || item(f.member, domain.InboxPendingKindQueue) == nil {
		t.Fatal("queued mention missing from mention or queue filter")
	}
	// 本人负责且客户等待时被提醒：类型是等我回复，按 @我 同样能筛出。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	note(f.member, f.owner)
	if pending := item(f.owner, ""); pending == nil || pending.Kind != domain.InboxPendingKindReply || !pending.Mentioned {
		t.Fatalf("reply mention=%+v", pending)
	}
	if item(f.owner, domain.InboxPendingKindMention) == nil {
		t.Fatal("reply mention missing from mention filter")
	}
	// 同事回复后提醒不再成立。
	if _, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到", Visibility: domain.MessageVisibilityInternal,
	}); err != nil {
		t.Fatal(err)
	}
	if pending := item(f.owner, ""); pending == nil || pending.Mentioned {
		t.Fatalf("answered mention=%+v", pending)
	}
}

// TestInboxPendingQueueSince 验证待领取从客户开始等待与进入队列的较早者计起，筛选 @我 时从提醒时间计起。
func TestInboxPendingQueueSince(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	// since 读取指定身份在给定类型筛选下目标会话的等待起点。
	since := func(identity *servermodels.Identity, kind domain.InboxPendingKind) *time.Time {
		t.Helper()
		page, _, err := query.Execute(ctx, identity, inboxaction.LoadInput{Scope: domain.InboxScopePending, PendingKind: kind})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Conversations {
			if row.ID == f.conversationID && row.Pending != nil {
				return &row.Pending.Since
			}
		}
		return nil
	}
	// session 读取目标会话当前处理周期。
	session := func() *servermodels.ServiceSession {
		t.Helper()
		current := &servermodels.ServiceSession{}
		if err := f.db.NewSelect().Model(current).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return current
	}
	// 新周期无负责人时从首条消息起计入队列。
	opening := session()
	if opening.QueuedAt == nil || !opening.QueuedAt.Equal(*opening.AwaitingReplySince) {
		t.Fatalf("opening queued_at=%v awaiting=%v", opening.QueuedAt, opening.AwaitingReplySince)
	}
	// 回复客户后转回公共队列：客户不再等待，从进入队列计起，不回退到周期开启时间。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if claimed := session(); claimed.QueuedAt != nil {
		t.Fatalf("claimed session keeps queued_at=%v", claimed.QueuedAt)
	}
	if _, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "已处理"}); err != nil {
		t.Fatal(err)
	}
	coordinator := agentrunaction.NewExecuteAction(f.db, nil, nil, testAttachmentReader(f.db), nil, nil)
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db)).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetPublicQueue,
	}); err != nil {
		t.Fatal(err)
	}
	queued := session()
	if queued.QueuedAt == nil || queued.AwaitingReplySince != nil || !queued.QueuedAt.After(opening.StatusChangedAt) {
		t.Fatalf("returned queued_at=%v awaiting=%v opened=%v", queued.QueuedAt, queued.AwaitingReplySince, opening.StatusChangedAt)
	}
	if got := since(f.member, domain.InboxPendingKindQueue); got == nil || !got.Equal(*queued.QueuedAt) {
		t.Fatalf("queue since=%v want %v", got, queued.QueuedAt)
	}
	// 客户随后来信时仍从进入队列计起。
	if _, err := f.visitorMessage(ctx, "还有问题"); err != nil {
		t.Fatal(err)
	}
	if got := since(f.member, domain.InboxPendingKindQueue); got == nil || !got.Equal(*queued.QueuedAt) {
		t.Fatalf("queue since after customer message=%v want %v", got, queued.QueuedAt)
	}
	// 同时被提醒时，筛选 @我 从提醒时间计起，其余筛选仍按待领取计起。
	note, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "帮忙领一下",
		Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{f.member.OrganizationIdentity.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := since(f.member, domain.InboxPendingKindMention); got == nil || !got.Equal(note.OriginatedAt) {
		t.Fatalf("mention since=%v want %v", got, note.OriginatedAt)
	}
	if got := since(f.member, ""); got == nil || !got.Equal(*queued.QueuedAt) {
		t.Fatalf("unfiltered since=%v want %v", got, queued.QueuedAt)
	}
}
