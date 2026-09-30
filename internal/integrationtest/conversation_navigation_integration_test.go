//go:build server

package integrationtest

import (
	"context"
	"errors"
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
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// navigationFixture 是两人群聊测试工作区；成员邮箱在测试库内唯一，密码均为 password123，令牌为创建时签发的账号会话。
type navigationFixture struct {
	db                      *bun.DB
	owner, member           *servermodels.Identity
	ownerEmail, memberEmail string
	ownerToken, memberToken string
	groupID, subjectID      string
}

// newNavigationFixture 创建独立测试工作区并建立两人群聊。
func newNavigationFixture(t *testing.T) navigationFixture {
	t.Helper()
	ctx := context.Background()
	store, err := serverstorage.Open(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ownerEmail := uniqueEmail("owner")
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "导航测试", DisplayName: "群主", Email: ownerEmail, Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	owner := installed.Identity
	memberEmail := uniqueEmail("member")
	_, err = newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	if err != nil {
		t.Fatal(err)
	}
	login := loginMember(t, db, owner.Organization.ID, memberEmail, "password123")
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, owner, groupchataction.GroupConversationInput{Title: "导航测试群", MemberIdentityIDs: []string{login.Identity.OrganizationIdentity.ID}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, owner, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := navigationFixture{
		db: db, owner: owner, member: login.Identity, ownerEmail: ownerEmail, memberEmail: memberEmail,
		ownerToken: installed.Token, memberToken: login.Token, groupID: group.ID,
	}
	for _, participant := range detail.Participants {
		if participant.IdentityID == fixture.member.OrganizationIdentity.ID {
			fixture.subjectID = participant.ChatSubjectID
		}
	}
	return fixture
}

// send 通过真实发送命令写入测试消息。
func (f navigationFixture) send(t *testing.T, identity *servermodels.Identity, body string, all bool, subjects ...string) conversationaction.ConversationMessage {
	t.Helper()
	message, err := newGroupSendAction(f.db).Execute(context.Background(), identity, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: body, MentionAll: all, MentionSubjectIDs: subjects})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

// state 读取个人水位，保留被删除消息的指针。
func (f navigationFixture) state(t *testing.T) servermodels.ConversationUserState {
	t.Helper()
	var state servermodels.ConversationUserState
	if err := f.db.NewSelect().Model(&state).Where("cus.organization_id = ? AND cus.conversation_id = ? AND cus.user_id = ?", f.owner.Organization.ID, f.groupID, f.member.User.ID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	if err != nil || !slices.Equal(queue.MessageIDs, []string{first.ID, second.ID}) || queue.LastTargetSequence == nil || *queue.LastTargetSequence != second.MessageSeq {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
	if _, err := read.Execute(ctx, f.member, f.groupID, last.ID, false); err != nil {
		t.Fatal(err)
	}
	status, err := navigation.Execute(ctx, f.member, f.groupID)
	if err != nil || status.PendingMentionCount != 2 || status.ReviewedThroughSequence != 0 || status.LatestMessageID == nil || *status.LatestMessageID != last.ID {
		t.Fatalf("navigation=%+v err=%v", status, err)
	}
	if _, err := review.Execute(ctx, f.member, f.groupID, last.ID); !errors.Is(err, conversationaction.ErrMentionTargetInvalid) {
		t.Fatalf("accepted ordinary message: %v", err)
	}
	result, err := review.Execute(ctx, f.member, f.groupID, first.ID)
	if err != nil || result.Outcome != "reviewed" {
		t.Fatalf("review=%+v err=%v", result, err)
	}
	result, err = review.Execute(ctx, f.member, f.groupID, first.ID)
	if err != nil || result.Outcome != "alreadyReviewed" {
		t.Fatalf("repeated review=%+v err=%v", result, err)
	}
	state := f.state(t)
	if state.LastReadMessageID == nil || *state.LastReadMessageID != last.ID || state.LastReviewedMentionMessageID == nil || *state.LastReviewedMentionMessageID != first.ID {
		t.Fatalf("watermarks coupled: %+v", state)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id IN (?)", bun.In([]string{first.ID, second.ID})).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = review.Execute(ctx, f.member, f.groupID, second.ID)
	if err != nil || result.Outcome != "unavailable" || result.ReviewedThroughSequence != first.MessageSeq {
		t.Fatalf("deleted review=%+v err=%v", result, err)
	}
	third := f.send(t, f.owner, "删除后继续", false, f.subjectID)
	result, err = review.Execute(ctx, f.member, f.groupID, third.ID)
	if err != nil || result.Outcome != "reviewed" {
		t.Fatalf("review after deletion=%+v err=%v", result, err)
	}
	if _, err := read.Execute(ctx, f.member, f.groupID, first.ID, false); err != nil {
		t.Fatal(err)
	}
	if state = f.state(t); *state.LastReadMessageID != last.ID {
		t.Fatalf("read regressed: %+v", state)
	}
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	f.send(t, f.owner, "离群前未查看", true)
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	for _, readQuery := range []func() error{
		func() error { _, err := pending.Execute(ctx, f.member, f.groupID); return err },
		func() error { _, err := navigation.Execute(ctx, f.member, f.groupID); return err },
		func() error { _, err := review.Execute(ctx, f.member, f.groupID, third.ID); return err },
	} {
		if err := readQuery(); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("removed member access: %v", err)
		}
	}
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}}); err != nil {
		t.Fatal(err)
	}
	state = f.state(t)
	if !state.Muted || state.LastReadMessageID == nil || state.LastReviewedMentionMessageID == nil || *state.LastReadMessageID != *state.LastReviewedMentionMessageID {
		t.Fatalf("rejoin state=%+v", state)
	}
	queue, err = pending.Execute(ctx, f.member, f.groupID)
	if err != nil || len(queue.MessageIDs) != 0 {
		t.Fatalf("rejoin queue=%+v err=%v", queue, err)
	}
	archivedTarget := f.send(t, f.owner, "归档仍可查看", true)
	if _, err := f.db.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("status = ?", domain.ConversationStatusArchived).Where("id = ?", f.groupID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = review.Execute(ctx, f.member, f.groupID, archivedTarget.ID)
	if err != nil || result.Outcome != "reviewed" {
		t.Fatalf("archived review=%+v err=%v", result, err)
	}
	// 核验群聊读取和确认操作的企业隔离。
	other := newNavigationFixture(t)
	if _, err := pending.Execute(ctx, other.member, f.groupID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("cross-tenant queue: %v", err)
	}
	if _, err := review.Execute(ctx, other.member, f.groupID, archivedTarget.ID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("cross-tenant review: %v", err)
	}
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
	if err != nil || len(window.Messages) != 51 || !window.HasEarlier || !window.HasLater || window.Before == nil || window.After == nil || window.Messages[25].ID != messages[35].ID {
		t.Fatalf("context=%+v err=%v", window, err)
	}
	earlier, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Before: window.Before})
	if err != nil || earlier.HasEarlier || !earlier.HasLater || len(earlier.Messages) != 10 {
		t.Fatalf("earlier=%+v err=%v", earlier, err)
	}
	later, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, After: window.After})
	if err != nil || later.HasLater || !later.HasEarlier || len(later.Messages) != 9 {
		t.Fatalf("later=%+v err=%v", later, err)
	}
	// 目标为首条消息时窗口没有更早内容。
	oldest, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: messages[0].ID})
	if err != nil || oldest.HasEarlier || !oldest.HasLater || oldest.Messages[0].ID != messages[0].ID {
		t.Fatalf("oldest=%+v err=%v", oldest, err)
	}
	// 读取历史不推进已读。
	if count, err := f.db.NewSelect().Model((*servermodels.ConversationUserState)(nil)).Where("cus.conversation_id = ? AND cus.user_id = ? AND cus.last_read_message_id IS NOT NULL", f.groupID, f.member.User.ID).Count(ctx); err != nil || count != 0 {
		t.Fatalf("history advanced read state: count=%d err=%v", count, err)
	}
	reply, err := send.Execute(ctx, f.owner, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "引用", ReplyToMessageID: messages[35].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", messages[35].ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: messages[35].ID}); !errors.Is(err, conversationaction.ErrMessageUnavailable) {
		t.Fatalf("deleted context: %v", err)
	}
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, AroundMessageID: reply.ID})
	if err != nil {
		t.Fatal(err)
	}
	reference := window.Messages[len(window.Messages)-1].ReplyTo
	if reference == nil || !reference.Deleted || reference.Body != "" || reference.ID != messages[35].ID {
		t.Fatalf("deleted reference=%+v", reference)
	}
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
		if err != nil {
			t.Fatal(err)
		}
	}
	var sequences []int64
	if err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Column("message_seq").Where("conversation_id = ?", f.groupID).Order("message_seq ASC").Scan(ctx, &sequences); err != nil {
		t.Fatal(err)
	}
	if len(sequences) != 78 {
		t.Fatalf("message count=%d want78", len(sequences))
	}
	for index, sequence := range sequences {
		if sequence != int64(index+1) {
			t.Fatalf("sequence[%d]=%d", index, sequence)
		}
	}
	// 核验群聊最新页、游标和已读水位均按序号排序。
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("originated_at = ?", time.Now().Add(24*time.Hour)).Where("id = ?", messages[0].ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	latest, err := history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
	if err != nil || latest.Messages[len(latest.Messages)-1].MessageSeq != 78 {
		t.Fatalf("latest order err=%v", err)
	}
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
	if err != nil || len(window.Messages) != 54 || window.Messages[0].ID != messages[5].ID || window.Messages[53].ID != messages[58].ID || !window.HasEarlier || !window.HasLater {
		t.Fatalf("window count=%d err=%v", len(window.Messages), err)
	}
	// 删除两端消息后，边界收缩到仍可见的首尾消息，续读判断不变。
	deleted := []string{messages[5].ID, messages[30].ID, messages[58].ID}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id IN (?)", bun.In(deleted)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[5]), End: point(messages[58])})
	if err != nil || len(window.Messages) != 51 || window.Before == nil || window.Before.ID != messages[6].ID || window.After == nil || window.After.ID != messages[57].ID || !window.HasEarlier || !window.HasLater {
		t.Fatalf("deleted window=%+v err=%v", window.Before, err)
	}
	if slices.ContainsFunc(window.Messages, func(message conversationaction.ConversationMessage) bool { return message.ID == messages[30].ID }) {
		t.Fatal("deleted message stays in window")
	}
	// 范围内全部消息删除时保留请求的首尾游标。
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[58]), End: point(messages[58])})
	if err != nil || len(window.Messages) != 0 || window.Before == nil || window.Before.ID != messages[58].ID || window.After == nil || window.After.ID != messages[58].ID || !window.HasEarlier || !window.HasLater {
		t.Fatalf("empty window=%+v err=%v", window, err)
	}
	window, err = history.Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID, Start: point(messages[0]), End: point(messages[59])})
	if err != nil || len(window.Messages) != 57 || window.HasEarlier || window.HasLater {
		t.Fatalf("full window count=%d err=%v", len(window.Messages), err)
	}
	var validation *conversationaction.ValidationError
	for name, input := range map[string]conversationaction.ConversationMessageHistoryInput{
		"reversed":  {ConversationID: f.groupID, Start: point(messages[40]), End: point(messages[10])},
		"open end":  {ConversationID: f.groupID, Start: point(messages[10])},
		"mixed":     {ConversationID: f.groupID, Start: point(messages[10]), End: point(messages[20]), After: point(messages[10])},
		"no access": {ConversationID: uuid.NewV7().String(), Start: point(messages[10]), End: point(messages[20])},
	} {
		_, err := history.Execute(ctx, f.member, input)
		if name == "no access" {
			if !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("%s: %v", name, err)
			}
		} else if !errors.As(err, &validation) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

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
	if err != nil || state.PendingMentionCount != 0 || state.LatestSequence != 0 {
		t.Fatalf("uncommitted message visible: %+v %v", state, err)
	}
	release.Do(func() { close(barrier.release) })
	first, second := <-firstDone, <-secondDone
	if first.err != nil || second.err != nil || first.message.MessageSeq != 1 || second.message.MessageSeq != 2 {
		t.Fatalf("commit order: %+v %+v", first, second)
	}
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
	if err := <-errorsOut; err != nil {
		t.Fatal(err)
	}
	if err := <-errorsOut; err != nil {
		t.Fatal(err)
	}
	slices.Sort(outcomes)
	if !slices.Equal(outcomes, []string{"alreadyReviewed", "reviewed"}) {
		t.Fatalf("concurrent review outcomes=%v", outcomes)
	}
}

