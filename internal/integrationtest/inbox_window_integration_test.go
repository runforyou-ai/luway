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

	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	serverfilecontent "github.com/runforyou-ai/cervi/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// assertInboxWindowIDs 核对窗口顺序、位置游标及非空切片。
func assertInboxWindowIDs(t *testing.T, actual []appservice.InboxConversation, expected []appservice.InboxConversation) {
	t.Helper()
	if actual == nil || !slices.EqualFunc(actual, expected, func(a, b appservice.InboxConversation) bool { return a.ID == b.ID }) {
		t.Fatalf("window IDs differ: actual=%v expected=%v", actual, expected)
	}
	for _, row := range actual {
		if row.PositionCursor == "" {
			t.Fatalf("missing position cursor for %s", row.ID)
		}
	}
}

// TestInboxContextDeepWindow 验证第二百条定位、前后翻页、完整范围重读和原位置恢复。
func TestInboxContextDeepWindow(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	for index := range 219 {
		group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: fmt.Sprintf("锚点群 %d", index), MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}})
		if err != nil {
			t.Fatal(err)
		}
		// 同时覆盖微秒、并列时间和没有消息的空时间区。
		if index%7 != 0 {
			activity := time.Date(2026, 9, 9, 0, 0, 0, 123456000, time.UTC).Add(time.Duration(index/3) * time.Microsecond)
			if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("last_activity_at = ?", activity).Where("id = ?", group.ID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	login := loginMember(t, f.db, f.owner.Organization.ID, f.memberEmail, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID}
	filter := appservice.InboxQuery{Scope: appservice.InboxScopeChat}
	all, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, Limit: 300})
	if err != nil || len(all.Conversations) != 220 || all.HasMore || all.HasBefore {
		t.Fatalf("full list size=%d err=%v", len(all.Conversations), err)
	}
	for _, index := range []int{0, 100, 199, 219} {
		anchor := all.Conversations[index]
		located, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: anchor.ID, BeforeLimit: 3, AfterLimit: 4})
		if err != nil || located.Anchor.Availability != appservice.InboxConversationMatching || located.Anchor.Conversation == nil {
			t.Fatalf("locate %d=%+v err=%v", index, located, err)
		}
		assertInboxWindowIDs(t, located.Window.Conversations, all.Conversations[max(0, index-3):min(220, index+5)])
		if located.Window.HasBefore != (index > 3) || located.Window.HasAfter != (index+5 < 220) {
			t.Fatalf("wrong neighbor flags at %d: %+v", index, located.Window)
		}
	}

	// 从尾部跨越空时间区向前遍历，结果应与一次完整排序相同。
	cursor := all.EndCursor
	actual := []appservice.InboxConversation{all.Conversations[219]}
	for attempts := 0; ; attempts++ {
		if attempts > 30 {
			t.Fatal("backward pagination did not terminate")
		}
		page, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, BeforeCursor: cursor, Limit: 13})
		if err != nil || !page.HasMore {
			t.Fatalf("previous page=%+v err=%v", page, err)
		}
		actual = append(page.Conversations, actual...)
		if !page.HasBefore {
			break
		}
		cursor = page.StartCursor
	}
	assertInboxWindowIDs(t, actual, all.Conversations)

	windowInput := appservice.InboxWindowInput{Query: filter, StartCursor: all.Conversations[50].PositionCursor, EndCursor: all.Conversations[209].PositionCursor}
	window, err := backend.ReadInboxWindow(ctx, meta, windowInput)
	if err != nil || !window.HasBefore || !window.HasAfter || window.StartCursor != windowInput.StartCursor || window.EndCursor != windowInput.EndCursor {
		t.Fatalf("full window=%+v err=%v", window, err)
	}
	assertInboxWindowIDs(t, window.Conversations, all.Conversations[50:210])

	anchor := all.Conversations[199]
	request := appservice.InboxContextInput{Query: filter, AnchorID: anchor.ID, AnchorCursor: anchor.PositionCursor, BeforeLimit: 3, AfterLimit: 4}
	if _, err := newGroupSendAction(f.db).Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: anchor.ID, ClientMessageID: uuid.NewV7().String(), Body: "锚点上浮"}); err != nil {
		t.Fatal(err)
	}
	moved, err := backend.GetInboxContext(ctx, meta, request)
	if err != nil || moved.Anchor.Availability != appservice.InboxConversationMatching || moved.Anchor.Conversation.PositionCursor == anchor.PositionCursor {
		t.Fatalf("moved anchor=%+v err=%v", moved, err)
	}
	expected := append(slices.Clone(all.Conversations[196:199]), all.Conversations[200:204]...)
	assertInboxWindowIDs(t, moved.Window.Conversations, expected)
	current, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: anchor.ID, BeforeLimit: 3, AfterLimit: 4})
	if err != nil || current.Window.HasBefore || current.Window.Conversations[0].ID != anchor.ID {
		t.Fatalf("current anchor=%+v err=%v", current, err)
	}

	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: anchor.ID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	removed, err := backend.GetInboxContext(ctx, meta, request)
	if err != nil || removed.Anchor.Availability != appservice.InboxConversationUnavailable || removed.Anchor.Conversation != nil {
		t.Fatalf("removed anchor=%+v err=%v", removed, err)
	}
	assertInboxWindowIDs(t, removed.Window.Conversations, expected)
	// 删除边界会话后按游标中的原始边界恢复窗口。
	if _, err := f.db.NewDelete().Model((*servermodels.Conversation)(nil)).Where("id = ?", anchor.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	deleted, err := backend.GetInboxContext(ctx, meta, request)
	if err != nil || deleted.Anchor.Conversation != nil {
		t.Fatalf("deleted anchor=%+v err=%v", deleted, err)
	}
	assertInboxWindowIDs(t, deleted.Window.Conversations, expected)
	refreshed, err := backend.ReadInboxWindow(ctx, meta, windowInput)
	if err != nil {
		t.Fatal(err)
	}
	expectedRange := append(slices.Clone(all.Conversations[50:199]), all.Conversations[200:210]...)
	assertInboxWindowIDs(t, refreshed.Conversations, expectedRange)
	// 两侧边界删除后保留原闭区间，并能从原值向两侧继续读取。
	if _, err := f.db.NewDelete().Model((*servermodels.Conversation)(nil)).Where("id IN (?)", bun.In([]string{all.Conversations[50].ID, all.Conversations[209].ID})).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	refreshed, err = backend.ReadInboxWindow(ctx, meta, windowInput)
	if err != nil || refreshed.StartCursor != windowInput.StartCursor || refreshed.EndCursor != windowInput.EndCursor {
		t.Fatalf("deleted interval boundaries=%+v err=%v", refreshed, err)
	}
	assertInboxWindowIDs(t, refreshed.Conversations, expectedRange[1:len(expectedRange)-1])
	previous, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, BeforeCursor: refreshed.StartCursor, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	assertInboxWindowIDs(t, previous.Conversations, all.Conversations[47:50])
	next, err := backend.LoadInbox(ctx, meta, appservice.LoadInboxInput{Scope: filter.Scope, Cursor: refreshed.EndCursor, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	assertInboxWindowIDs(t, next.Conversations, all.Conversations[210:213])
}

// TestInboxContextFilters 验证聊天、待处理与全部服务会话沿用同一资格、排序和默认窗口大小。
func TestInboxContextFilters(t *testing.T) {
	t.Parallel()
	f := newInboxPaginationFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	filters := []inboxaction.LoadInput{{Scope: domain.InboxScopeChat}, {Scope: domain.InboxScopePending}, {Scope: domain.InboxScopeAll}}
	for _, filter := range customerInboxFilters(f.owner.OrganizationIdentity.ID, f.member.OrganizationIdentity.ID) {
		filters = append(filters, filter.input)
	}
	for _, filter := range filters {
		filter.Limit = 300
		page, _, err := query.Execute(ctx, f.owner, filter)
		if err != nil || len(page.Conversations) == 0 {
			t.Fatalf("filter=%+v err=%v", filter, err)
		}
		index := len(page.Conversations) / 2
		located, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: page.Conversations[index].ID})
		if err != nil || !located.Anchor.MatchesQuery || !slices.EqualFunc(located.Window.Conversations, page.Conversations[max(0, index-25):min(len(page.Conversations), index+26)], func(a, b inboxaction.ConversationSummary) bool { return a.ID == b.ID }) {
			t.Fatalf("filter=%+v context=%+v err=%v", filter, located, err)
		}
		// 每类会话都能够独立定位，窗口内所有摘要仍符合当前筛选。
		seen := make(map[domain.ConversationType]bool)
		for _, row := range page.Conversations {
			if seen[row.Type] {
				continue
			}
			seen[row.Type] = true
			located, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: row.ID, BeforeLimit: 1, AfterLimit: 1})
			if err != nil || !located.Anchor.MatchesQuery || located.Anchor.Conversation.Type != row.Type {
				t.Fatalf("type=%s context=%+v err=%v", row.Type, located, err)
			}
		}
	}
}

