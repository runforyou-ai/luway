//go:build server

package integrationtest

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type chatQueryGateKey struct{}

type chatQueryGate struct {
	match            func(*bun.QueryEvent) bool
	before           bool
	occurrence, seen int
	reached, release chan struct{}
	once             sync.Once
}

type chatQueryHook struct{}

// BeforeQuery 在指定查询取锁前暂停测试事务。
func (chatQueryHook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if gate, ok := ctx.Value(chatQueryGateKey{}).(*chatQueryGate); ok {
		gate.pause(ctx, event, true)
	}
	return ctx
}

// AfterQuery 在指定查询执行完成后暂停测试事务。
func (chatQueryHook) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if gate, ok := ctx.Value(chatQueryGateKey{}).(*chatQueryGate); ok {
		gate.pause(ctx, event, false)
	}
}

// pause 用查询屏障控制事务交错。
func (g *chatQueryGate) pause(ctx context.Context, event *bun.QueryEvent, before bool) {
	if before != g.before || !g.match(event) {
		return
	}
	g.seen++
	if g.seen != g.occurrence {
		return
	}
	close(g.reached)
	select {
	case <-g.release:
	case <-ctx.Done():
	}
}

// open 释放测试事务，允许清理路径重复调用。
func (g *chatQueryGate) open() { g.once.Do(func() { close(g.release) }) }

// newChatQueryGate 创建由当前测试负责释放的查询屏障。
func newChatQueryGate(t *testing.T, before bool, occurrence int, match func(*bun.QueryEvent) bool) *chatQueryGate {
	t.Helper()
	gate := &chatQueryGate{match: match, before: before, occurrence: occurrence, reached: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(gate.open)
	return gate
}

// waitChatSignal 等待查询屏障到达，并将超时报告在测试调用处。
func waitChatSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// waitChatResult 取得异步 Action 的执行结果。
func waitChatResult(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ctx.Err()
	}
}

// waitConversationLock 确认另一个事务正在等待指定会话行。
func waitConversationLock(t *testing.T, ctx context.Context, db *bun.DB, conversationID string) {
	t.Helper()
	waitChatDatabaseLock(t, ctx, db, `FROM "conversations"`, conversationID)
}

