//go:build server

package integrationtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// pinFixture 提供两名成员、两个群聊和一个内部单聊，用于验证个人置顶顺序与分区。
type pinFixture struct {
	db       *bun.DB
	owner    *servermodels.Identity
	member   *servermodels.Identity
	groupA   string
	groupB   string
	directID string
}

// newPinFixture 用真实创建入口建立置顶测试所需的会话。
func newPinFixture(t *testing.T) pinFixture {
	t.Helper()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	suffix := uuid.NewV7().String()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "置顶测试", DisplayName: "群主",
		Email: "owner@" + suffix + ".pin.test", Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := "member@" + suffix + ".pin.test"
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID,
	})
	require.NoError(t, err)
	login := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123")
	fixture := pinFixture{db: db, owner: owner, member: login.Identity}
	createGroup := groupchataction.NewCreateGroupConversationAction(db)
	groupA, err := createGroup.Execute(ctx, owner, groupchataction.GroupConversationInput{Title: "置顶群 A", MemberIdentityIDs: []string{login.Identity.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	fixture.groupA = groupA.ID
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(db, testEnqueuer).Execute(ctx, owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: login.Identity.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊",
	})
	require.NoError(t, err)
	fixture.directID = direct.Conversation.ID
	groupB, err := createGroup.Execute(ctx, owner, groupchataction.GroupConversationInput{Title: "置顶群 B", MemberIdentityIDs: []string{login.Identity.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	fixture.groupB = groupB.ID
	return fixture
}

// pin 按指定位置写入置顶并返回写入后的顺序版本。
func (f pinFixture) pin(t *testing.T, identity *servermodels.Identity, input conversationaction.ConversationPinInput) int64 {
	t.Helper()
	state, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(context.Background(), identity, input)
	require.NoError(t, err, "置顶写入失败 %+v", input)
	return state.PinOrderVersion
}

// partition 读取指定分区的会话编号顺序。
func (f pinFixture) partition(t *testing.T, identity *servermodels.Identity, input inboxaction.LoadInput) []string {
	t.Helper()
	page, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(context.Background(), identity, input)
	require.NoError(t, err, "读取分区 %s 失败", input.Partition)
	return arr.Map(page.Conversations, func(conversation inboxaction.ConversationSummary) string { return conversation.ID })
}

// pinRanks 读取当前用户全部置顶记录的顺序值。
func (f pinFixture) pinRanks(t *testing.T, identity *servermodels.Identity) []int64 {
	t.Helper()
	var ranks []int64
	require.NoError(t, f.db.NewSelect().Table("conversation_user_states").Column("pin_rank").
		Where("workspace_id = ? AND user_id = ? AND pin_rank IS NOT NULL", identity.Workspace.ID, identity.User.ID).
		OrderExpr("pin_rank ASC").Scan(context.Background(), &ranks))
	return ranks
}

// TestConversationPinOrder 验证置顶追加末尾、按邻居移动保留隐藏项顺序，以及顺序版本的并发校验。
func TestConversationPinOrder(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	all := inboxaction.LoadInput{Scope: domain.InboxScopeChat}
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	regular := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionRegular}

	version := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.directID, Pinned: true, ExpectedPinOrderVersion: version})
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})
	require.Equal(t, int64(3), version, "三次置顶后的顺序版本")
	require.Equal(t, []string{f.groupA, f.directID, f.groupB}, f.partition(t, f.owner, pinned), "置顶区顺序")
	require.Empty(t, f.partition(t, f.owner, regular), "普通区仍含置顶会话")
	// 未指定分区的调用方继续读取完整活动序，不因置顶漏项。
	require.Len(t, f.partition(t, f.owner, all), 3, "完整活动序")
	// 重复置顶已在置顶区的会话不改变顺序，也不推进版本。
	require.Equal(t, version, f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true, ExpectedPinOrderVersion: version}), "重复置顶推进了顺序版本")

	// 当前视图只显示群聊，单聊属于隐藏项；把 B 移到 A 前不应改变隐藏项的相对位置。
	groupsOnly := pinned
	groupsOnly.Kinds = []domain.ConversationType{domain.ConversationTypeGroup}
	require.Equal(t, []string{f.groupA, f.groupB}, f.partition(t, f.owner, groupsOnly), "群聊视图置顶区")
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{
		ConversationID: f.groupB, Pinned: true, NeighborID: f.groupA,
		Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version,
	})
	require.Equal(t, []string{f.groupB, f.groupA, f.directID}, f.partition(t, f.owner, pinned), "移动后的置顶区顺序")

	// 过期的顺序版本不得写入，两端同时排序只允许一个成功。
	stale := conversationaction.ConversationPinInput{ConversationID: f.directID, Pinned: true, NeighborID: f.groupB, Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version - 1}
	_, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(ctx, f.owner, stale)
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict, "过期顺序版本的写入结果")
	require.Equal(t, conversationaction.ConflictReasonPinOrderVersionStale, conflict.Reason)
	got := f.partition(t, f.owner, pinned)
	require.Len(t, got, 3, "冲突后置顶区被改写")
	require.Equal(t, f.groupB, got[0], "冲突后置顶区被改写")

	// 取消置顶把会话交还普通区，重复取消不产生伪变化。
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, ExpectedPinOrderVersion: version})
	require.Equal(t, []string{f.groupB, f.directID}, f.partition(t, f.owner, pinned), "取消置顶后的置顶区")
	require.Equal(t, []string{f.groupA}, f.partition(t, f.owner, regular), "取消置顶后的普通区")
	require.Equal(t, version, f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, ExpectedPinOrderVersion: version}), "重复取消置顶推进了顺序版本")
	// 另一名成员的置顶顺序与本人无关。
	require.Empty(t, f.partition(t, f.member, pinned), "成员的置顶区受到群主影响")
}

