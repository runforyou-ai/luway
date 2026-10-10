//go:build server

package integrationtest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestDissolveGroupPreservesMembers 验证主动解散权限、幂等和全体当前成员的只读历史。
func TestDissolveGroupPreservesMembers(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	dissolve := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db))
	_, err := dissolve.Execute(ctx, f.member, f.groupID)
	require.ErrorIs(t, err, conversationaction.ErrGroupOwnerRequired, "member dissolve")
	foreign := newNavigationFixture(t)
	_, err = dissolve.Execute(ctx, foreign.owner, f.groupID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "foreign dissolve")
	message := f.send(t, f.owner, "解散后保留的消息", true)
	for range 2 {
		group, err := dissolve.Execute(ctx, f.owner, f.groupID)
		require.NoError(t, err)
		require.Equal(t, domain.ConversationStatusArchived, group.Status)
		require.Len(t, group.Participants, 2)
	}
	for _, identity := range []*servermodels.Identity{f.owner, f.member} {
		history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
		require.NoError(t, err)
		require.Len(t, history.Messages, 2)
		require.Equal(t, message.ID, history.Messages[0].ID)
		require.NotNil(t, history.Messages[1].SystemEvent)
		require.Equal(t, domain.ConversationSystemEventGroupDissolved, history.Messages[1].SystemEvent.Type)
		_, err = groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, identity, f.groupID)
		require.NoError(t, err)
	}
	_, err = dissolve.Execute(ctx, f.member, f.groupID)
	require.ErrorIs(t, err, conversationaction.ErrGroupOwnerRequired, "archived member dissolve")
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "archived add")
	for _, operation := range groupAccessWrites() {
		err := operation.execute(ctx, f, message.ID)
		if operation.name == "send" {
			require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "archived send")
		} else {
			require.NoError(t, err, "archived %s", operation.name)
		}
	}
}

// TestDissolveDoesNotRestoreFormerMember 验证群聊解散后退出成员的历史访问限制。
func TestDissolveDoesNotRestoreFormerMember(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	leave := groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db))
	require.NoError(t, leave.Execute(ctx, f.member, f.groupID))
	var conflict *conversationaction.ConflictError
	err := leave.Execute(ctx, f.owner, f.groupID)
	require.ErrorAs(t, err, &conflict, "last owner leave")
	require.Equal(t, groupchataction.ConflictReasonGroupOwnerCannotLeave, conflict.Reason, "last owner leave")
	_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID)
	require.NoError(t, err)
	_, err = groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "former member access")
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
					_, err = groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{ConversationID: f.groupID, OwnerIdentityID: f.member.WorkspaceIdentity.ID})
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
					_, err = groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{ConversationID: f.groupID, OwnerIdentityID: f.member.WorkspaceIdentity.ID})
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
			require.NoError(t, <-dissolved)
			err := <-written
			switch operation {
			case "dissolve":
				require.NoError(t, err)
			case "transfer_first":
				require.ErrorIs(t, err, conversationaction.ErrGroupOwnerRequired, "former owner dissolve")
			default:
				require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "write after dissolve")
			}
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
			expectedEvent := domain.ConversationSystemEventGroupDissolved
			if operation == "transfer_first" {
				expectedEvent = domain.ConversationSystemEventGroupOwnerTransferred
			}
			require.NoError(t, err)
			require.Len(t, history.Messages, 1)
			require.NotNil(t, history.Messages[0].SystemEvent)
			require.Equal(t, expectedEvent, history.Messages[0].SystemEvent.Type)
		})
	}
}
