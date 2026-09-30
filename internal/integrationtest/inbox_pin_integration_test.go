//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
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
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	suffix := uuid.NewV7().String()
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "置顶测试", DisplayName: "群主",
		Email: "owner@" + suffix + ".pin.test", Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := "member@" + suffix + ".pin.test"
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID,
	}); err != nil {
		t.Fatal(err)
	}
	login := loginMember(t, db, owner.Organization.ID, memberEmail, "password123")
	fixture := pinFixture{db: db, owner: owner, member: login.Identity}
	createGroup := groupchataction.NewCreateGroupConversationAction(db)
	groupA, err := createGroup.Execute(ctx, owner, groupchataction.GroupConversationInput{Title: "置顶群 A", MemberIdentityIDs: []string{login.Identity.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	fixture.groupA = groupA.ID
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(db).Execute(ctx, owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: login.Identity.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊",
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.directID = direct.Conversation.ID
	groupB, err := createGroup.Execute(ctx, owner, groupchataction.GroupConversationInput{Title: "置顶群 B", MemberIdentityIDs: []string{login.Identity.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	fixture.groupB = groupB.ID
	return fixture
}

// pin 按指定位置写入置顶并返回写入后的顺序版本。
func (f pinFixture) pin(t *testing.T, identity *servermodels.Identity, input conversationaction.ConversationPinInput) int64 {
	t.Helper()
	state, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(context.Background(), identity, input)
	if err != nil {
		t.Fatalf("置顶写入失败 %+v: %v", input, err)
	}
	return state.PinOrderVersion
}

// partition 读取指定分区的会话编号顺序。
func (f pinFixture) partition(t *testing.T, identity *servermodels.Identity, input inboxaction.LoadInput) []string {
	t.Helper()
	page, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(context.Background(), identity, input)
	if err != nil {
		t.Fatalf("读取分区 %s 失败: %v", input.Partition, err)
	}
	ids := make([]string, 0, len(page.Conversations))
	for _, conversation := range page.Conversations {
		ids = append(ids, conversation.ID)
	}
	return ids
}

// pinRanks 读取当前用户全部置顶记录的顺序值。
func (f pinFixture) pinRanks(t *testing.T, identity *servermodels.Identity) []int64 {
	t.Helper()
	var ranks []int64
	if err := f.db.NewSelect().Table("conversation_user_states").Column("pin_rank").
		Where("organization_id = ? AND user_id = ? AND pin_rank IS NOT NULL", identity.Organization.ID, identity.User.ID).
		OrderExpr("pin_rank ASC").Scan(context.Background(), &ranks); err != nil {
		t.Fatal(err)
	}
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
	if version != 3 {
		t.Fatalf("三次置顶后的顺序版本 = %d，want 3", version)
	}
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupA || got[1] != f.directID || got[2] != f.groupB {
		t.Fatalf("置顶区顺序 = %v，want [A X B]", got)
	}
	if got := f.partition(t, f.owner, regular); len(got) != 0 {
		t.Fatalf("普通区仍含置顶会话: %v", got)
	}
	// 未指定分区的调用方继续读取完整活动序，不因置顶漏项。
	if got := f.partition(t, f.owner, all); len(got) != 3 {
		t.Fatalf("完整活动序 = %v，want 三条", got)
	}
	// 重复置顶已在置顶区的会话不改变顺序，也不推进版本。
	if again := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true, ExpectedPinOrderVersion: version}); again != version {
		t.Fatalf("重复置顶推进了顺序版本: %d", again)
	}

	// 当前视图只显示群聊，单聊属于隐藏项；把 B 移到 A 前不应改变隐藏项的相对位置。
	groupsOnly := pinned
	groupsOnly.Kinds = []domain.ConversationType{domain.ConversationTypeGroup}
	if got := f.partition(t, f.owner, groupsOnly); len(got) != 2 || got[0] != f.groupA || got[1] != f.groupB {
		t.Fatalf("群聊视图置顶区 = %v，want [A B]", got)
	}
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{
		ConversationID: f.groupB, Pinned: true, NeighborID: f.groupA,
		Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version,
	})
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupB || got[1] != f.groupA || got[2] != f.directID {
		t.Fatalf("移动后的置顶区顺序 = %v，want [B A X]", got)
	}

	// 过期的顺序版本不得写入，两端同时排序只允许一个成功。
	stale := conversationaction.ConversationPinInput{ConversationID: f.directID, Pinned: true, NeighborID: f.groupB, Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version - 1}
	_, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(ctx, f.owner, stale)
	conflict, ok := errors.AsType[*conversationaction.ConflictError](err)
	if !ok || conflict.Reason != conversationaction.ConflictReasonPinOrderVersionStale {
		t.Fatalf("过期顺序版本的写入结果 = %v", err)
	}
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupB {
		t.Fatalf("冲突后置顶区被改写: %v", got)
	}

	// 取消置顶把会话交还普通区，重复取消不产生伪变化。
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, ExpectedPinOrderVersion: version})
	if got := f.partition(t, f.owner, pinned); len(got) != 2 || got[0] != f.groupB || got[1] != f.directID {
		t.Fatalf("取消置顶后的置顶区 = %v，want [B X]", got)
	}
	if got := f.partition(t, f.owner, regular); len(got) != 1 || got[0] != f.groupA {
		t.Fatalf("取消置顶后的普通区 = %v，want [A]", got)
	}
	if again := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, ExpectedPinOrderVersion: version}); again != version {
		t.Fatalf("重复取消置顶推进了顺序版本: %d", again)
	}
	// 另一名成员的置顶顺序与本人无关。
	if got := f.partition(t, f.member, pinned); len(got) != 0 {
		t.Fatalf("成员的置顶区受到群主影响: %v", got)
	}
}