// TestConversationPinPartitionEligibility 验证按 ID 核对列表资格时同样应用置顶分区。
func TestConversationPinPartitionEligibility(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	pinnedQuery := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	regularQuery := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionRegular}
	matches := func(input inboxaction.LoadInput) inboxaction.ConversationResult {
		t.Helper()
		results, err := query.ReadByIDs(ctx, f.owner, []string{f.groupA}, &input)
		require.NoError(t, err, "按 ID 读取失败")
		require.Len(t, results, 1, "按 ID 读取失败")
		return results[0]
	}
	require.True(t, matches(regularQuery).MatchesQuery, "未置顶会话不属于普通区")
	result := matches(pinnedQuery)
	require.False(t, result.MatchesQuery, "未置顶会话被判为置顶区匹配: %+v", result)
	require.NotNil(t, result.Conversation, "未置顶会话被判为置顶区匹配: %+v", result)
	f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	// 置顶后旧分区资格失效，客户端据此把该行移出普通区而不是重复显示。
	result = matches(regularQuery)
	require.False(t, result.MatchesQuery, "置顶后仍被判为普通区匹配: %+v", result)
	require.NotNil(t, result.Conversation, "置顶后仍被判为普通区匹配: %+v", result)
	result = matches(pinnedQuery)
	require.True(t, result.MatchesQuery, "置顶后未被判为置顶区匹配: %+v", result)
	require.True(t, result.Conversation.Pinned, "置顶后未被判为置顶区匹配: %+v", result)
	// 不指定分区的调用方仍按完整活动序核对资格。
	require.True(t, matches(inboxaction.LoadInput{Scope: domain.InboxScopeChat}).MatchesQuery, "完整活动序漏掉置顶会话")
}