// waitChatDatabaseLock 通过数据库阻塞关系确认指定业务行上的锁等待。
func waitChatDatabaseLock(t *testing.T, ctx context.Context, db *bun.DB, queryPart, id string) {
	t.Helper()
	for {
		var waiting bool
		err := db.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND pid <> pg_backend_pid()
				AND wait_event_type = 'Lock' AND cardinality(pg_blocking_pids(pid)) > 0
				AND query LIKE ? AND query LIKE ?
		)`, "%"+id+"%", "%"+queryPart+"%").Scan(ctx, &waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}

// newChatLockUser 创建尚未拥有聊天主体的测试用户。
func newChatLockUser(t *testing.T, db *bun.DB, owner *servermodels.Identity) *servermodels.Identity {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewV7().String()
	email := uniqueEmail("chat-lock")
	_, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "锁定成员 " + id, Email: email, Password: "password123", RoleID: owner.User.RoleID})
	if err != nil {
		t.Fatal(err)
	}
	return loginMember(t, db, owner.Organization.ID, email, "password123").Identity
}

// TestDirectFirstMessagesConverge 验证主体竞争和身份对竞争均收敛为一份长期单聊。
func TestDirectFirstMessagesConverge(t *testing.T) {
	t.Parallel()
	for _, existingSubjects := range []bool{false, true} {
		name := "创建共享主体"
		if existingSubjects {
			name = "竞争唯一身份对"
		}
		t.Run(name, func(t *testing.T) {
			f := newNavigationFixture(t)
			actors := []*servermodels.Identity{f.owner, f.member}
			if !existingSubjects {
				actors = []*servermodels.Identity{newChatLockUser(t, f.db, f.owner), newChatLockUser(t, f.db, f.owner)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			f.db.AddQueryHook(chatQueryHook{})
			gates := make([]*chatQueryGate, 2)
			results := make([]directchataction.FirstDirectTextMessageResult, 2)
			done := make(chan error, 2)
			for i := range actors {
				gates[i] = newChatQueryGate(t, true, 1, func(event *bun.QueryEvent) bool {
					table := `"chat_subjects"`
					if existingSubjects {
						table = `"direct_conversations"`
					}
					return event.Operation() == "INSERT" && strings.Contains(event.Query, table)
				})
				go func() {
					var err error
					results[i], err = directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(context.WithValue(ctx, chatQueryGateKey{}, gates[i]), actors[i], directchataction.FirstDirectTextMessageInput{TargetIdentityID: actors[1-i].OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "并发首发"})
					done <- err
				}()
			}
			for _, gate := range gates {
				waitChatSignal(t, ctx, gate.reached)
			}
			for _, gate := range gates {
				gate.open()
			}
			for range actors {
				if err := waitChatResult(t, ctx, done); err != nil {
					t.Fatal(err)
				}
			}
			if results[0].Conversation.ID != results[1].Conversation.ID || results[0].Message.ID == results[1].Message.ID {
				t.Fatalf("first messages did not converge: %+v", results)
			}
			for table, want := range map[string]int{"direct_conversations": 1, "conversation_participants": 2, "messages": 2} {
				count, err := f.db.NewSelect().TableExpr(table).Where("organization_id = ? AND conversation_id = ?", f.owner.Organization.ID, results[0].Conversation.ID).Count(ctx)
				if err != nil || count != want {
					t.Fatalf("%s count=%d err=%v", table, count, err)
				}
			}
			count, err := f.db.NewSelect().Model((*servermodels.Conversation)(nil)).Where("cv.organization_id = ? AND cv.type = ?", f.owner.Organization.ID, domain.ConversationTypeDirect).Count(ctx)
			if err != nil || count != 1 {
				t.Fatalf("direct conversations=%d err=%v", count, err)
			}
		})
	}
}

// TestDirectSendRechecksAccessAfterWaiting 验证等锁后的归档、停用和参与关系检查先于幂等返回。
func TestDirectSendRechecksAccessAfterWaiting(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"归档", "目标停用", "参与者离开"} {
		t.Run(change, func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			clientID := uuid.NewV7().String()
			first, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: clientID, Body: "第一条"})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var cv servermodels.Conversation
			if err := tx.NewSelect().Model(&cv).Where("cv.id = ?", first.Conversation.ID).For("UPDATE").Scan(ctx); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := directchataction.NewSendDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: cv.ID, ClientMessageID: clientID, Body: "第一条"})
				done <- err
			}()
			waitConversationLock(t, ctx, f.db, cv.ID)
			switch change {
			case "归档":
				_, err = tx.NewUpdate().Model(&cv).Set("status = ?", domain.ConversationStatusArchived).WherePK().Exec(ctx)
			case "目标停用":
				_, err = tx.NewUpdate().Model((*servermodels.User)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("id = ?", f.member.User.ID).Exec(ctx)
			case "参与者离开":
				_, err = tx.NewUpdate().Model((*servermodels.ConversationParticipant)(nil)).Set("left_at = now()").Where("conversation_id = ? AND subject_id = ?", cv.ID, *cv.CreatedBySubjectID).Exec(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := waitChatResult(t, ctx, done); !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("send after %s=%v", change, err)
			}
			if change == "归档" {
				restored, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: clientID, Body: "第一条"})
				if err != nil || restored.Conversation.ID != cv.ID || restored.Message.ID != first.Message.ID {
					t.Fatalf("explicit restore=%+v err=%v", restored, err)
				}
			}
		})
	}
}

// TestSharedSubjectsAcrossDirectAndGroup 验证真人首发与建群对共享主体使用相同的创建顺序。
func TestSharedSubjectsAcrossDirectAndGroup(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	first, second := newChatLockUser(t, f.db, f.owner), newChatLockUser(t, f.db, f.owner)
	if first.OrganizationIdentity.ID > second.OrganizationIdentity.ID {
		first, second = second, first
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"chat_subjects"`)
	})
	directDone, groupDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), first, directchataction.FirstDirectTextMessageInput{TargetIdentityID: second.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "并发创建主体"})
		directDone <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, second, groupchataction.GroupConversationInput{Title: "共享主体测试", MemberIdentityIDs: []string{first.OrganizationIdentity.ID}})
		groupDone <- err
	}()
	waitChatDatabaseLock(t, ctx, f.db, `INSERT INTO "chat_subjects"`, first.OrganizationIdentity.ID)
	gate.open()
	for _, done := range []<-chan error{directDone, groupDone} {
		if err := waitChatResult(t, ctx, done); err != nil {
			t.Fatal(err)
		}
	}
	count, err := f.db.NewSelect().Model((*servermodels.ChatSubject)(nil)).Where("cs.organization_id = ? AND cs.source_id IN (?, ?)", f.owner.Organization.ID, first.OrganizationIdentity.ID, second.OrganizationIdentity.ID).Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("shared subjects=%d err=%v", count, err)
	}
}

// TestDirectSendAcceptedBeforeTargetDisabled 验证已通过资格校验的发送完成后才拒绝后续发送。
func TestDirectSendAcceptedBeforeTargetDisabled(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "第一条"})
	if err != nil {
		t.Fatal(err)
	}
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "INSERT" && strings.Contains(event.Query, `"messages"`)
	})
	send := directchataction.NewSendDirectTextMessageAction(f.db)
	input := directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "校验已通过"}
	done := make(chan error, 1)
	go func() {
		_, err := send.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), f.owner, input)
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	// 停用在会话锁上等待已通过校验的发送提交，再推进展示该成员的会话版本。
	disabled := make(chan error, 1)
	go func() {
		_, err := testUserStatusAction(f.db).Execute(ctx, f.member, f.member.User.ID, domain.IdentityStatusInactive)
		disabled <- err
	}()
	waitChatDatabaseLock(t, ctx, f.db, "FROM conversations", f.member.OrganizationIdentity.ID)
	gate.open()
	for _, result := range []<-chan error{done, disabled} {
		if err := waitChatResult(t, ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	// 核验消息重放时的账号发送资格校验。
	if _, err := send.Execute(ctx, f.owner, input); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("replay after disabled=%v", err)
	}
	count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", first.Conversation.ID).Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("accepted messages=%d err=%v", count, err)
	}
}
