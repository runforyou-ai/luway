//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
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
	require.NoError(t, err)
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: second.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "另一渠道客户消息",
	})
	require.NoError(t, err)
	queue := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned}
	both, _, err := query.Execute(ctx, f.owner, queue)
	require.NoError(t, err)
	require.Len(t, both.Conversations, 2)
	for channelID, expected := range map[string]string{f.channelID: f.conversationID, second.ID: other.Conversation.ID} {
		filtered := queue
		filtered.ChannelID = channelID
		page, _, err := query.Execute(ctx, f.owner, filtered)
		require.NoError(t, err)
		require.Equal(t, []string{expected}, conversationIDs(page.Conversations), "channel=%s", channelID)
		// 筛选只决定列表资格，落选会话仍保留阅读资格。
		results, err := query.ReadByIDs(ctx, f.owner, []string{f.conversationID, other.Conversation.ID}, &filtered)
		require.NoError(t, err)
		for _, result := range results {
			require.NotNil(t, result.Conversation, "channel=%s eligibility=%+v", channelID, result)
			require.Equal(t, result.ID == expected, result.MatchesQuery, "channel=%s eligibility=%+v", channelID, result)
		}
	}
	// 领取并关闭一条会话，核对渠道条件在已关闭视图同样生效。
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, other.Conversation.ID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.owner, other.Conversation.ID)
	require.NoError(t, err)
	closed := inboxaction.LoadInput{
		Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.owner.WorkspaceIdentity.ID,
		ServiceStatus: domain.ServiceSessionStatusClosed, ChannelID: second.ID,
	}
	closedPage, _, err := query.Execute(ctx, f.owner, closed)
	require.NoError(t, err)
	require.Equal(t, []string{other.Conversation.ID}, conversationIDs(closedPage.Conversations), "closed by channel")
	closed.ChannelID = f.channelID
	closedPage, _, err = query.Execute(ctx, f.owner, closed)
	require.NoError(t, err)
	require.Empty(t, closedPage.Conversations, "other channel closed")
	// 停用渠道只影响后续接收，历史会话仍按原渠道筛出，候选中保留该渠道。
	_, err = newTestChannelStatusAction(f.db).Execute(ctx, f.owner, second.ID, false)
	require.NoError(t, err)
	closed.ChannelID = second.ID
	closedPage, _, err = query.Execute(ctx, f.owner, closed)
	require.NoError(t, err)
	require.Equal(t, []string{other.Conversation.ID}, conversationIDs(closedPage.Conversations), "disabled channel")
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.owner.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	candidates, err := backend.ListInboxChannels(ctx, appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID})
	require.NoError(t, err)
	require.Len(t, candidates.Channels, 2)
	// 候选按渠道类型的既定顺序和名称排序，停用渠道保留在列表中。
	require.Equal(t, "客服未读测试", candidates.Channels[0].Name)
	require.Equal(t, second.ID, candidates.Channels[1].ID)
	require.False(t, candidates.Channels[1].Enabled)
}

