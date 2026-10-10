//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestCustomerNoteMentions 验证内部备注提醒建立协作者、待处理的 @我 条目、提及计数和周期关闭后的移出。
func TestCustomerNoteMentions(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	load := inboxaction.NewLoadInboxQuery(f.db)
	memberID := f.member.WorkspaceIdentity.ID
	// customerRow 读取指定身份在给定范围中的目标会话摘要。
	customerRow := func(identity *servermodels.Identity, input inboxaction.LoadInput) (*inboxaction.ConversationSummary, inboxaction.UnreadCounts) {
		t.Helper()
		page, counts, err := load.Execute(ctx, identity, input)
		require.NoError(t, err)
		for index := range page.Conversations {
			if page.Conversations[index].ID == f.conversationID {
				return &page.Conversations[index], counts
			}
		}
		return nil, counts
	}
	mentioned := inboxaction.LoadInput{Scope: domain.InboxScopePending, PendingKind: domain.InboxPendingKindMention}
	all := inboxaction.LoadInput{Scope: domain.InboxScopeAll}
	// 负责人领取后，提醒同事的会话对同事只以 @我 出现在待处理。
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)

	beforeRow, beforeCounts := customerRow(f.member, mentioned)
	require.Nil(t, beforeRow, "mentioned items before note")
	require.Zero(t, beforeCounts.Pending, "mentioned items before note")
	noteInput := servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
		Body: "@成员 帮忙看下物流", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{memberID},
	}
	note, err := send.Execute(ctx, f.owner, noteInput)
	require.NoError(t, err)
	require.Len(t, note.Mentions, 1)
	require.Equal(t, memberID, note.Mentions[0].SourceID)

	// 被提醒成员写入参与者关系，负责人保持不变。
	var participants int64
	participants, err = f.db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id").
		Where("cp.conversation_id = ? AND cp.left_at IS NULL AND cs.source_id = ?", f.conversationID, memberID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), participants, "collaborator participants")
	session := &servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(session).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx))
	require.NotNil(t, session.AssigneeIdentityID, "mention changed assignee")
	require.Equal(t, f.owner.WorkspaceIdentity.ID, *session.AssigneeIdentityID, "mention changed assignee")

	row, counts := customerRow(f.member, mentioned)
	require.NotNil(t, row)
	require.Equal(t, 1, row.MentionedUnreadCount)
	require.Equal(t, 1, counts.Pending)
	require.NotNil(t, row.Pending)
	require.Equal(t, domain.InboxPendingKindMention, row.Pending.Kind)
	require.True(t, row.Pending.Since.Equal(note.OriginatedAt), "pending since = %v, want %v", row.Pending.Since, note.OriginatedAt)
	ownerMentioned, _ := customerRow(f.owner, mentioned)
	require.Nil(t, ownerMentioned, "sender sees own mention in mentioned items")
	ownerAll, _ := customerRow(f.owner, all)
	require.NotNil(t, ownerAll)
	require.Equal(t, 1, ownerAll.Service.UnansweredMentionCount, "unanswered mentions")

	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	last := history.Messages[len(history.Messages)-1]
	require.Equal(t, note.ID, last.ID)
	require.Len(t, last.Mentions, 1)
	require.Equal(t, memberID, last.Mentions[0].SourceID)

	t.Run("提及导航覆盖客户会话", func(t *testing.T) {
		state, err := conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.member, f.conversationID)
		require.NoError(t, err)
		require.Equal(t, 1, state.PendingMentionCount, "navigation state")
		pending, err := conversationaction.NewListPendingConversationMentionsQuery(f.db).Execute(ctx, f.member, f.conversationID)
		require.NoError(t, err)
		require.Equal(t, []string{note.ID}, pending.MessageIDs)
		// 未被提醒的成员没有待查看提及，也不能确认他人的提醒。
		ownerState, err := conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.owner, f.conversationID)
		require.NoError(t, err)
		require.Zero(t, ownerState.PendingMentionCount, "owner navigation state")
		_, err = conversationaction.NewMarkConversationMentionReviewedAction(f.db).Execute(ctx, f.owner, f.conversationID, note.ID)
		require.ErrorIs(t, err, conversationaction.ErrMentionTargetInvalid, "owner review")
		review, err := conversationaction.NewMarkConversationMentionReviewedAction(f.db).Execute(ctx, f.member, f.conversationID, note.ID)
		require.NoError(t, err)
		require.Equal(t, "reviewed", review.Outcome)
		state, err = conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.member, f.conversationID)
		require.NoError(t, err)
		require.Zero(t, state.PendingMentionCount, "navigation state after review")
	})

	t.Run("阅读后不再计数但仍待处理", func(t *testing.T) {
		_, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, note.ID, false)
		require.NoError(t, err)
		row, counts := customerRow(f.member, mentioned)
		require.NotNil(t, row)
		require.Zero(t, row.MentionedUnreadCount)
		require.Equal(t, 1, counts.Pending)
	})

	t.Run("被提醒成员发言后提醒视为已回复", func(t *testing.T) {
		_, err := send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "物流已催", Visibility: domain.MessageVisibilityInternal,
		})
		require.NoError(t, err)
		ownerAll, _ := customerRow(f.owner, all)
		require.NotNil(t, ownerAll)
		require.Zero(t, ownerAll.Service.UnansweredMentionCount, "unanswered mentions after reply")
		row, counts := customerRow(f.member, mentioned)
		require.Nil(t, row, "answered mention still pending")
		require.Zero(t, counts.Pending, "answered mention still pending")
	})

	t.Run("提醒目标校验", func(t *testing.T) {
		cases := []struct {
			name  string
			input servicesessionaction.ServiceTextMessageInput
		}{
			{"对客消息不能提醒", servicesessionaction.ServiceTextMessageInput{Body: "您好", MentionIdentityIDs: []string{memberID}}},
		}
		for _, test := range cases {
			test.input.ConversationID, test.input.ClientMessageID = f.conversationID, uuid.NewV7().String()
			var validation *conversationaction.ValidationError
			_, err := send.Execute(ctx, f.owner, test.input)
			require.ErrorAs(t, err, &validation, test.name)
			require.Equal(t, servicesessionaction.ValidationMentionIdentityIDsInvalid, validation.Fields["mentionIdentityIds"], test.name)
		}
		for _, target := range []string{f.owner.WorkspaceIdentity.ID, uuid.NewV7().String()} {
			_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
				ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
				Body: "看下", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{target},
			})
			var conflict *conversationaction.ConflictError
			require.ErrorAs(t, err, &conflict, "mention %s", target)
			require.Equal(t, servicesessionaction.ConflictReasonNoteMentionTargetInvalid, conflict.Reason, "mention %s", target)
		}
	})

	t.Run("重试时提醒成员变化产生冲突", func(t *testing.T) {
		replay, err := send.Execute(ctx, f.owner, noteInput)
		require.NoError(t, err)
		require.Equal(t, note.ID, replay.ID)
		require.Len(t, replay.Mentions, 1)
		changed := noteInput
		changed.MentionIdentityIDs = nil
		var conflict *conversationaction.ConflictError
		_, err = send.Execute(ctx, f.owner, changed)
		require.ErrorAs(t, err, &conflict, "changed mentions retry")
		require.Equal(t, conversationaction.ConflictReasonIdempotencyMismatch, conflict.Reason)
	})

	t.Run("周期关闭后移出待处理", func(t *testing.T) {
		// 关闭前留下一条未回应提醒，关闭后不再计入待处理。
		_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "@成员 结单前再确认下", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{memberID},
		})
		require.NoError(t, err)
		_, counts := customerRow(f.member, mentioned)
		require.Equal(t, 1, counts.Pending, "unanswered mention before close")
		tasks := servertest.NewTasks()
		closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, tasks, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer)
		_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
		require.NoError(t, err)
		row, counts := customerRow(f.member, mentioned)
		require.Nil(t, row, "closed session still pending")
		require.Zero(t, counts.Pending, "closed session still pending")
		// 客户再次来信开启新周期，上一周期的提醒不再归入 @我。
		_, err = f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
			ClientMessageID: uuid.NewV7().String(), Body: "又有新问题",
		})
		require.NoError(t, err)
		row, _ = customerRow(f.member, mentioned)
		require.Nil(t, row, "new session inherits previous mentions")
	})
}

