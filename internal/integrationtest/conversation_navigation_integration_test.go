//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// navigationFixture 是两名成员的测试工作区，可按需建立群聊；成员邮箱在测试库内唯一，密码均为 password123，令牌为创建时签发的账号会话。
type navigationFixture struct {
	db                      *bun.DB
	owner, member           *servermodels.Identity
	ownerEmail, memberEmail string
	ownerToken, memberToken string
	groupID, subjectID      string
}

// newMemberFixture 创建独立测试工作区、两名成员及其登录会话。
func newMemberFixture(t *testing.T) navigationFixture {
	t.Helper()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ownerEmail := servertest.UniqueEmail("owner")
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "导航测试", DisplayName: "群主", Email: ownerEmail, Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := servertest.UniqueEmail("member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	login := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123")
	return navigationFixture{
		db: db, owner: owner, member: login.Identity, ownerEmail: ownerEmail, memberEmail: memberEmail,
		ownerToken: installed.Token, memberToken: login.Token,
	}
}

// newNavigationFixture 在两名成员的独立工作区内建立群聊。
func newNavigationFixture(t *testing.T) navigationFixture {
	t.Helper()
	fixture := newMemberFixture(t)
	ctx := context.Background()
	group, err := groupchataction.NewCreateGroupConversationAction(fixture.db).Execute(ctx, fixture.owner, groupchataction.GroupConversationInput{Title: "导航测试群", MemberIdentityIDs: []string{fixture.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	detail, err := groupchataction.NewGetGroupConversationQuery(fixture.db).Execute(ctx, fixture.owner, group.ID)
	require.NoError(t, err)
	fixture.groupID = group.ID
	for _, participant := range detail.Participants {
		if participant.IdentityID == fixture.member.WorkspaceIdentity.ID {
			fixture.subjectID = participant.ChatSubjectID
		}
	}
	return fixture
}

// send 通过真实发送命令写入测试消息。
func (f navigationFixture) send(t *testing.T, identity *servermodels.Identity, body string, all bool, subjects ...string) conversationaction.ConversationMessage {
	t.Helper()
	message, err := newGroupSendAction(f.db).Execute(context.Background(), identity, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: body, MentionAll: all, MentionSubjectIDs: subjects})
	require.NoError(t, err)
	return message
}

// state 读取个人水位，保留被删除消息的指针。
func (f navigationFixture) state(t *testing.T) servermodels.ConversationUserState {
	t.Helper()
	var state servermodels.ConversationUserState
	require.NoError(t, f.db.NewSelect().Model(&state).Where("cus.workspace_id = ? AND cus.conversation_id = ? AND cus.user_id = ?", f.owner.Workspace.ID, f.groupID, f.member.User.ID).Scan(context.Background()))
	return state
}

// TestGroupMentionNavigation 验证独立水位、连续确认、删除、跨工作区边界及重新入群。
func TestGroupMentionNavigation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	pending := conversationaction.NewListPendingConversationMentionsQuery(f.db)
	navigation := conversationaction.NewGetConversationNavigationStateQuery(f.db)
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	read := conversationaction.NewMarkConversationReadAction(f.db)
	first := f.send(t, f.owner, "第一条提及", false, f.subjectID)
	second := f.send(t, f.owner, "两种提及合并一次", true, f.subjectID)
	f.send(t, f.member, "自己的所有人消息", true)
	last := f.send(t, f.owner, "普通消息", false)
	queue, err := pending.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Equal(t, []string{first.ID, second.ID}, queue.MessageIDs)
	require.NotNil(t, queue.LastTargetSequence)
	require.Equal(t, second.MessageSeq, *queue.LastTargetSequence)
	_, err = read.Execute(ctx, f.member, f.groupID, last.ID, false)
	require.NoError(t, err)
	status, err := navigation.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Equal(t, 2, status.PendingMentionCount)
	require.Equal(t, int64(0), status.ReviewedThroughSequence)
	require.NotNil(t, status.LatestMessageID)
	require.Equal(t, last.ID, *status.LatestMessageID)
	_, err = review.Execute(ctx, f.member, f.groupID, last.ID)
	require.ErrorIs(t, err, conversationaction.ErrMentionTargetInvalid, "accepted ordinary message")
	result, err := review.Execute(ctx, f.member, f.groupID, first.ID)
	require.NoError(t, err)
	require.Equal(t, "reviewed", result.Outcome)
	result, err = review.Execute(ctx, f.member, f.groupID, first.ID)
	require.NoError(t, err)
	require.Equal(t, "alreadyReviewed", result.Outcome)
	state := f.state(t)
	require.NotNil(t, state.LastReadMessageID)
	require.Equal(t, last.ID, *state.LastReadMessageID)
	require.NotNil(t, state.LastReviewedMentionMessageID)
	require.Equal(t, first.ID, *state.LastReviewedMentionMessageID)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id IN (?)", bun.List([]string{first.ID, second.ID})).Exec(ctx)
	require.NoError(t, err)
	result, err = review.Execute(ctx, f.member, f.groupID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "unavailable", result.Outcome)
	require.Equal(t, first.MessageSeq, result.ReviewedThroughSequence)
	third := f.send(t, f.owner, "删除后继续", false, f.subjectID)
	result, err = review.Execute(ctx, f.member, f.groupID, third.ID)
	require.NoError(t, err)
	require.Equal(t, "reviewed", result.Outcome)
	_, err = read.Execute(ctx, f.member, f.groupID, first.ID, false)
	require.NoError(t, err)
	state = f.state(t)
	require.Equal(t, last.ID, *state.LastReadMessageID, "read regressed")
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true)
	require.NoError(t, err)
	f.send(t, f.owner, "离群前未查看", true)
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	for _, readQuery := range []func() error{
		func() error { _, err := pending.Execute(ctx, f.member, f.groupID); return err },
		func() error { _, err := navigation.Execute(ctx, f.member, f.groupID); return err },
		func() error { _, err := review.Execute(ctx, f.member, f.groupID, third.ID); return err },
	} {
		require.ErrorIs(t, readQuery(), conversationaction.ErrConversationNotFound, "removed member access")
	}
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	state = f.state(t)
	require.True(t, state.Muted)
	require.NotNil(t, state.LastReadMessageID)
	require.NotNil(t, state.LastReviewedMentionMessageID)
	require.Equal(t, *state.LastReviewedMentionMessageID, *state.LastReadMessageID)
	queue, err = pending.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Empty(t, queue.MessageIDs)
	archivedTarget := f.send(t, f.owner, "归档仍可查看", true)
	_, err = f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("status = ?", domain.ConversationStatusArchived).Where("id = ?", f.groupID).Exec(ctx)
	require.NoError(t, err)
	result, err = review.Execute(ctx, f.member, f.groupID, archivedTarget.ID)
	require.NoError(t, err)
	require.Equal(t, "reviewed", result.Outcome)
	// 核验群聊读取和确认操作的企业隔离。
	other := newNavigationFixture(t)
	_, err = pending.Execute(ctx, other.member, f.groupID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross-tenant queue")
	_, err = review.Execute(ctx, other.member, f.groupID, archivedTarget.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "cross-tenant review")
}

// TestGroupMessageContextAndOrder 验证双向窗口、首条定位、读历史不推进已读、删除引用和并发写入的稳定顺序。
func TestGroupMessageContextAndOrder(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	send := newGroupSendAction(f.db)
	messages := make([]conversationaction.ConversationMessage, 70)
	for index := range messages {
		messages[index] = f.send(t, f.owner, fmt.Sprintf("消息 %d", index), index == 30)
	}
	history := conversationaction.NewListConversationMessagesQuery(f.db)
	window, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: messages[35].ID})
	require.NoError(t, err)
	require.Len(t, window.Messages, 51)
	require.True(t, window.HasEarlier)
	require.True(t, window.HasLater)
	require.NotNil(t, window.Before)
	require.NotNil(t, window.After)
	require.Equal(t, messages[35].ID, window.Messages[25].ID)
	earlier, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Before: window.Before})
	require.NoError(t, err)
	require.False(t, earlier.HasEarlier)
	require.True(t, earlier.HasLater)
	require.Len(t, earlier.Messages, 10)
	later, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, After: window.After})
	require.NoError(t, err)
	require.False(t, later.HasLater)
	require.True(t, later.HasEarlier)
	require.Len(t, later.Messages, 9)
	// 目标为首条消息时窗口没有更早内容。
	oldest, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: messages[0].ID})
	require.NoError(t, err)
	require.False(t, oldest.HasEarlier)
	require.True(t, oldest.HasLater)
	require.Equal(t, messages[0].ID, oldest.Messages[0].ID)
	// 读取历史不推进已读。
	count, err := f.db.NewSelect().Model((*servermodels.ConversationUserState)(nil)).Where("cus.conversation_id = ? AND cus.user_id = ? AND cus.last_read_message_id IS NOT NULL", f.groupID, f.member.User.ID).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "history advanced read state")
	reply, err := send.Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "引用", ReplyToMessageID: messages[35].ID})
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", messages[35].ID).Exec(ctx)
	require.NoError(t, err)
	_, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: messages[35].ID})
	require.ErrorIs(t, err, conversationaction.ErrMessageUnavailable, "deleted context")
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: reply.ID})
	require.NoError(t, err)
	reference := window.Messages[len(window.Messages)-1].ReplyTo
	require.NotNil(t, reference)
	require.True(t, reference.Deleted)
	require.Empty(t, reference.Body)
	require.Equal(t, messages[35].ID, reference.ID)
	// 相同幂等键并发重试只占用一个序号，两个发送者共享同一递增序列。
	const writers = 12
	var wait sync.WaitGroup
	errs := make(chan error, writers)
	input := groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "幂等重试"}
	for index := range writers {
		wait.Go(func() {
			identity := f.owner
			current := input
			if index%2 == 1 {
				identity = f.member
				current.ClientMessageID = uuid.NewV7().String()
			}
			_, err := send.Execute(ctx, identity, current)
			errs <- err
		})
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var sequences []int64
	require.NoError(t, f.db.NewSelect().Model((*servermodels.Message)(nil)).Column("message_seq").Where("conversation_id = ?", f.groupID).Order("message_seq ASC").Scan(ctx, &sequences))
	require.Len(t, sequences, 78)
	for index, sequence := range sequences {
		require.Equal(t, int64(index+1), sequence, "sequence[%d]", index)
	}
	// 核验群聊最新页、游标和已读水位均按序号排序。
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("originated_at = ?", time.Now().Add(24*time.Hour)).Where("id = ?", messages[0].ID).Exec(ctx)
	require.NoError(t, err)
	latest, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
	require.NoError(t, err)
	require.Equal(t, int64(78), latest.Messages[len(latest.Messages)-1].MessageSeq, "latest order")
}