// TestConversationPinRenumber 验证顺序值间隔耗尽后在同一事务内整区重编号且不触发唯一冲突。
func TestConversationPinRenumber(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	version := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})
	// 把相邻顺序值压到最小间隔，后续插入只能通过重编号完成。
	for index, conversationID := range []string{f.groupA, f.groupB} {
		_, err := f.db.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("pin_rank = ?", index+1).
			Where("workspace_id = ? AND user_id = ? AND conversation_id = ?", f.owner.Workspace.ID, f.owner.User.ID, conversationID).
			Exec(ctx)
		require.NoError(t, err)
	}
	f.pin(t, f.owner, conversationaction.ConversationPinInput{
		ConversationID: f.directID, Pinned: true, NeighborID: f.groupB,
		Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version,
	})
	require.Equal(t, []string{f.groupA, f.directID, f.groupB}, f.partition(t, f.owner, pinned), "重编号后的置顶区顺序")
	ranks := f.pinRanks(t, f.owner)
	require.Len(t, ranks, 3, "重编号未恢复顺序值间隔: %v", ranks)
	require.Positive(t, ranks[0], "重编号未恢复顺序值间隔: %v", ranks)
	require.GreaterOrEqual(t, ranks[1]-ranks[0], int64(2), "重编号未恢复顺序值间隔: %v", ranks)
	require.GreaterOrEqual(t, ranks[2]-ranks[1], int64(2), "重编号未恢复顺序值间隔: %v", ranks)
}

// TestConversationPinCursor 验证置顶区游标绑定个人顺序版本，顺序变化后要求整区重读。
func TestConversationPinCursor(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned, Limit: 1}
	version := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})
	page, _, err := query.Execute(ctx, f.owner, pinned)
	require.NoError(t, err)
	require.Len(t, page.Conversations, 1)
	require.Equal(t, f.groupA, page.Conversations[0].ID)
	require.True(t, page.HasMore)
	require.Equal(t, version, page.PinOrderVersion)
	require.True(t, page.Conversations[0].Pinned, "置顶区摘要未标记置顶事实")
	next := pinned
	next.Cursor = page.NextCursor
	second, _, err := query.Execute(ctx, f.owner, next)
	require.NoError(t, err)
	require.Len(t, second.Conversations, 1)
	require.Equal(t, f.groupB, second.Conversations[0].ID)
	// 顺序变化使旧游标失效，客户端据此整区重读而不是静默漏项。
	f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.directID, Pinned: true, ExpectedPinOrderVersion: version})
	_, _, err = query.Execute(ctx, f.owner, next)
	require.ErrorIs(t, err, inboxaction.ErrCursorInvalid, "顺序变化后仍接受旧置顶游标")
	// 普通区游标与置顶区分开，不受置顶顺序版本影响。
	regular := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionRegular}
	regularPage, _, err := query.Execute(ctx, f.owner, regular)
	require.NoError(t, err)
	require.Empty(t, regularPage.Conversations, "普通区")
}

// TestConversationPinRevocation 验证失权清除置顶并推进顺序版本，重新入群不自动恢复，解散仍保留置顶。
func TestConversationPinRevocation(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	version := f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})
	_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
		ConversationID: f.groupA, MemberIdentityID: f.member.WorkspaceIdentity.ID,
	})
	require.NoError(t, err)
	require.Equal(t, []string{f.groupB}, f.partition(t, f.member, pinned), "失权后的置顶区")
	// 失权清理只清除本人对该会话的置顶，不写其他用户的账号行，因此不推进个人顺序版本；
	// 本人其他端由同一受众的会话失权通知触发整区重读。
	heads, err := inboxaction.NewLoadInboxQuery(f.db).SyncHeads(ctx, f.member)
	require.NoError(t, err)
	require.Equal(t, version, heads.PinOrderVersion, "失权后的顺序版本")
	// 重新入群只恢复阅读资格，个人置顶不自动回到置顶区。
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{
		ConversationID: f.groupA, MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	require.Equal(t, []string{f.groupB}, f.partition(t, f.member, pinned), "重新入群恢复了置顶")
	// 解散后历史仍可阅读，置顶保留。
	_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupB)
	require.NoError(t, err)
	require.Equal(t, []string{f.groupB}, f.partition(t, f.member, pinned), "解散清除了可读会话的置顶")
}

