//go:build server

package integrationtest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type groupAccessWrite struct {
	name    string
	execute func(context.Context, navigationFixture, string) error
}

// groupAccessWrites 列出需要群成员资格的消息和个人状态写入口。
func groupAccessWrites() []groupAccessWrite {
	return []groupAccessWrite{
		{"send", func(ctx context.Context, f navigationFixture, _ string) error {
			_, err := newGroupSendAction(f.db).Execute(ctx, f.member, groupchataction.GroupTextMessageInput{ConversationID: f.groupID, ClientMessageID: uuid.NewV7().String(), Body: "等待后发送"})
			return err
		}},
		{"mute", func(ctx context.Context, f navigationFixture, _ string) error {
			_, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true)
			return err
		}},
		{"read", func(ctx context.Context, f navigationFixture, messageID string) error {
			_, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.groupID, messageID, false)
			return err
		}},
		{"mention", func(ctx context.Context, f navigationFixture, messageID string) error {
			_, err := conversationaction.NewMarkConversationMentionReviewedAction(f.db).Execute(ctx, f.member, f.groupID, messageID)
			return err
		}},
		{"unread", func(ctx context.Context, f navigationFixture, _ string) error {
			return conversationaction.NewUpdateConversationUnreadMarkAction(f.db).Execute(ctx, f.member, f.groupID, true)
		}},
	}
}

// TestRemovedMemberCannotWriteAfterWaiting 验证各写入口等待移除事务后拒绝过期关系且不留下写入。
func TestRemovedMemberCannotWriteAfterWaiting(t *testing.T) {
	t.Parallel()
	for _, operation := range groupAccessWrites() {
		t.Run(operation.name, func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			message := f.send(t, f.owner, "待阅读提及", true)
			if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, false); err != nil {
				t.Fatal(err)
			}
			before := f.state(t)
			// 群系统事件的空正文用于命中屏障，此时成员退出关系尚未提交。
			barrier := &navigationWriteBarrier{entered: make(chan struct{}), release: make(chan struct{})}
			f.db.AddQueryHook(barrier)
			var release sync.Once
			defer release.Do(func() { close(barrier.release) })
			removed, written := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID})
				removed <- err
			}()
			select {
			case <-barrier.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// 未提交的移除不阻塞只读 Query，页面仍可能取得即将过期的群资料。
			if _, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID); err != nil {
				t.Fatal(err)
			}
			go func() { written <- operation.execute(ctx, f, message.ID) }()
			waitForNavigationLock(t, ctx, f.db, f.groupID)
			release.Do(func() { close(barrier.release) })
			if err := <-removed; err != nil {
				t.Fatal(err)
			}
			if err := <-written; !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("removed member write=%v", err)
			}
			if after := f.state(t); !reflect.DeepEqual(before, after) {
				t.Fatalf("personal state changed: before=%+v after=%+v", before, after)
			}
			var messages, receipts int
			if err := f.db.NewSelect().TableExpr("messages").ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &messages); err != nil {
				t.Fatal(err)
			}
			if err := f.db.NewSelect().TableExpr("conversation_mention_reviews").ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &receipts); err != nil {
				t.Fatal(err)
			}
			if messages != 2 || receipts != 0 {
				t.Fatalf("messages=%d receipts=%d", messages, receipts)
			}
			if _, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("removed member detail=%v", err)
			}
			if _, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("removed member history=%v", err)
			}
		})
	}
}

type groupSettingsBarrier struct {
	userID           string
	entered, release chan struct{}
	once             sync.Once
}

// BeforeQuery 保留个人设置写入上下文。
func (b *groupSettingsBarrier) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

// AfterQuery 在个人设置已经写入而尚未提交时暂停事务。
func (b *groupSettingsBarrier) AfterQuery(ctx context.Context, event *bun.QueryEvent) {
	if event.Model == nil {
		return
	}
	// 只按模型指向实际写入值的语句判断，按类型限定的语句不带个人状态。
	state, ok := event.Model.Value().(*servermodels.ConversationUserState)
	if !ok || state == nil || state.UserID != b.userID || event.Operation() != "INSERT" || event.Err != nil {
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

// TestGroupSettingsCommitBeforeRemoval 验证先取得群锁的个人设置完成后才允许移除成员。
func TestGroupSettingsCommitBeforeRemoval(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	barrier := &groupSettingsBarrier{userID: f.member.User.ID, entered: make(chan struct{}), release: make(chan struct{})}
	f.db.AddQueryHook(barrier)
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })
	muted, removed := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true)
		muted <- err
	}()
	select {
	case <-barrier.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID})
		removed <- err
	}()
	waitForNavigationLock(t, ctx, f.db, f.groupID)
	release.Do(func() { close(barrier.release) })
	if err := <-muted; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if state := f.state(t); !state.Muted {
		t.Fatalf("mute not committed: %+v", state)
	}
	if _, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, false); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("removed member mute=%v", err)
	}
}

// TestGroupOwnerTransferAndLeave 验证转让后的群主资格、普通成员退出及最后一位真人解散。
func TestGroupOwnerTransferAndLeave(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	update := groupchataction.NewUpdateGroupConversationAction(f.db)
	input := groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "转让后的群"}
	if _, err := update.Execute(ctx, f.member, input); !errors.Is(err, conversationaction.ErrGroupOwnerRequired) {
		t.Fatalf("member management=%v", err)
	}
	if _, err := groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{ConversationID: f.groupID, OwnerIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(ctx, f.owner, input); !errors.Is(err, conversationaction.ErrGroupOwnerRequired) {
		t.Fatalf("former owner management=%v", err)
	}
	if _, err := update.Execute(ctx, f.member, input); err != nil {
		t.Fatal(err)
	}
	if err := groupchataction.NewLeaveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, f.groupID); err != nil {
		t.Fatal(err)
	}
	detail, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID)
	if err != nil || detail.Status != domain.ConversationStatusArchived {
		t.Fatalf("dissolved group=%+v err=%v", detail, err)
	}
	if _, err := update.Execute(ctx, f.member, input); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("dissolved management=%v", err)
	}
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
	if err != nil || len(history.Messages) != 4 {
		t.Fatalf("dissolved history=%+v err=%v", history, err)
	}
}

// TestArchivedGroupAccessAfterWaiting 验证等待归档提交后的发送限制和个人阅读资格。
func TestArchivedGroupAccessAfterWaiting(t *testing.T) {
	t.Parallel()
	for _, operation := range groupAccessWrites() {
		t.Run(operation.name, func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			message := f.send(t, f.owner, "归档前待查看提及", true)
			// 独立事务持有归档写锁，使被测 Action 明确等待会话状态提交。
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("status = ?", domain.ConversationStatusArchived).
				Where("organization_id = ? AND id = ?", f.owner.Organization.ID, f.groupID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			written := make(chan error, 1)
			go func() { written <- operation.execute(ctx, f, message.ID) }()
			waitForNavigationLock(t, ctx, f.db, f.groupID)
			// 历史与提及查询只读取已提交快照，不等待会话写锁。
			if _, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID}); err != nil {
				t.Fatal(err)
			}
			if _, err := conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.member, f.groupID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			err = <-written
			if operation.name == "send" {
				if !errors.Is(err, conversationaction.ErrConversationNotFound) {
					t.Fatalf("archived send=%v", err)
				}
			} else if err != nil {
				t.Fatalf("archived %s=%v", operation.name, err)
			}
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
			if err != nil || len(history.Messages) != 1 || history.Messages[0].ID != message.ID {
				t.Fatalf("archived history=%+v err=%v", history, err)
			}
		})
	}
}