// TestInboxPendingScope 验证待处理条目的类型优先级、等待起点排序、类型与队列筛选、服务对象筛选和待处理总数。
func TestInboxPendingScope(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	// pending 读取指定身份的待处理条目，返回按显示顺序排列的会话编号、条目摘要与待处理总数。
	pending := func(identity *servermodels.Identity, input inboxaction.LoadInput) ([]string, map[string]*inboxaction.PendingSummary, int) {
		t.Helper()
		input.Scope = domain.InboxScopePending
		page, counts, err := query.Execute(ctx, identity, input)
		require.NoError(t, err)
		items := arr.Associate(page.Conversations, func(row inboxaction.ConversationSummary) (string, *inboxaction.PendingSummary) {
			return row.ID, row.Pending
		})
		// 按编号读取时同样返回条目摘要。
		results, err := query.ReadByIDs(ctx, identity, conversationIDs(page.Conversations), &input)
		require.NoError(t, err)
		for _, result := range results {
			require.True(t, result.MatchesQuery)
			require.NotNil(t, result.Conversation.Pending)
			require.Equal(t, items[result.ID].Kind, result.Conversation.Pending.Kind)
			require.True(t, result.Conversation.Pending.Since.Equal(items[result.ID].Since), "read by id pending=%+v list=%+v", result.Conversation.Pending, items[result.ID])
		}
		return conversationIDs(page.Conversations), items, counts.Pending
	}
	// session 读取目标会话当前处理周期。
	session := func(conversationID string) *servermodels.ServiceSession {
		t.Helper()
		current := &servermodels.ServiceSession{}
		require.NoError(t, f.db.NewSelect().Model(current).
			Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.workspace_id = ss.workspace_id").
			Where("svc.conversation_id = ?", conversationID).Scan(ctx))
		return current
	}

	// 公共队列中客户正在等待的周期对所有成员都是待领取，从客户开始等待计起。
	opening := session(f.conversationID)
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		ids, items, count := pending(identity, inboxaction.LoadInput{})
		require.Equal(t, []string{f.conversationID}, ids)
		require.Equal(t, 1, count)
		require.Equal(t, domain.InboxPendingKindQueue, items[f.conversationID].Kind)
		require.True(t, items[f.conversationID].Since.Equal(*opening.AwaitingReplySince), "queue item=%+v", items[f.conversationID])
	}
	// 领取后成为负责人的等我回复，从获得周期的时间计起，同事不再看到。
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	claimed := session(f.conversationID)
	ids, items, _ := pending(f.owner, inboxaction.LoadInput{})
	require.Equal(t, []string{f.conversationID}, ids)
	require.Equal(t, domain.InboxPendingKindReply, items[f.conversationID].Kind)
	require.True(t, items[f.conversationID].Since.Equal(*claimed.AssigneeAssignedAt), "reply item=%+v", items[f.conversationID])
	coworkerIDs, _, coworkerCount := pending(f.member, inboxaction.LoadInput{})
	require.Empty(t, coworkerIDs, "claimed session pending for coworker")
	require.Zero(t, coworkerCount)
	// 内部备注提醒同事后，同事的 @我 从提醒时间计起。
	note, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "帮忙看下",
		Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{f.member.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	ids, items, _ = pending(f.member, inboxaction.LoadInput{})
	require.Equal(t, []string{f.conversationID}, ids)
	require.Equal(t, domain.InboxPendingKindMention, items[f.conversationID].Kind)
	require.True(t, items[f.conversationID].Since.Equal(note.OriginatedAt), "mention item=%+v", items[f.conversationID])
	// 负责人对客回复后客户不再等待，负责人的等我回复结束，同事的 @我 保留。
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "马上处理"})
	require.NoError(t, err)
	ownerIDs, _, ownerCount := pending(f.owner, inboxaction.LoadInput{})
	require.Empty(t, ownerIDs, "answered reply still pending")
	require.Zero(t, ownerCount)
	// 另一条排队会话等待更久时排在前面。
	older, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "另一位客户",
	})
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Table("service_sessions").Set("awaiting_reply_since = ?", note.OriginatedAt.Add(-time.Hour)).
		Where("conversation_id = ?", older.Conversation.ID).Exec(ctx)
	require.NoError(t, err)
	ids, items, count := pending(f.member, inboxaction.LoadInput{})
	require.Equal(t, []string{older.Conversation.ID, f.conversationID}, ids, "waiting order")
	require.Equal(t, 2, count)
	require.Equal(t, domain.InboxPendingKindQueue, items[older.Conversation.ID].Kind)
	// 分页按等待起点续读。
	first, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopePending, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []string{older.Conversation.ID}, conversationIDs(first.Conversations), "first page")
	require.True(t, first.HasMore)
	next, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopePending, Limit: 1, Cursor: first.NextCursor})
	require.NoError(t, err)
	require.Equal(t, []string{f.conversationID}, conversationIDs(next.Conversations), "next page")
	require.False(t, next.HasMore)
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
		require.Equal(t, tc.expected, ids, "filter=%+v", tc.input)
		require.Equal(t, 2, count, "filter=%+v", tc.input)
	}
	// 客户再次来信后负责人重新等我回复，从新的等待起点计起。
	_, err = f.visitorMessage(ctx, "还在吗")
	require.NoError(t, err)
	waiting := session(f.conversationID)
	ids, items, _ = pending(f.owner, inboxaction.LoadInput{PendingKind: domain.InboxPendingKindReply})
	require.Equal(t, []string{f.conversationID}, ids)
	require.True(t, items[f.conversationID].Since.Equal(*waiting.AwaitingReplySince), "reply after new message=%+v", items[f.conversationID])
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
			require.NoError(t, err)
			if index > 0 {
				require.Equal(t, pending, counts.Pending, "input=%+v", input)
				require.Equal(t, unread, counts.PendingUnread, "input=%+v", input)
			}
			pending, unread = counts.Pending, counts.PendingUnread
		}
		return pending, unread
	}

	received, err := f.visitorMessage(ctx, "有人吗")
	require.NoError(t, err)
	pending, unread := counts()
	require.Equal(t, 1, pending, "before read")
	require.NotZero(t, unread, "before read")
	// 阅读到最新消息后待处理仍在，未读消息数清零。
	_, err = conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, received.Message.ID, false)
	require.NoError(t, err)
	pending, unread = counts()
	require.Equal(t, 1, pending, "after read")
	require.Zero(t, unread, "after read")
	// 本人发出的内部备注不计入未读。
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	_, err = send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "我先看看",
		Visibility: domain.MessageVisibilityInternal,
	})
	require.NoError(t, err)
	pending, unread = counts()
	require.Equal(t, 1, pending, "after own note")
	require.Zero(t, unread, "after own note")
	// 客户再次来信后按消息条数计入未读。
	for _, body := range []string{"还在吗", "急"} {
		_, err := f.visitorMessage(ctx, body)
		require.NoError(t, err)
	}
	pending, unread = counts()
	require.Equal(t, 1, pending, "after new message")
	require.Equal(t, 2, unread, "after new message")
}

