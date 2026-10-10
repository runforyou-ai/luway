//go:build server

package integrationtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"

	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// assertInboxWindowIDs 核对窗口顺序、位置游标及非空切片。
func assertInboxWindowIDs(t *testing.T, actual []appservice.InboxConversation, expected []appservice.InboxConversation) {
	t.Helper()
	require.NotNil(t, actual)
	require.Len(t, actual, len(expected), "window IDs differ: actual=%v expected=%v", actual, expected)
	for index := range expected {
		require.Equal(t, expected[index].ID, actual[index].ID, "window IDs differ: actual=%v expected=%v", actual, expected)
	}
	for _, row := range actual {
		require.NotEmpty(t, row.PositionCursor, "missing position cursor for %s", row.ID)
	}
}

// TestInboxContextDeepWindow 验证第二百条定位、前后翻页、完整范围重读和原位置恢复。
func TestInboxContextDeepWindow(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	for index := range 219 {
		group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: fmt.Sprintf("锚点群 %d", index), MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
		require.NoError(t, err)
		// 同时覆盖微秒、并列时间和没有消息的空时间区。
		if index%7 != 0 {
			activity := time.Date(2026, 9, 9, 0, 0, 0, 123456000, time.UTC).Add(time.Duration(index/3) * time.Microsecond)
			_, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", activity).Where("id = ?", group.ID).Exec(ctx)
			require.NoError(t, err)
		}
	}
	// 批量造数后刷新收件箱相关表的统计信息。
	analyzeInboxTables(ctx, t, f.db)
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.memberEmail, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}
	filter := appservice.InboxQuery{Scope: domain.InboxScopeChat}
	all, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, Limit: 300})
	require.NoError(t, err)
	require.Len(t, all.Conversations, 220)
	require.False(t, all.HasMore)
	require.False(t, all.HasBefore)
	for _, index := range []int{0, 100, 199, 219} {
		anchor := all.Conversations[index]
		located, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: anchor.ID, BeforeLimit: 3, AfterLimit: 4})
		require.NoError(t, err, "locate %d", index)
		require.Equal(t, appservice.InboxConversationMatching, located.Anchor.Availability, "locate %d", index)
		require.NotNil(t, located.Anchor.Conversation, "locate %d", index)
		assertInboxWindowIDs(t, located.Window.Conversations, all.Conversations[max(0, index-3):min(220, index+5)])
		require.Equal(t, index > 3, located.Window.HasBefore, "wrong neighbor flags at %d", index)
		require.Equal(t, index+5 < 220, located.Window.HasAfter, "wrong neighbor flags at %d", index)
	}

	// 从尾部跨越空时间区向前遍历，结果应与一次完整排序相同。
	cursor := all.EndCursor
	actual := []appservice.InboxConversation{all.Conversations[219]}
	for attempts := 0; ; attempts++ {
		require.LessOrEqual(t, attempts, 30, "backward pagination did not terminate")
		page, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, BeforeCursor: cursor, Limit: 13})
		require.NoError(t, err)
		require.True(t, page.HasMore, "previous page=%+v", page)
		actual = append(page.Conversations, actual...)
		if !page.HasBefore {
			break
		}
		cursor = page.StartCursor
	}
	assertInboxWindowIDs(t, actual, all.Conversations)

	windowInput := appservice.InboxWindowInput{Query: filter, StartCursor: all.Conversations[50].PositionCursor, EndCursor: all.Conversations[209].PositionCursor}
	window, err := backend.ReadInboxWindow(ctx, meta, windowInput)
	require.NoError(t, err)
	require.True(t, window.HasBefore)
	require.True(t, window.HasAfter)
	require.Equal(t, windowInput.StartCursor, window.StartCursor)
	require.Equal(t, windowInput.EndCursor, window.EndCursor)
	assertInboxWindowIDs(t, window.Conversations, all.Conversations[50:210])

	anchor := all.Conversations[199]
	request := appservice.InboxContextInput{Query: filter, AnchorID: anchor.ID, AnchorCursor: anchor.PositionCursor, BeforeLimit: 3, AfterLimit: 4}
	_, err = newGroupSendAction(f.db).Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: anchor.ID, ClientMessageID: uuid.NewV7().String(), Body: "锚点上浮"})
	require.NoError(t, err)
	moved, err := backend.GetInboxContext(ctx, meta, request)
	require.NoError(t, err)
	require.Equal(t, appservice.InboxConversationMatching, moved.Anchor.Availability)
	require.NotEqual(t, anchor.PositionCursor, moved.Anchor.Conversation.PositionCursor)
	expected := append(slices.Clone(all.Conversations[196:199]), all.Conversations[200:204]...)
	assertInboxWindowIDs(t, moved.Window.Conversations, expected)
	current, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: anchor.ID, BeforeLimit: 3, AfterLimit: 4})
	require.NoError(t, err)
	require.False(t, current.Window.HasBefore)
	require.Equal(t, anchor.ID, current.Window.Conversations[0].ID)

	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: anchor.ID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	removed, err := backend.GetInboxContext(ctx, meta, request)
	require.NoError(t, err)
	require.Equal(t, appservice.InboxConversationUnavailable, removed.Anchor.Availability)
	require.Nil(t, removed.Anchor.Conversation)
	assertInboxWindowIDs(t, removed.Window.Conversations, expected)
	// 删除边界会话后按游标中的原始边界恢复窗口。
	_, err = f.db.NewDelete().Model((*servermodels.Conversation)(nil)).Where("id = ?", anchor.ID).Exec(ctx)
	require.NoError(t, err)
	deleted, err := backend.GetInboxContext(ctx, meta, request)
	require.NoError(t, err)
	require.Nil(t, deleted.Anchor.Conversation)
	assertInboxWindowIDs(t, deleted.Window.Conversations, expected)
	refreshed, err := backend.ReadInboxWindow(ctx, meta, windowInput)
	require.NoError(t, err)
	expectedRange := append(slices.Clone(all.Conversations[50:199]), all.Conversations[200:210]...)
	assertInboxWindowIDs(t, refreshed.Conversations, expectedRange)
	// 两侧边界删除后保留原闭区间，并能从原值向两侧继续读取。
	_, err = f.db.NewDelete().Model((*servermodels.Conversation)(nil)).Where("id IN (?)", bun.List([]string{all.Conversations[50].ID, all.Conversations[209].ID})).Exec(ctx)
	require.NoError(t, err)
	refreshed, err = backend.ReadInboxWindow(ctx, meta, windowInput)
	require.NoError(t, err)
	require.Equal(t, windowInput.StartCursor, refreshed.StartCursor)
	require.Equal(t, windowInput.EndCursor, refreshed.EndCursor)
	assertInboxWindowIDs(t, refreshed.Conversations, expectedRange[1:len(expectedRange)-1])
	previous, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, BeforeCursor: refreshed.StartCursor, Limit: 3})
	require.NoError(t, err)
	assertInboxWindowIDs(t, previous.Conversations, all.Conversations[47:50])
	next, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, Cursor: refreshed.EndCursor, Limit: 3})
	require.NoError(t, err)
	assertInboxWindowIDs(t, next.Conversations, all.Conversations[210:213])
}