// TestConversationMessageWindowRange 验证按首尾游标重读包含两端的完整范围、删除后的边界与非法范围。
func TestConversationMessageWindowRange(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	messages := make([]conversationaction.ConversationMessage, 60)
	for index := range messages {
		messages[index] = f.send(t, f.owner, fmt.Sprintf("范围消息 %d", index), false)
	}
	history := conversationaction.NewListConversationMessagesQuery(f.db)
	point := func(message conversationaction.ConversationMessage) *conversationaction.MessageCursorPoint {
		return &conversationaction.MessageCursorPoint{ID: message.ID, MessageSeq: message.MessageSeq}
	}
	window, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[5]), End: point(messages[58])})
	require.NoError(t, err)
	require.Len(t, window.Messages, 54)
	require.Equal(t, messages[5].ID, window.Messages[0].ID)
	require.Equal(t, messages[58].ID, window.Messages[53].ID)
	require.True(t, window.HasEarlier)
	require.True(t, window.HasLater)
	// 删除两端消息后，边界收缩到仍可见的首尾消息，续读判断不变。
	deleted := []string{messages[5].ID, messages[30].ID, messages[58].ID}
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id IN (?)", bun.List(deleted)).Exec(ctx)
	require.NoError(t, err)
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[5]), End: point(messages[58])})
	require.NoError(t, err)
	require.Len(t, window.Messages, 51)
	require.NotNil(t, window.Before)
	require.Equal(t, messages[6].ID, window.Before.ID)
	require.NotNil(t, window.After)
	require.Equal(t, messages[57].ID, window.After.ID)
	require.True(t, window.HasEarlier)
	require.True(t, window.HasLater)
	require.False(t, slices.ContainsFunc(window.Messages, func(message conversationaction.ConversationMessage) bool { return message.ID == messages[30].ID }), "deleted message stays in window")
	// 范围内全部消息删除时保留请求的首尾游标。
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[58]), End: point(messages[58])})
	require.NoError(t, err)
	require.Empty(t, window.Messages)
	require.NotNil(t, window.Before)
	require.Equal(t, messages[58].ID, window.Before.ID)
	require.NotNil(t, window.After)
	require.Equal(t, messages[58].ID, window.After.ID)
	require.True(t, window.HasEarlier)
	require.True(t, window.HasLater)
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[0]), End: point(messages[59])})
	require.NoError(t, err)
	require.Len(t, window.Messages, 57)
	require.False(t, window.HasEarlier)
	require.False(t, window.HasLater)
	var validation *conversationaction.ValidationError
	for name, input := range map[string]conversationaction.ConversationMessageHistoryInput{
		"reversed":  {ConversationID: f.groupID, Start: point(messages[40]), End: point(messages[10])},
		"open end":  {ConversationID: f.groupID, Start: point(messages[10])},
		"mixed":     {ConversationID: f.groupID, Start: point(messages[10]), End: point(messages[20]), After: point(messages[10])},
		"no access": {ConversationID: uuid.NewV7().String(), Start: point(messages[10]), End: point(messages[20])},
	} {
		_, err := history.Execute(ctx, f.member, input)
		if name == "no access" {
			require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, name)
		} else {
			require.ErrorAs(t, err, &validation, name)
		}
	}
}