// pinUserLockBarrier 在置顶事务锁定本人账号行之后、取得会话锁之前暂停该事务。
type pinUserLockBarrier struct {
	userID           string
	entered, release chan struct{}
	once             sync.Once
}

// BeforeQuery 保留置顶写入的上下文。
func (b *pinUserLockBarrier) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在本人账号行加锁成功后暂停，让移除成员的事务同时进行。
func (b *pinUserLockBarrier) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if event.Err != nil || !strings.Contains(event.Query, "FOR NO KEY UPDATE") || !strings.Contains(event.Query, b.userID) {
		return
	}
	b.once.Do(func() {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	})
}

// TestConversationPinRemovalLockOrder 验证移除成员不等待被移出成员的账号行，并在提交后拒绝迟到的置顶写入。
func TestConversationPinRemovalLockOrder(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	version := f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true})
	version = f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true, ExpectedPinOrderVersion: version})

	barrier := &pinUserLockBarrier{userID: f.member.User.ID, entered: make(chan struct{}), release: make(chan struct{})}
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })

	// 成员先锁定本人账号行，停在取得会话锁之前。
	pinned := make(chan error, 1)
	go func() {
		_, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(ctx, f.member, conversationaction.ConversationPinInput{
			ConversationID: f.groupA, Pinned: true, Position: domain.ConversationPinPositionStart, ExpectedPinOrderVersion: version,
		})
		pinned <- err
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	// 群主同时移除该成员：失权清理不写成员账号行，因此不与持有该行的置顶写入形成循环等待。
	removed := make(chan error, 1)
	go func() {
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
			ConversationID: f.groupA, MemberIdentityID: f.member.WorkspaceIdentity.ID,
		})
		removed <- err
	}()
	// 移除不会被持有成员账号行的置顶事务挡住，否则这里会一直等到超时。
	require.NoError(t, <-removed, "移除成员失败")
	release.Do(func() { close(barrier.release) })
	// 移除已提交，恢复执行的置顶写入按失权拒绝，不会把顺序写回去。
	require.ErrorIs(t, <-pinned, conversationaction.ErrConversationNotFound, "失权后的置顶写入")
	// 移除提交后被移出会话的置顶已清除，另一条置顶保留。
	got := f.partition(t, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned})
	require.Equal(t, []string{f.groupB}, got, "并发结束后的置顶区")
}

// TestCustomerConversationPin 验证客户会话按企业客服的历史访问资格置顶，并进入同一个人置顶顺序。
func TestCustomerConversationPin(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	query := inboxaction.NewLoadInboxQuery(f.db)
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeAll, Partition: domain.InboxPartitionPinned}
	pin := conversationaction.NewUpdateConversationPinAction(f.db)
	state, err := pin.Execute(ctx, f.member, conversationaction.ConversationPinInput{ConversationID: f.conversationID, Pinned: true})
	require.NoError(t, err)
	require.True(t, state.Pinned)
	require.Equal(t, int64(1), state.PinOrderVersion)
	page, _, err := query.Execute(ctx, f.member, pinned)
	require.NoError(t, err)
	require.Len(t, page.Conversations, 1)
	require.Equal(t, f.conversationID, page.Conversations[0].ID)
	require.True(t, page.Conversations[0].Pinned)
	// 客户会话由企业客服共享，另一名客服的置顶顺序不受影响。
	ownerPage, _, err := query.Execute(ctx, f.owner, pinned)
	require.NoError(t, err)
	require.Empty(t, ownerPage.Conversations, "另一名客服的置顶区")
	// 普通区不再包含该会话，完整活动序仍然包含。
	regular, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll, Partition: domain.InboxPartitionRegular})
	require.NoError(t, err)
	require.Empty(t, regular.Conversations, "客户会话仍留在普通区")
	all, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll})
	require.NoError(t, err)
	require.Len(t, all.Conversations, 1)
	require.True(t, all.Conversations[0].Pinned)
	// 不存在的会话按会话不存在处理，不泄露其他企业的置顶顺序。
	_, err = pin.Execute(ctx, f.member, conversationaction.ConversationPinInput{
		ConversationID: uuid.NewV7().String(), Pinned: true, ExpectedPinOrderVersion: 1,
	})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "置顶不存在的会话")
}