// TestInboxContextFilters 验证聊天、待处理与全部服务会话沿用同一资格、排序和默认窗口大小。
func TestInboxContextFilters(t *testing.T) {
	t.Parallel()
	f := newInboxPaginationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	filters := []inboxaction.LoadInput{{Scope: domain.InboxScopeChat}, {Scope: domain.InboxScopePending}, {Scope: domain.InboxScopeAll}}
	for _, filter := range customerInboxFilters(f.owner.WorkspaceIdentity.ID, f.member.WorkspaceIdentity.ID) {
		filters = append(filters, filter.input)
	}
	for _, filter := range filters {
		filter.Limit = 300
		page, _, err := query.Execute(ctx, f.owner, filter)
		require.NoError(t, err, "filter=%+v", filter)
		require.NotEmpty(t, page.Conversations, "filter=%+v", filter)
		index := len(page.Conversations) / 2
		located, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: page.Conversations[index].ID})
		require.NoError(t, err, "filter=%+v", filter)
		require.True(t, located.Anchor.MatchesQuery, "filter=%+v", filter)
		require.Equal(t, conversationIDs(page.Conversations[max(0, index-25):min(len(page.Conversations), index+26)]), conversationIDs(located.Window.Conversations), "filter=%+v", filter)
		// 每类会话都能够独立定位，窗口内所有摘要仍符合当前筛选。
		for _, row := range arr.UniqueBy(page.Conversations, func(row inboxaction.ConversationSummary) domain.ConversationType { return row.Type }) {
			located, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: row.ID, BeforeLimit: 1, AfterLimit: 1})
			require.NoError(t, err, "type=%s", row.Type)
			require.True(t, located.Anchor.MatchesQuery, "type=%s", row.Type)
			require.Equal(t, row.Type, located.Anchor.Conversation.Type)
		}
	}
}