// TestConversationPinPositions 验证首尾落点与回到原位置的请求不产生写入。
func TestConversationPinPositions(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	version := f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.directID, Pinned: true, ExpectedPinOrderVersion: version})
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})

	// 已经位于目标位置的移动不写入，也不推进顺序版本。
	for _, input := range []conversationaction.ConversationPinInput{
		{ConversationID: f.directID, Pinned: true, NeighborID: f.groupA, Position: domain.ConversationPinPositionAfter},
		{ConversationID: f.directID, Pinned: true, NeighborID: f.groupB, Position: domain.ConversationPinPositionBefore},
		{ConversationID: f.groupA, Pinned: true, Position: domain.ConversationPinPositionStart},
		{ConversationID: f.groupB, Pinned: true, Position: domain.ConversationPinPositionEnd},
	} {
		input.ExpectedPinOrderVersion = version
		if again := f.pin(t, f.owner, input); again != version {
			t.Fatalf("原位移动 %+v 推进了顺序版本: %d", input, again)
		}
	}
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupA || got[1] != f.directID || got[2] != f.groupB {
		t.Fatalf("原位移动后的置顶区顺序 = %v，want [A X B]", got)
	}

	// 首尾落点不带邻居，可在隐藏项存在时直接移到整个置顶区的两端。
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, Position: domain.ConversationPinPositionStart, ExpectedPinOrderVersion: version})
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupB || got[1] != f.groupA || got[2] != f.directID {
		t.Fatalf("移到区首后的顺序 = %v，want [B A X]", got)
	}
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, Position: domain.ConversationPinPositionEnd, ExpectedPinOrderVersion: version})
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupA || got[1] != f.directID || got[2] != f.groupB {
		t.Fatalf("移到区尾后的顺序 = %v，want [A X B]", got)
	}

	// 首尾落点不接受邻居，取消置顶不接受位置指令。
	for _, input := range []conversationaction.ConversationPinInput{
		{ConversationID: f.groupA, Pinned: true, Position: domain.ConversationPinPositionStart, NeighborID: f.groupB, ExpectedPinOrderVersion: version},
		{ConversationID: f.groupA, Pinned: true, Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version},
		{ConversationID: f.groupA, Position: domain.ConversationPinPositionEnd, ExpectedPinOrderVersion: version},
	} {
		_, err := conversationaction.NewUpdateConversationPinAction(f.db).Execute(ctx, f.owner, input)
		if _, ok := errors.AsType[*conversationaction.ValidationError](err); !ok {
			t.Fatalf("接受了不合法的位置指令 %+v: %v", input, err)
		}
	}
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
		if err != nil || len(results) != 1 {
			t.Fatalf("按 ID 读取失败: %v", err)
		}
		return results[0]
	}
	if result := matches(regularQuery); !result.MatchesQuery {
		t.Fatalf("未置顶会话不属于普通区: %+v", result)
	}
	if result := matches(pinnedQuery); result.MatchesQuery || result.Conversation == nil {
		t.Fatalf("未置顶会话被判为置顶区匹配: %+v", result)
	}
	f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	// 置顶后旧分区资格失效，客户端据此把该行移出普通区而不是重复显示。
	if result := matches(regularQuery); result.MatchesQuery || result.Conversation == nil {
		t.Fatalf("置顶后仍被判为普通区匹配: %+v", result)
	}
	if result := matches(pinnedQuery); !result.MatchesQuery || !result.Conversation.Pinned {
		t.Fatalf("置顶后未被判为置顶区匹配: %+v", result)
	}
	// 不指定分区的调用方仍按完整活动序核对资格。
	if result := matches(inboxaction.LoadInput{Scope: domain.InboxScopeChat}); !result.MatchesQuery {
		t.Fatalf("完整活动序漏掉置顶会话: %+v", result)
	}
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
		if _, err := f.db.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("pin_rank = ?", index+1).
			Where("organization_id = ? AND user_id = ? AND conversation_id = ?", f.owner.Organization.ID, f.owner.User.ID, conversationID).
			Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	version = f.pin(t, f.owner, conversationaction.ConversationPinInput{
		ConversationID: f.directID, Pinned: true, NeighborID: f.groupB,
		Position: domain.ConversationPinPositionBefore, ExpectedPinOrderVersion: version,
	})
	if got := f.partition(t, f.owner, pinned); len(got) != 3 || got[0] != f.groupA || got[1] != f.directID || got[2] != f.groupB {
		t.Fatalf("重编号后的置顶区顺序 = %v，want [A X B]", got)
	}
	ranks := f.pinRanks(t, f.owner)
	if len(ranks) != 3 || ranks[0] <= 0 || ranks[1]-ranks[0] < 2 || ranks[2]-ranks[1] < 2 {
		t.Fatalf("重编号未恢复顺序值间隔: %v", ranks)
	}
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
	if err != nil || len(page.Conversations) != 1 || page.Conversations[0].ID != f.groupA || !page.HasMore || page.PinOrderVersion != version {
		t.Fatalf("置顶区首页 = %+v，error = %v", page, err)
	}
	if !page.Conversations[0].Pinned {
		t.Fatal("置顶区摘要未标记置顶事实")
	}
	next := pinned
	next.Cursor = page.NextCursor
	second, _, err := query.Execute(ctx, f.owner, next)
	if err != nil || len(second.Conversations) != 1 || second.Conversations[0].ID != f.groupB {
		t.Fatalf("置顶区续页 = %+v，error = %v", second, err)
	}
	// 顺序变化使旧游标失效，客户端据此整区重读而不是静默漏项。
	f.pin(t, f.owner, conversationaction.ConversationPinInput{ConversationID: f.directID, Pinned: true, ExpectedPinOrderVersion: version})
	if _, _, err := query.Execute(ctx, f.owner, next); !errors.Is(err, inboxaction.ErrCursorInvalid) {
		t.Fatalf("顺序变化后仍接受旧置顶游标: %v", err)
	}
	// 普通区游标与置顶区分开，不受置顶顺序版本影响。
	regular := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionRegular}
	regularPage, _, err := query.Execute(ctx, f.owner, regular)
	if err != nil || len(regularPage.Conversations) != 0 {
		t.Fatalf("普通区 = %+v，error = %v", regularPage, err)
	}
}