// navigationWriteBarrier 在写入指定正文的消息时暂停查询的屏障。
type navigationWriteBarrier struct {
	body             string
	entered, release chan struct{}
	once             sync.Once
}

// BeforeQuery 保留原查询上下文。
func (b *navigationWriteBarrier) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在消息插入后、事务提交前建立可控屏障。
func (b *navigationWriteBarrier) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if event.Model == nil {
		return
	}
	message, ok := event.Model.Value().(*servermodels.Message)
	if !ok || message.Body != b.body || event.Operation() != "INSERT" || event.Err != nil {
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

// TestGroupSequenceCommitBarrier 验证未提交消息阻止后续序号绕过，并发确认保持连续幂等。
func TestGroupSequenceCommitBarrier(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	barrier := &navigationWriteBarrier{body: "屏障内未提交提及", entered: make(chan struct{}), release: make(chan struct{})}
	// 查询钩子只在本测试专用连接上阻塞指定消息。
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })
	type sent struct {
		message conversationaction.ConversationMessage
		err     error
	}
	firstDone, secondDone := make(chan sent, 1), make(chan sent, 1)
	send := newGroupSendAction(f.db)
	go func() {
		message, err := send.Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: barrier.body, MentionAll: true})
		firstDone <- sent{message, err}
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		message, err := send.Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "后续提及", MentionAll: true})
		secondDone <- sent{message, err}
	}()
	waitForNavigationLock(t, ctx, f.db, f.groupID)
	navigation := conversationaction.NewGetConversationNavigationStateQuery(f.db)
	state, err := navigation.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Zero(t, state.PendingMentionCount, "uncommitted message visible")
	require.Zero(t, state.LatestSequence, "uncommitted message visible")
	release.Do(func() { close(barrier.release) })
	first, second := <-firstDone, <-secondDone
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, int64(1), first.message.MessageSeq, "commit order")
	require.Equal(t, int64(2), second.message.MessageSeq, "commit order")
	// 两端同时确认同一条提醒，恰好一次推进且两次都成功。
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	results := make(chan conversationaction.ConversationMentionReview, 2)
	errorsOut := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := review.Execute(ctx, f.member, f.groupID, first.message.ID)
			results <- result
			errorsOut <- err
		}()
	}
	outcomes := []string{(<-results).Outcome, (<-results).Outcome}
	require.NoError(t, <-errorsOut)
	require.NoError(t, <-errorsOut)
	slices.Sort(outcomes)
	require.Equal(t, []string{"alreadyReviewed", "reviewed"}, outcomes)
}