// TestInboxContextUnavailable 验证缺失原位置、退出筛选、跨企业、空窗口和非法边界。
func TestInboxContextUnavailable(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.memberEmail, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}
	filter := appservice.InboxQuery{Scope: domain.InboxScopeChat}
	located, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: f.groupID})
	require.NoError(t, err)
	require.Len(t, located.Window.Conversations, 1)
	require.Nil(t, located.Window.Conversations[0].LastActivityAt)
	oldCursor := located.Anchor.Conversation.PositionCursor
	foreign := newNavigationFixture(t)
	for _, id := range []string{uuid.NewV7().String(), foreign.groupID} {
		unavailable, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: id})
		require.NoError(t, err)
		require.Equal(t, appservice.InboxConversationUnavailable, unavailable.Anchor.Availability)
		require.Nil(t, unavailable.Anchor.Conversation)
		require.Empty(t, unavailable.Window.StartCursor)
		assertInboxWindowIDs(t, unavailable.Window.Conversations, nil)
	}
	outside, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: appservice.InboxQuery{Scope: domain.InboxScopeAll}, AnchorID: f.groupID})
	require.NoError(t, err)
	require.Equal(t, appservice.InboxConversationOutsideQuery, outside.Anchor.Availability)
	require.NotNil(t, outside.Anchor.Conversation)
	require.Empty(t, outside.Anchor.Conversation.PositionCursor)
	assertInboxWindowIDs(t, outside.Window.Conversations, nil)
	for _, request := range []appservice.InboxContextInput{
		{Query: filter, AnchorID: f.groupID, AnchorCursor: "bad"},
		{Query: filter, AnchorID: uuid.NewV7().String(), AnchorCursor: oldCursor},
		{Query: appservice.InboxQuery{Scope: domain.InboxScopeAll}, AnchorID: f.groupID, AnchorCursor: oldCursor},
	} {
		_, err := backend.GetInboxContext(ctx, meta, request)
		var apiError *appservice.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, "inbox_cursor_invalid", apiError.Reason)
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	for _, identity := range []*servermodels.Identity{f.owner, foreign.owner} {
		_, err := query.ReadContext(ctx, identity, inboxaction.ContextInput{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, AnchorID: f.groupID, AnchorCursor: oldCursor})
		require.ErrorIs(t, err, inboxaction.ErrCursorInvalid, "accepted foreign cursor")
	}
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	request := appservice.InboxContextInput{Query: filter, AnchorID: f.groupID, AnchorCursor: oldCursor}
	empty, err := backend.GetInboxContext(ctx, meta, request)
	require.NoError(t, err)
	require.Nil(t, empty.Anchor.Conversation)
	require.False(t, empty.Window.HasBefore)
	require.False(t, empty.Window.HasAfter)
	require.Equal(t, oldCursor, empty.Window.StartCursor)
	require.Equal(t, oldCursor, empty.Window.EndCursor)
	assertInboxWindowIDs(t, empty.Window.Conversations, nil)
	window, err := backend.ReadInboxWindow(ctx, meta, appservice.InboxWindowInput{Query: filter, StartCursor: oldCursor, EndCursor: oldCursor})
	require.NoError(t, err)
	require.False(t, window.HasBefore)
	require.False(t, window.HasAfter)
	require.Equal(t, oldCursor, window.StartCursor)
	assertInboxWindowIDs(t, window.Conversations, nil)
}