// TestConversationPinRevocation 验证失权清除置顶并推进顺序版本，重新入群不自动恢复，解散仍保留置顶。
func TestConversationPinRevocation(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx := context.Background()
	pinned := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned}
	version := f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupA, Pinned: true})
	version = f.pin(t, f.member, conversationaction.ConversationPinInput{ConversationID: f.groupB, Pinned: true, ExpectedPinOrderVersion: version})
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
		ConversationID: f.groupA, MemberIdentityID: f.member.OrganizationIdentity.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.partition(t, f.member, pinned); len(got) != 1 || got[0] != f.groupB {
		t.Fatalf("失权后的置顶区 = %v，want [B]", got)
	}
	// 失权清理只清除本人对该会话的置顶，不写其他用户的账号行，因此不推进个人顺序版本；
	// 本人其他端由同一受众的会话失权通知触发整区重读。
	heads, err := inboxaction.NewLoadInboxQuery(f.db).SyncHeads(ctx, f.member)
	if err != nil || heads.PinOrderVersion != version {
		t.Fatalf("失权后的顺序版本 = %d，want %d，error = %v", heads.PinOrderVersion, version, err)
	}
	// 重新入群只恢复阅读资格，个人置顶不自动回到置顶区。
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{
		ConversationID: f.groupA, MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.partition(t, f.member, pinned); len(got) != 1 || got[0] != f.groupB {
		t.Fatalf("重新入群恢复了置顶: %v", got)
	}
	// 解散后历史仍可阅读，置顶保留。
	if _, err := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupB); err != nil {
		t.Fatal(err)
	}
	if got := f.partition(t, f.member, pinned); len(got) != 1 || got[0] != f.groupB {
		t.Fatalf("解散清除了可读会话的置顶: %v", got)
	}
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
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
			ConversationID: f.groupA, MemberIdentityID: f.member.OrganizationIdentity.ID,
		})
		removed <- err
	}()
	// 移除不会被持有成员账号行的置顶事务挡住，否则这里会一直等到超时。
	if err := <-removed; err != nil {
		t.Fatalf("移除成员失败: %v", err)
	}
	release.Do(func() { close(barrier.release) })
	// 移除已提交，恢复执行的置顶写入按失权拒绝，不会把顺序写回去。
	if err := <-pinned; !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("失权后的置顶写入 = %v", err)
	}
	// 移除提交后被移出会话的置顶已清除，另一条置顶保留。
	got := f.partition(t, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned})
	if len(got) != 1 || got[0] != f.groupB {
		t.Fatalf("并发结束后的置顶区 = %v，want [B]", got)
	}
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
	if err != nil || !state.Pinned || state.PinOrderVersion != 1 {
		t.Fatalf("客户会话置顶结果 = %+v，error = %v", state, err)
	}
	page, _, err := query.Execute(ctx, f.member, pinned)
	if err != nil || len(page.Conversations) != 1 || page.Conversations[0].ID != f.conversationID || !page.Conversations[0].Pinned {
		t.Fatalf("客户会话置顶区 = %+v，error = %v", page, err)
	}
	// 客户会话由企业客服共享，另一名客服的置顶顺序不受影响。
	ownerPage, _, err := query.Execute(ctx, f.owner, pinned)
	if err != nil || len(ownerPage.Conversations) != 0 {
		t.Fatalf("另一名客服的置顶区 = %+v，error = %v", ownerPage, err)
	}
	// 普通区不再包含该会话，完整活动序仍然包含。
	regular, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll, Partition: domain.InboxPartitionRegular})
	if err != nil || len(regular.Conversations) != 0 {
		t.Fatalf("客户会话仍留在普通区: %+v，error = %v", regular, err)
	}
	all, _, err := query.Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll})
	if err != nil || len(all.Conversations) != 1 || !all.Conversations[0].Pinned {
		t.Fatalf("完整活动序 = %+v，error = %v", all, err)
	}
	// 不存在的会话按会话不存在处理，不泄露其他企业的置顶顺序。
	if _, err := pin.Execute(ctx, f.member, conversationaction.ConversationPinInput{
		ConversationID: uuid.NewV7().String(), Pinned: true, ExpectedPinOrderVersion: 1,
	}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("置顶不存在的会话返回 %v", err)
	}
}