// TestGroupRemovalLockOrderAcrossOwners 验证两名群主同时移除对方时按统一锁序等待，不产生死锁。
func TestGroupRemovalLockOrderAcrossOwners(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 成员另建一个群并把群主拉进去，两人因此互为对方群里的普通成员。
	second, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.member, groupchataction.GroupConversationInput{
		Title: "互相移除测试群", MemberIdentityIDs: []string{f.owner.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	// 屏障拦住群主账号行的首次加锁，让两个移除事务真正交叠。
	barrier := &pinUserLockBarrier{userID: f.owner.User.ID, entered: make(chan struct{}), release: make(chan struct{})}
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })

	remove := func(actor *servermodels.Identity, conversationID, memberIdentityID string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, actor, groupchataction.GroupConversationMemberInput{
				ConversationID: conversationID, MemberIdentityID: memberIdentityID,
			})
			done <- err
		}()
		return done
	}
	first := remove(f.owner, f.groupA, f.member.WorkspaceIdentity.ID)
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// 第二个事务只锁本人账号行与自己群的会话，不去争抢对方账号行。
	other := remove(f.member, second.ID, f.owner.WorkspaceIdentity.ID)
	require.NoError(t, <-other, "成员移除群主失败")
	release.Do(func() { close(barrier.release) })
	require.NoError(t, <-first, "群主移除成员失败")
}

// pinReadBarrier 在置顶写入读取顺序集合之后暂停该事务。
type pinReadBarrier struct {
	userID           string
	entered, release chan struct{}
	once             sync.Once
}

// BeforeQuery 保留置顶写入的上下文。
func (b *pinReadBarrier) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在读取本人置顶集合后暂停，让失权清理在重编号之前提交。
func (b *pinReadBarrier) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	// 只拦读取顺序集合的语句，失权清理的更新同样带置顶条件但不含排序。
	if event.Err != nil || !strings.Contains(event.Query, "ORDER BY pin_rank") || !strings.Contains(event.Query, b.userID) {
		return
	}
	b.once.Do(func() {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	})
}

// TestConversationPinRenumberKeepsRevokedCleared 验证重编号不会写回并发失权清除的置顶。
func TestConversationPinRenumberKeepsRevokedCleared(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	version := f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})
	// 把相邻顺序值压到最小间隔，后续插入只能通过整区重编号完成。
	for index, conversationID := range []string{f.groupA, f.groupB} {
		_, err := f.db.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("pin_rank = ?", index+1).
			Where("workspace_id = ? AND user_id = ? AND conversation_id = ?", f.member.Workspace.ID, f.member.User.ID, conversationID).
			Exec(ctx)
		require.NoError(t, err)
	}
	barrier := &pinReadBarrier{userID: f.member.User.ID, entered: make(chan struct{}), release: make(chan struct{})}
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })

	// 成员把单聊插到两个群之间，读到的顺序集合此时仍包含群 A。
	pinned := make(chan error, 1)
	go func() {
		_, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(ctx, f.member, conversationaction.ConversationPinInput{
			ConversationID: f.directID, Pinned: true, NeighborID: f.groupB,
			Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version,
		})
		pinned <- err
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// 群主在重编号之前移除该成员并提交，群 A 的置顶随失权清除。
	_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
		ConversationID: f.groupA, MemberIdentityID: f.member.WorkspaceIdentity.ID,
	})
	require.NoError(t, err)
	release.Do(func() { close(barrier.release) })
	require.NoError(t, <-pinned, "置顶写入失败")
	// 重新入群后群 A 不得自动回到置顶区。
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{
		ConversationID: f.groupA, MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	got := f.partition(t, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned})
	require.Equal(t, []string{f.directID, f.groupB}, got, "重编号后的置顶区")
}