// TestInboxContextUnavailable 验证缺失原位置、退出筛选、跨企业、空窗口和非法边界。
func TestInboxContextUnavailable(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	login := loginMember(t, f.db, f.owner.Organization.ID, f.memberEmail, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Mode: domain.DeploymentModeSelfHosted}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID}
	filter := appservice.InboxQuery{Scope: appservice.InboxScopeChat}
	located, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: f.groupID})
	if err != nil || len(located.Window.Conversations) != 1 || located.Window.Conversations[0].LastActivityAt != nil {
		t.Fatalf("empty conversation=%+v err=%v", located, err)
	}
	oldCursor := located.Anchor.Conversation.PositionCursor
	foreign := newNavigationFixture(t)
	for _, id := range []string{uuid.NewV7().String(), foreign.groupID} {
		unavailable, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: filter, AnchorID: id})
		if err != nil || unavailable.Anchor.Availability != appservice.InboxConversationUnavailable || unavailable.Anchor.Conversation != nil || unavailable.Window.StartCursor != "" {
			t.Fatalf("unavailable=%+v err=%v", unavailable, err)
		}
		assertInboxWindowIDs(t, unavailable.Window.Conversations, nil)
	}
	outside, err := backend.GetInboxContext(ctx, meta, appservice.InboxContextInput{Query: appservice.InboxQuery{Scope: appservice.InboxScopeAll}, AnchorID: f.groupID})
	if err != nil || outside.Anchor.Availability != appservice.InboxConversationOutsideQuery || outside.Anchor.Conversation == nil || outside.Anchor.Conversation.PositionCursor != "" {
		t.Fatalf("outside query=%+v err=%v", outside, err)
	}
	assertInboxWindowIDs(t, outside.Window.Conversations, nil)
	for _, request := range []appservice.InboxContextInput{
		{Query: filter, AnchorID: f.groupID, AnchorCursor: "bad"},
		{Query: filter, AnchorID: uuid.NewV7().String(), AnchorCursor: oldCursor},
		{Query: appservice.InboxQuery{Scope: appservice.InboxScopeAll}, AnchorID: f.groupID, AnchorCursor: oldCursor},
	} {
		_, err := backend.GetInboxContext(ctx, meta, request)
		var apiError *appservice.Error
		if !errors.As(err, &apiError) || apiError.Reason != "inbox_cursor_invalid" {
			t.Fatalf("invalid cursor=%v", err)
		}
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	for _, identity := range []*servermodels.Identity{f.owner, foreign.owner} {
		_, err := query.ReadContext(ctx, identity, inboxaction.ContextInput{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, AnchorID: f.groupID, AnchorCursor: oldCursor})
		if !errors.Is(err, inboxaction.ErrCursorInvalid) {
			t.Fatalf("accepted foreign cursor: %v", err)
		}
	}
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	request := appservice.InboxContextInput{Query: filter, AnchorID: f.groupID, AnchorCursor: oldCursor}
	empty, err := backend.GetInboxContext(ctx, meta, request)
	if err != nil || empty.Anchor.Conversation != nil || empty.Window.HasBefore || empty.Window.HasAfter || empty.Window.StartCursor != oldCursor || empty.Window.EndCursor != oldCursor {
		t.Fatalf("empty neighborhood=%+v err=%v", empty, err)
	}
	assertInboxWindowIDs(t, empty.Window.Conversations, nil)
	window, err := backend.ReadInboxWindow(ctx, meta, appservice.InboxWindowInput{Query: filter, StartCursor: oldCursor, EndCursor: oldCursor})
	if err != nil || window.HasBefore || window.HasAfter || window.StartCursor != oldCursor {
		t.Fatalf("empty range=%+v err=%v", window, err)
	}
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
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	gate.open()
	if err := waitChatResult(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Anchor.MatchesQuery || snapshot.Anchor.Conversation == nil || len(snapshot.Window.Conversations) != 1 || snapshot.Window.Conversations[0].ID != f.groupID {
		t.Fatalf("mixed snapshot=%+v", snapshot)
	}
	current, err := query.ReadContext(ctx, f.member, inboxaction.ContextInput{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, AnchorID: f.groupID})
	if err != nil || current.Anchor.Conversation != nil || len(current.Window.Conversations) != 0 {
		t.Fatalf("next snapshot=%+v err=%v", current, err)
	}
}

// TestInboxContextCustomerTransition 验证领取后锚点退出队列仍可读，原范围为空也不替换为其他队列。
func TestInboxContextCustomerTransition(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	for range 2 {
		if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "邻近访客"}); err != nil {
			t.Fatal(err)
		}
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	filter := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterUnassigned}
	page, _, err := query.Execute(ctx, f.owner, filter)
	if err != nil || len(page.Conversations) != 3 {
		t.Fatalf("queue=%+v err=%v", page, err)
	}
	anchor := page.Conversations[1]
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, anchor.ID); err != nil {
		t.Fatal(err)
	}
	result, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: anchor.ID, AnchorCursor: anchor.PositionCursor})
	if err != nil || result.Anchor.MatchesQuery || result.Anchor.Conversation == nil || len(result.Window.Conversations) != 2 || result.Window.Conversations[0].ID != page.Conversations[0].ID || result.Window.Conversations[1].ID != page.Conversations[2].ID {
		t.Fatalf("claimed anchor=%+v err=%v", result, err)
	}
	window, err := query.ReadWindow(ctx, f.owner, inboxaction.ReadWindowInput{Query: filter, StartCursor: anchor.PositionCursor, EndCursor: anchor.PositionCursor})
	if err != nil || len(window.Conversations) != 0 || !window.HasBefore || !window.HasAfter || window.StartCursor != anchor.PositionCursor || window.EndCursor != anchor.PositionCursor {
		t.Fatalf("empty interval within nonempty query=%+v err=%v", window, err)
	}
	withoutPosition, err := query.ReadContext(ctx, f.owner, inboxaction.ContextInput{Query: filter, AnchorID: anchor.ID})
	if err != nil || withoutPosition.Anchor.Conversation == nil || len(withoutPosition.Window.Conversations) != 0 || withoutPosition.Window.StartCursor != "" {
		t.Fatalf("unlocated readable anchor=%+v err=%v", withoutPosition, err)
	}
	// 核验倒置区间和跨查询边界的校验错误。
	for _, input := range []inboxaction.ReadWindowInput{
		{Query: filter, StartCursor: page.EndCursor, EndCursor: page.StartCursor},
		{Query: inboxaction.LoadInput{Scope: domain.InboxScopeChat}, StartCursor: page.StartCursor, EndCursor: page.EndCursor},
		{Query: filter, StartCursor: page.StartCursor},
	} {
		_, err := query.ReadWindow(ctx, f.owner, input)
		if !errors.Is(err, inboxaction.ErrQueryInvalid) && !errors.Is(err, inboxaction.ErrCursorInvalid) {
			t.Fatalf("invalid range accepted: %+v err=%v", input, err)
		}
	}
	_, _, err = query.Execute(ctx, f.owner, inboxaction.LoadInput{Scope: filter.Scope, AssigneeFilter: filter.AssigneeFilter, Cursor: page.EndCursor, BeforeCursor: page.StartCursor})
	if !errors.Is(err, inboxaction.ErrQueryInvalid) {
		t.Fatalf("conflicting directions accepted: %v", err)
	}
}