// TestInboxPendingMentionAlongsideOtherKinds 验证提醒本人独立于条目类型：待领取或等我回复的会话同时被提醒时，行上标明提醒，筛选 @我 时包含该会话。
func TestInboxPendingMentionAlongsideOtherKinds(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	// item 读取指定身份在给定类型筛选下的目标会话条目。
	item := func(identity *servermodels.Identity, kind domain.InboxPendingKind) *inboxaction.PendingSummary {
		t.Helper()
		page, _, err := query.Execute(ctx, identity, inboxaction.LoadInput{Scope: domain.InboxScopePending, PendingKind: kind})
		require.NoError(t, err)
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
		_, err := send.Execute(ctx, author, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "看下",
			Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{target.WorkspaceIdentity.ID},
		})
		require.NoError(t, err)
	}

	// 公共队列中的会话提醒同事：类型仍是待领取，同时标明提醒，按 @我 与待领取都能筛出。
	note(f.owner, f.member)
	queuedMention := item(f.member, "")
	require.NotNil(t, queuedMention)
	require.Equal(t, domain.InboxPendingKindQueue, queuedMention.Kind)
	require.True(t, queuedMention.Mentioned)
	require.NotNil(t, item(f.member, domain.InboxPendingKindMention), "queued mention missing from mention filter")
	require.NotNil(t, item(f.member, domain.InboxPendingKindQueue), "queued mention missing from queue filter")
	// 本人负责且客户等待时被提醒：类型是等我回复，按 @我 同样能筛出。
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	note(f.member, f.owner)
	replyMention := item(f.owner, "")
	require.NotNil(t, replyMention)
	require.Equal(t, domain.InboxPendingKindReply, replyMention.Kind)
	require.True(t, replyMention.Mentioned)
	require.NotNil(t, item(f.owner, domain.InboxPendingKindMention), "reply mention missing from mention filter")
	// 同事回复后提醒不再成立。
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "收到", Visibility: domain.MessageVisibilityInternal,
	})
	require.NoError(t, err)
	answered := item(f.owner, "")
	require.NotNil(t, answered)
	require.False(t, answered.Mentioned)
}

// TestInboxPendingQueueSince 验证待领取从客户开始等待与进入队列的较早者计起，筛选 @我 时从提醒时间计起。
func TestInboxPendingQueueSince(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	// since 读取指定身份在给定类型筛选下目标会话的等待起点。
	since := func(identity *servermodels.Identity, kind domain.InboxPendingKind) *time.Time {
		t.Helper()
		page, _, err := query.Execute(ctx, identity, inboxaction.LoadInput{Scope: domain.InboxScopePending, PendingKind: kind})
		require.NoError(t, err)
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
		require.NoError(t, f.db.NewSelect().Model(current).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx))
		return current
	}
	// 新周期无负责人时从首条消息起计入队列。
	opening := session()
	require.NotNil(t, opening.QueuedAt)
	require.True(t, opening.QueuedAt.Equal(*opening.AwaitingReplySince), "opening queued_at=%v awaiting=%v", opening.QueuedAt, opening.AwaitingReplySince)
	// 回复客户后转回公共队列：客户不再等待，从进入队列计起，不回退到周期开启时间。
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	require.Nil(t, session().QueuedAt, "claimed session keeps queued_at")
	_, err = send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "已处理"})
	require.NoError(t, err)
	coordinator := newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetPublicQueue,
	})
	require.NoError(t, err)
	queued := session()
	require.NotNil(t, queued.QueuedAt)
	require.Nil(t, queued.AwaitingReplySince)
	require.True(t, queued.QueuedAt.After(opening.StatusChangedAt), "returned queued_at=%v opened=%v", queued.QueuedAt, opening.StatusChangedAt)
	got := since(f.member, domain.InboxPendingKindQueue)
	require.NotNil(t, got)
	require.True(t, got.Equal(*queued.QueuedAt), "queue since=%v want %v", got, queued.QueuedAt)
	// 客户随后来信时仍从进入队列计起。
	_, err = f.visitorMessage(ctx, "还有问题")
	require.NoError(t, err)
	got = since(f.member, domain.InboxPendingKindQueue)
	require.NotNil(t, got)
	require.True(t, got.Equal(*queued.QueuedAt), "queue since after customer message=%v want %v", got, queued.QueuedAt)
	// 同时被提醒时，筛选 @我 从提醒时间计起，其余筛选仍按待领取计起。
	note, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "帮忙领一下",
		Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{f.member.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	got = since(f.member, domain.InboxPendingKindMention)
	require.NotNil(t, got)
	require.True(t, got.Equal(note.OriginatedAt), "mention since=%v want %v", got, note.OriginatedAt)
	got = since(f.member, "")
	require.NotNil(t, got)
	require.True(t, got.Equal(*queued.QueuedAt), "unfiltered since=%v want %v", got, queued.QueuedAt)
}