// waitForNavigationLock 等待语句中带有指定编号的写入确实发生数据库锁竞争。
func waitForNavigationLock(t *testing.T, ctx context.Context, db *bun.DB, lockedID string) {
	t.Helper()
	for {
		var waiting bool
		err := db.NewSelect().TableExpr("pg_stat_activity").ColumnExpr("EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE ?)", "%"+lockedID+"%").Limit(1).Scan(ctx, &waiting)
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestVisibleMentionsCanBeReviewedOutOfOrder 验证可视提及单独确认且不越过屏幕外的旧提及。
func TestVisibleMentionsCanBeReviewedOutOfOrder(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	pending := conversationaction.NewListPendingConversationMentionsQuery(f.db)
	navigation := conversationaction.NewGetConversationNavigationStateQuery(f.db)
	first := f.send(t, f.owner, "屏幕外的第一条", false, f.subjectID)
	second := f.send(t, f.owner, "屏幕外的第二条", true)
	third := f.send(t, f.owner, "当前可视的提及", false, f.subjectID)
	result, err := review.Execute(ctx, f.member, f.groupID, third.ID)
	require.NoError(t, err)
	require.Equal(t, "reviewed", result.Outcome)
	require.Zero(t, result.ReviewedThroughSequence)
	result, err = review.Execute(ctx, f.member, f.groupID, third.ID)
	require.NoError(t, err)
	require.Equal(t, "alreadyReviewed", result.Outcome)
	queue, err := pending.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Equal(t, []string{first.ID, second.ID}, queue.MessageIDs, "offscreen mentions lost")
	status, err := navigation.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Equal(t, 2, status.PendingMentionCount)
	result, err = review.Execute(ctx, f.member, f.groupID, first.ID)
	require.NoError(t, err)
	require.Equal(t, first.MessageSeq, result.ReviewedThroughSequence)
	result, err = review.Execute(ctx, f.member, f.groupID, second.ID)
	require.NoError(t, err)
	require.Equal(t, third.MessageSeq, result.ReviewedThroughSequence)
	var receipts int
	require.NoError(t, f.db.NewSelect().Model((*servermodels.ConversationMentionReview)(nil)).ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &receipts))
	require.Zero(t, receipts, "compacted receipts")
	state := f.state(t)
	require.Nil(t, state.LastReadMessageID, "mention review advanced ordinary read")
	fourth := f.send(t, f.owner, "随后删除的旧提及", true)
	fifth := f.send(t, f.owner, "再次可视的提及", true)
	_, err = review.Execute(ctx, f.member, f.groupID, fifth.ID)
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", fourth.ID).Exec(ctx)
	require.NoError(t, err)
	sixth := f.send(t, f.owner, "删除后继续确认", true)
	result, err = review.Execute(ctx, f.member, f.groupID, sixth.ID)
	require.NoError(t, err)
	require.Equal(t, sixth.MessageSeq, result.ReviewedThroughSequence, "deleted gap blocks review")
	// 重新入群建立新基线，并清除上一轮尚未合并的单条记录。
	f.send(t, f.owner, "离群前屏幕外", true)
	last := f.send(t, f.owner, "离群前已看到", true)
	_, err = review.Execute(ctx, f.member, f.groupID, last.ID)
	require.NoError(t, err)
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	queue, err = pending.Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Empty(t, queue.MessageIDs)
	require.NoError(t, f.db.NewSelect().Model((*servermodels.ConversationMentionReview)(nil)).ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &receipts))
	require.Zero(t, receipts, "rejoin receipts")
}