// TestInboxContextSnapshot 验证锚点、邻域和前后资格使用同一读取快照。
func TestInboxContextSnapshot(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	query := inboxaction.NewLoadInboxQuery(f.db)
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, "members.member_count AS member_count") && strings.Contains(event.Query, "cv.id IN")
	})
	var snapshot inboxaction.ConversationContext
	done := make(chan error, 1)
	go func() {
		var err error
		snapshot, err = query.ReadContext(context.WithValue(ctx, chatQueryGateKey{}, gate), f.member, inboxaction.ContextInput{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, AnchorID: f.groupID})
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, done))
	require.True(t, snapshot.Anchor.MatchesQuery)
	require.NotNil(t, snapshot.Anchor.Conversation)
	require.Len(t, snapshot.Window.Conversations, 1)
	require.Equal(t, f.groupID, snapshot.Window.Conversations[0].ID)
	current, err := query.ReadContext(ctx, f.member, inboxaction.ContextInput{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, AnchorID: f.groupID})
	require.NoError(t, err)
	require.Nil(t, current.Anchor.Conversation)
	require.Empty(t, current.Window.Conversations)
}

// TestInboxContextCustomerTransition 验证领取后锚点退出队列仍可读，原范围为空也不替换为其他队列。
func TestInboxContextCustomerTransition(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	for range 2 {
		_, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "邻近访客"})
		require.NoError(t, err)
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	filter := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned}
	page, _, err := query.Execute(ctx, f.owner, filter)
	require.NoError(t, err)
	require.Len(t, page.Conversations, 3)
	anchor := page.Conversations[1]
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, anchor.ID)
	require.NoError(t, err)
	result, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: anchor.ID, AnchorCursor: anchor.PositionCursor})
	require.NoError(t, err)
	require.False(t, result.Anchor.MatchesQuery)
	require.NotNil(t, result.Anchor.Conversation)
	require.Equal(t, []string{page.Conversations[0].ID, page.Conversations[2].ID}, conversationIDs(result.Window.Conversations))
	window, err := query.ReadWindow(ctx, f.owner, inboxaction.ReadWindowInput{Query: filter, StartCursor: anchor.PositionCursor, EndCursor: anchor.PositionCursor})
	require.NoError(t, err)
	require.Empty(t, window.Conversations)
	require.True(t, window.HasBefore)
	require.True(t, window.HasAfter)
	require.Equal(t, anchor.PositionCursor, window.StartCursor)
	require.Equal(t, anchor.PositionCursor, window.EndCursor)
	withoutPosition, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: anchor.ID})
	require.NoError(t, err)
	require.NotNil(t, withoutPosition.Anchor.Conversation)
	require.Empty(t, withoutPosition.Window.Conversations)
	require.Empty(t, withoutPosition.Window.StartCursor)
	// 核验倒置区间和跨查询边界的校验错误。
	for _, input := range []inboxaction.ReadWindowInput{
		{Query: filter, StartCursor: page.EndCursor, EndCursor: page.StartCursor},
		{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, StartCursor: page.StartCursor, EndCursor: page.EndCursor},
		{Query: filter, StartCursor: page.StartCursor},
	} {
		_, err := query.ReadWindow(ctx, f.owner, input)
		require.True(t, errors.Is(err, inboxaction.ErrQueryInvalid) || errors.Is(err, inboxaction.ErrCursorInvalid), "invalid range accepted: %+v err=%v", input, err)
	}
	_, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: filter.Scope, AssigneeFilter: filter.AssigneeFilter, Cursor: page.EndCursor, BeforeCursor: page.StartCursor})
	require.ErrorIs(t, err, inboxaction.ErrQueryInvalid, "conflicting directions accepted")
}