// TestInboxWindowSnapshot 验证完整区间候选、摘要及边界资格来自同一快照。
func TestInboxWindowSnapshot(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	query := inboxaction.NewLoadInboxQuery(f.db)
	filter := inboxaction.LoadInput{Scope: domain.InboxScopeChat}
	page, _, err := query.Execute(ctx, f.member, filter)
	if err != nil {
		t.Fatal(err)
	}
	input := inboxaction.ReadWindowInput{Query: filter, StartCursor: page.StartCursor, EndCursor: page.EndCursor}
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, "AS candidates") && strings.Contains(event.Query, "ORDER BY")
	})
	var snapshot inboxaction.ConversationWindow
	done := make(chan error, 1)
	go func() {
		var err error
		snapshot, err = query.ReadWindow(context.WithValue(ctx, chatQueryGateKey{}, gate), f.member, input)
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	gate.open()
	if err := waitChatResult(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Conversations) != 1 || snapshot.Conversations[0].ID != f.groupID || snapshot.HasBefore || snapshot.HasAfter {
		t.Fatalf("mixed range snapshot=%+v", snapshot)
	}
	current, err := query.ReadWindow(ctx, f.member, input)
	if err != nil || len(current.Conversations) != 0 || current.StartCursor != input.StartCursor || current.EndCursor != input.EndCursor {
		t.Fatalf("next range snapshot=%+v err=%v", current, err)
	}
}