// TestCustomerNoteMentionsCreateSubjectsInOrder 验证尚无聊天主体的两名成员在不同客户会话中同时互相提醒时都能发送成功。
func TestCustomerNoteMentionsCreateSubjectsInOrder(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	identities := make([]*servermodels.Identity, 0, 2)
	for index, email := range []string{servertest.UniqueEmail("first-note"), servertest.UniqueEmail("second-note")} {
		_, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: []string{"备注成员甲", "备注成员乙"}[index], Email: email, Password: "password123", RoleID: f.owner.User.RoleID})
		require.NoError(t, err)
		identities = append(identities, servertest.LoginMember(t, f.db, f.owner.Workspace.ID, email, "password123").Identity)
	}
	// high 的身份编号较大，旧顺序下两个事务会先创建各自的主体再等待对方。
	low, high := identities[0], identities[1]
	if low.WorkspaceIdentity.ID > high.WorkspaceIdentity.ID {
		low, high = high, low
	}
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:fedcba9876543210fedcba9876543210", ClientMessageID: uuid.NewV7().String(), Body: "另一位客户",
	})
	require.NoError(t, err)
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return strings.Contains(event.Query, `INSERT INTO "chat_subjects"`)
	})
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	// note 构造提醒对方的内部备注。
	note := func(conversationID string, target *servermodels.Identity) servicesessionaction.ServiceTextMessageInput {
		return servicesessionaction.ServiceTextMessageInput{
			ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "请协助",
			Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{target.WorkspaceIdentity.ID},
		}
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := send.Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), high, note(f.conversationID, low))
		first <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := send.Execute(ctx, low, note(other.Conversation.ID, high))
		second <- err
	}()
	// 第二个事务等待第一个事务已写入的同一个主体后再放行。
	waitChatDatabaseLock(t, ctx, f.db, `INSERT INTO "chat_subjects"`, low.WorkspaceIdentity.ID)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, first))
	require.NoError(t, waitChatResult(t, ctx, second))
}
