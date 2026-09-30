//go:build server

package integrationtest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// TestDissolveGroupPreservesMembers 验证主动解散权限、幂等和全体当前成员的只读历史。
func TestDissolveGroupPreservesMembers(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	dissolve := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db))
	if _, err := dissolve.Execute(ctx, f.member, f.groupID); !errors.Is(err, conversationaction.ErrGroupOwnerRequired) {
		t.Fatalf("member dissolve=%v", err)
	}
	foreign := newNavigationFixture(t)
	if _, err := dissolve.Execute(ctx, foreign.owner, f.groupID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("foreign dissolve=%v", err)
	}
	message := f.send(t, f.owner, "解散后保留的消息", true)
	for range 2 {
		group, err := dissolve.Execute(ctx, f.owner, f.groupID)
		if err != nil || group.Status != domain.ConversationStatusArchived || len(group.Participants) != 2 {
			t.Fatalf("dissolve=%+v err=%v", group, err)
		}
	}
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		if err != nil || len(history.Messages) != 2 || history.Messages[0].ID != message.ID || history.Messages[1].SystemEvent == nil || history.Messages[1].SystemEvent.Type != domain.ConversationSystemEventGroupDissolved {
			t.Fatalf("history=%+v err=%v", history, err)
		}
		if _, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, identity, f.groupID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dissolve.Execute(ctx, f.member, f.groupID); !errors.Is(err, conversationaction.ErrGroupOwnerRequired) {
		t.Fatalf("archived member dissolve=%v", err)
	}
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("archived add=%v", err)
	}
	for _, operation := range groupAccessWrites() {
		err := operation.execute(ctx, f, message.ID)
		if operation.name == "send" {
			if !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("archived send=%v", err)
			}
		} else if err != nil {
			t.Fatalf("archived %s=%v", operation.name, err)
		}
	}
}

// TestDissolveDoesNotRestoreFormerMember 验证群聊解散后退出成员的历史访问限制。
func TestDissolveDoesNotRestoreFormerMember(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	leave := groupchataction.NewLeaveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db))
	if err := leave.Execute(ctx, f.member, f.groupID); err != nil {
		t.Fatal(err)
	}
	var conflict *conversationaction.ConflictError
	if err := leave.Execute(ctx, f.owner, f.groupID); !errors.As(err, &conflict) || conflict.Reason != groupchataction.ConflictReasonGroupOwnerCannotLeave {
		t.Fatalf("last owner leave=%v", err)
	}
	if _, err := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("former member access=%v", err)
	}
}

// TestDissolveSerializesWithGroupWrites 验证解散与发送、转让及重复解散按会话锁串行提交。
func TestDissolveSerializesWithGroupWrites(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"send", "transfer", "dissolve", "transfer_first"} {
		t.Run(operation, func(t *testing.T) {
			f := newNavigationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			barrier := &navigationWriteBarrier{entered: make(chan struct{}), release: make(chan struct{})}
			f.db.AddQueryHook(barrier)
			var release sync.Once
			defer release.Do(func() { close(barrier.release) })
			dissolved, written := make(chan error, 1), make(chan error, 1)
			go func() {
				var err error
				if operation == "transfer_first" {
					_, err = groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{ConversationID: f.groupID, OwnerIdentityID: f.member.OrganizationIdentity.ID})
				} else {
					_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID)
				}
				dissolved <- err
			}()
			select {
			case <-barrier.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			go func() {
				var err error
				switch operation {
				case "send":
					err = groupAccessWrites()[0].execute(ctx, f, "")
				case "transfer":
					_, err = groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{ConversationID: f.groupID, OwnerIdentityID: f.member.OrganizationIdentity.ID})
				case "dissolve", "transfer_first":
					_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID)
				}
				written <- err
			}()
			// 同一群主的并发写先等待用户锁，不同成员发送等待会话锁。
			lockID := f.groupID
			if operation != "send" {
				lockID = f.owner.User.ID
			}
			waitForNavigationLock(t, ctx, f.db, lockID)
			release.Do(func() { close(barrier.release) })
			if err := <-dissolved; err != nil {
				t.Fatal(err)
			}
			err := <-written
			if operation == "dissolve" {
				if err != nil {
					t.Fatal(err)
				}
			} else if operation == "transfer_first" {
				if !errors.Is(err, conversationaction.ErrGroupOwnerRequired) {
					t.Fatalf("former owner dissolve=%v", err)
				}
			} else if !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("write after dissolve=%v", err)
			}
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
			expectedEvent := domain.ConversationSystemEventGroupDissolved
			if operation == "transfer_first" {
				expectedEvent = domain.ConversationSystemEventGroupOwnerTransferred
			}
			if err != nil || len(history.Messages) != 1 || history.Messages[0].SystemEvent == nil || history.Messages[0].SystemEvent.Type != expectedEvent {
				t.Fatalf("history=%+v err=%v", history, err)
			}
		})
	}
}