// TestGroupRemovalLockOrderAcrossOwners 验证两名群主同时移除对方时按统一锁序等待，不产生死锁。
func TestGroupRemovalLockOrderAcrossOwners(t *testing.T) {
	t.Parallel()
	f := newPinFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// 成员另建一个群并把群主拉进去，两人因此互为对方群里的普通成员。
	second, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.member, groupchataction.GroupConversationInput{
		Title: "互相移除测试群", MemberIdentityIDs: []string{f.owner.OrganizationIdentity.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 屏障拦住群主账号行的首次加锁，让两个移除事务真正交叠。
	barrier := &pinUserLockBarrier{userID: f.owner.User.ID, entered: make(chan struct{}), release: make(chan struct{})}
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })

	remove := func(actor *servermodels.Identity, conversationID, memberIdentityID string) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, actor, groupchataction.GroupConversationMemberInput{
				ConversationID: conversationID, MemberIdentityID: memberIdentityID,
			})
			done <- err
		}()
		return done
	}
	first := remove(f.owner, f.groupA, f.member.OrganizationIdentity.ID)
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// 第二个事务只锁本人账号行与自己群的会话，不去争抢对方账号行。
	other := remove(f.member, second.ID, f.owner.OrganizationIdentity.ID)
	if err := <-other; err != nil {
		t.Fatalf("成员移除群主失败: %v", err)
	}
	release.Do(func() { close(barrier.release) })
	if err := <-first; err != nil {
		t.Fatalf("群主移除成员失败: %v", err)
	}
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
		if _, err := f.db.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("pin_rank = ?", index+1).
			Where("organization_id = ? AND user_id = ? AND conversation_id = ?", f.member.Organization.ID, f.member.User.ID, conversationID).
			Exec(ctx); err != nil {
			t.Fatal(err)
		}
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
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
		ConversationID: f.groupA, MemberIdentityID: f.member.OrganizationIdentity.ID,
	}); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(barrier.release) })
	if err := <-pinned; err != nil {
		t.Fatalf("置顶写入失败: %v", err)
	}
	// 重新入群后群 A 不得自动回到置顶区。
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{
		ConversationID: f.groupA, MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID},
	}); err != nil {
		t.Fatal(err)
	}
	got := f.partition(t, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeChat, Partition: domain.InboxPartitionPinned})
	if len(got) != 2 || got[0] != f.directID || got[1] != f.groupB {
		t.Fatalf("重编号后的置顶区 = %v，want [X B]", got)
	}
}