// waitForNavigationLock 等待语句中带有指定编号的写入确实发生数据库锁竞争。
func waitForNavigationLock(t *testing.T, ctx context.Context, db *bun.DB, lockedID string) {
	t.Helper()
	for {
		var waiting bool
		err := db.NewSelect().TableExpr("pg_stat_activity").ColumnExpr("EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE ?)", "%"+lockedID+"%").Limit(1).Scan(ctx, &waiting)
		if err != nil {
			t.Fatal(err)
		}
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
	if err != nil || result.Outcome != "reviewed" || result.ReviewedThroughSequence != 0 {
		t.Fatalf("out-of-order review=%+v err=%v", result, err)
	}
	result, err = review.Execute(ctx, f.member, f.groupID, third.ID)
	if err != nil || result.Outcome != "alreadyReviewed" {
		t.Fatalf("repeated sparse review=%+v err=%v", result, err)
	}
	queue, err := pending.Execute(ctx, f.member, f.groupID)
	if err != nil || !slices.Equal(queue.MessageIDs, []string{first.ID, second.ID}) {
		t.Fatalf("offscreen mentions lost: %+v err=%v", queue, err)
	}
	status, err := navigation.Execute(ctx, f.member, f.groupID)
	if err != nil || status.PendingMentionCount != 2 {
		t.Fatalf("sparse count=%+v err=%v", status, err)
	}
	result, err = review.Execute(ctx, f.member, f.groupID, first.ID)
	if err != nil || result.ReviewedThroughSequence != first.MessageSeq {
		t.Fatalf("first review=%+v err=%v", result, err)
	}
	result, err = review.Execute(ctx, f.member, f.groupID, second.ID)
	if err != nil || result.ReviewedThroughSequence != third.MessageSeq {
		t.Fatalf("merged review=%+v err=%v", result, err)
	}
	var receipts int
	if err := f.db.NewSelect().Model((*servermodels.ConversationMentionReview)(nil)).ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &receipts); err != nil || receipts != 0 {
		t.Fatalf("compacted receipts=%d err=%v", receipts, err)
	}
	state := f.state(t)
	if state.LastReadMessageID != nil {
		t.Fatalf("mention review advanced ordinary read: %+v", state)
	}
	fourth := f.send(t, f.owner, "随后删除的旧提及", true)
	fifth := f.send(t, f.owner, "再次可视的提及", true)
	if _, err := review.Execute(ctx, f.member, f.groupID, fifth.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Model((*servermodels.Message)(nil)).Set("deleted_at = now()").Where("id = ?", fourth.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sixth := f.send(t, f.owner, "删除后继续确认", true)
	result, err = review.Execute(ctx, f.member, f.groupID, sixth.ID)
	if err != nil || result.ReviewedThroughSequence != sixth.MessageSeq {
		t.Fatalf("deleted gap blocks review=%+v err=%v", result, err)
	}
	// 重新入群建立新基线，并清除上一轮尚未合并的单条记录。
	f.send(t, f.owner, "离群前屏幕外", true)
	last := f.send(t, f.owner, "离群前已看到", true)
	if _, err := review.Execute(ctx, f.member, f.groupID, last.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}}); err != nil {
		t.Fatal(err)
	}
	queue, err = pending.Execute(ctx, f.member, f.groupID)
	if err != nil || len(queue.MessageIDs) != 0 {
		t.Fatalf("rejoin queue=%+v err=%v", queue, err)
	}
	if err := f.db.NewSelect().Model((*servermodels.ConversationMentionReview)(nil)).ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &receipts); err != nil || receipts != 0 {
		t.Fatalf("rejoin receipts=%d err=%v", receipts, err)
	}
}
