//go:build server

package integrationtest

import (
	"context"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/require"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// groupAccessWrite 是一个需要群成员资格的写入口。
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
			_, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, false)
			require.NoError(t, err)
			before := f.state(t)
			// 群系统事件的空正文用于命中屏障，此时成员退出关系尚未提交。
			barrier := &navigationWriteBarrier{entered: make(chan struct{}), release: make(chan struct{})}
			f.db.AddQueryHook(barrier)
			var release sync.Once
			defer release.Do(func() { close(barrier.release) })
			removed, written := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
				removed <- err
			}()
			select {
			case <-barrier.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// 未提交的移除不阻塞只读 Query，页面仍可能取得即将过期的群资料。
			_, err = groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID)
			require.NoError(t, err)
			go func() { written <- operation.execute(ctx, f, message.ID) }()
			waitForNavigationLock(t, ctx, f.db, f.groupID)
			release.Do(func() { close(barrier.release) })
			require.NoError(t, <-removed)
			require.ErrorIs(t, <-written, conversationaction.ErrConversationNotFound, "removed member write")
			require.Equal(t, before, f.state(t), "personal state changed")
			var messages, receipts int
			require.NoError(t, f.db.NewSelect().TableExpr("messages").ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &messages))
			require.NoError(t, f.db.NewSelect().TableExpr("conversation_mention_reviews").ColumnExpr("count(*)").Where("conversation_id = ?", f.groupID).Scan(ctx, &receipts))
			require.Equal(t, 2, messages)
			require.Zero(t, receipts)
			_, err = groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID)
			require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "removed member detail")
			_, err = conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
			require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "removed member history")
		})
	}
}

// groupSettingsBarrier 在个人设置写入时暂停查询的屏障。
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
		_, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
		removed <- err
	}()
	waitForNavigationLock(t, ctx, f.db, f.groupID)
	release.Do(func() { close(barrier.release) })
	require.NoError(t, <-muted)
	require.NoError(t, <-removed)
	require.True(t, f.state(t).Muted, "mute not committed")
	_, err := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, false)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "removed member mute")
}

// TestGroupOwnerTransferAndLeave 验证转让后的群主资格、普通成员退出及最后一位真人解散。
func TestGroupOwnerTransferAndLeave(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	update := groupchataction.NewUpdateGroupConversationAction(f.db)
	input := groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "转让后的群"}
	_, err := update.Execute(ctx, f.member, input)
	require.ErrorIs(t, err, conversationaction.ErrGroupOwnerRequired, "member management")
	_, err = groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{ConversationID: f.groupID, OwnerIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	_, err = update.Execute(ctx, f.owner, input)
	require.ErrorIs(t, err, conversationaction.ErrGroupOwnerRequired, "former owner management")
	_, err = update.Execute(ctx, f.member, input)
	require.NoError(t, err)
	require.NoError(t, groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID))
	_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	detail, err := groupchataction.NewGetGroupConversationQuery(f.db).Execute(ctx, f.member, f.groupID)
	require.NoError(t, err)
	require.Equal(t, domain.ConversationStatusArchived, detail.Status)
	_, err = update.Execute(ctx, f.member, input)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "dissolved management")
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
	require.NoError(t, err)
	require.Len(t, history.Messages, 4, "dissolved history")
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
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.NewUpdate().Model((*servermodels.Conversation)(nil)).Set("status = ?", domain.ConversationStatusArchived).
				Where("workspace_id = ? AND id = ?", f.owner.Workspace.ID, f.groupID).Exec(ctx)
			require.NoError(t, err)
			written := make(chan error, 1)
			go func() { written <- operation.execute(ctx, f, message.ID) }()
			waitForNavigationLock(t, ctx, f.db, f.groupID)
			// 历史与提及查询只读取已提交快照，不等待会话写锁。
			_, err = conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
			require.NoError(t, err)
			_, err = conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.member, f.groupID)
			require.NoError(t, err)
			require.NoError(t, tx.Commit())
			err = <-written
			if operation.name == "send" {
				require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "archived send")
			} else {
				require.NoError(t, err, "archived %s", operation.name)
			}
			history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.groupID})
			require.NoError(t, err)
			require.Len(t, history.Messages, 1)
			require.Equal(t, message.ID, history.Messages[0].ID)
		})
	}
}
