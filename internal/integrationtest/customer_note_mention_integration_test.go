//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestCustomerNoteMentions 验证内部备注提醒建立协作者、待处理的 @我 条目、提及计数和周期关闭后的移出。
func TestCustomerNoteMentions(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	load := inboxaction.NewLoadInboxQuery(f.db)
	memberID := f.member.OrganizationIdentity.ID
	// customerRow 读取指定身份在给定范围中的目标会话摘要。
	customerRow := func(identity *servermodels.Identity, input inboxaction.LoadInput) (*inboxaction.ConversationSummary, inboxaction.UnreadCounts) {
		t.Helper()
		page, counts, err := load.Execute(ctx, identity, input)
		if err != nil {
			t.Fatal(err)
		}
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
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}

	if row, counts := customerRow(f.member, mentioned); row != nil || counts.Pending != 0 {
		t.Fatalf("mentioned items before note = %+v counts=%+v", row, counts)
	}
	noteInput := servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
		Body: "@成员 帮忙看下物流", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{memberID},
	}
	note, err := send.Execute(ctx, f.owner, noteInput)
	if err != nil || len(note.Mentions) != 1 || note.Mentions[0].SourceID != memberID {
		t.Fatalf("note=%+v err=%v", note, err)
	}

	// 被提醒成员写入参与者关系，负责人保持不变。
	var participants int
	participants, err = f.db.NewSelect().TableExpr("conversation_participants AS cp").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id").
		Where("cp.conversation_id = ? AND cp.left_at IS NULL AND cs.source_id = ?", f.conversationID, memberID).Count(ctx)
	if err != nil || participants != 1 {
		t.Fatalf("collaborator participants=%d err=%v", participants, err)
	}
	session := &servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(session).Where("ss.conversation_id = ?", f.conversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != f.owner.OrganizationIdentity.ID {
		t.Fatalf("mention changed assignee: %+v", session)
	}

	row, counts := customerRow(f.member, mentioned)
	if row == nil || row.MentionedUnreadCount != 1 || counts.Pending != 1 || row.Pending == nil || row.Pending.Kind != domain.InboxPendingKindMention || !row.Pending.Since.Equal(note.OriginatedAt) {
		t.Fatalf("mentioned item row=%+v counts=%+v", row, counts)
	}
	if row, _ := customerRow(f.owner, mentioned); row != nil {
		t.Fatalf("sender sees own mention in mentioned items: %+v", row)
	}
	if row, _ := customerRow(f.owner, all); row == nil || row.Service.UnansweredMentionCount != 1 {
		t.Fatalf("unanswered mentions = %+v", row)
	}

	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.member, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	if last := history.Messages[len(history.Messages)-1]; last.ID != note.ID || len(last.Mentions) != 1 || last.Mentions[0].SourceID != memberID {
		t.Fatalf("timeline mentions = %+v", last)
	}

	t.Run("提及导航覆盖客户会话", func(t *testing.T) {
		state, err := conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.member, f.conversationID)
		if err != nil || state.PendingMentionCount != 1 {
			t.Fatalf("navigation state=%+v err=%v", state, err)
		}
		pending, err := conversationaction.NewListPendingConversationMentionsQuery(f.db).Execute(ctx, f.member, f.conversationID)
		if err != nil || len(pending.MessageIDs) != 1 || pending.MessageIDs[0] != note.ID {
			t.Fatalf("pending mentions=%+v err=%v", pending, err)
		}
		// 未被提醒的成员没有待查看提及，也不能确认他人的提醒。
		ownerState, err := conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.owner, f.conversationID)
		if err != nil || ownerState.PendingMentionCount != 0 {
			t.Fatalf("owner navigation state=%+v err=%v", ownerState, err)
		}
		if _, err := conversationaction.NewMarkConversationMentionReviewedAction(f.db).Execute(ctx, f.owner, f.conversationID, note.ID); !errors.Is(err, conversationaction.ErrMentionTargetInvalid) {
			t.Fatalf("owner review = %v", err)
		}
		review, err := conversationaction.NewMarkConversationMentionReviewedAction(f.db).Execute(ctx, f.member, f.conversationID, note.ID)
		if err != nil || review.Outcome != "reviewed" {
			t.Fatalf("review=%+v err=%v", review, err)
		}
		state, err = conversationaction.NewGetConversationNavigationStateQuery(f.db).Execute(ctx, f.member, f.conversationID)
		if err != nil || state.PendingMentionCount != 0 {
			t.Fatalf("navigation state after review=%+v err=%v", state, err)
		}
	})

	t.Run("阅读后不再计数但仍待处理", func(t *testing.T) {
		if _, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, note.ID, false); err != nil {
			t.Fatal(err)
		}
		row, counts := customerRow(f.member, mentioned)
		if row == nil || row.MentionedUnreadCount != 0 || counts.Pending != 1 {
			t.Fatalf("mentioned item after read row=%+v counts=%+v", row, counts)
		}
	})

	t.Run("被提醒成员发言后提醒视为已回复", func(t *testing.T) {
		if _, err := send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "物流已催", Visibility: domain.MessageVisibilityInternal,
		}); err != nil {
			t.Fatal(err)
		}
		if row, _ := customerRow(f.owner, all); row == nil || row.Service.UnansweredMentionCount != 0 {
			t.Fatalf("unanswered mentions after reply = %+v", row)
		}
		if row, counts := customerRow(f.member, mentioned); row != nil || counts.Pending != 0 {
			t.Fatalf("answered mention still pending row=%+v counts=%+v", row, counts)
		}
	})

	t.Run("提醒目标校验", func(t *testing.T) {
		cases := []struct {
			name  string
			input servicesessionaction.ServiceTextMessageInput
		}{
			{"对客消息不能提醒", servicesessionaction.ServiceTextMessageInput{Body: "您好", MentionIdentityIDs: []string{memberID}}},
			{"不能重复提醒", servicesessionaction.ServiceTextMessageInput{Body: "看下", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{memberID, memberID}}},
		}
		for _, test := range cases {
			test.input.ConversationID, test.input.ClientMessageID = f.conversationID, uuid.NewV7().String()
			var validation *conversationaction.ValidationError
			if _, err := send.Execute(ctx, f.owner, test.input); !errors.As(err, &validation) || validation.Fields["mentionIdentityIds"] != servicesessionaction.ValidationMentionIdentityIDsInvalid {
				t.Fatalf("%s: %v", test.name, err)
			}
		}
		for _, target := range []string{f.owner.OrganizationIdentity.ID, uuid.NewV7().String()} {
			_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
				ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
				Body: "看下", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{target},
			})
			var conflict *conversationaction.ConflictError
			if !errors.As(err, &conflict) || conflict.Reason != servicesessionaction.ConflictReasonNoteMentionTargetInvalid {
				t.Fatalf("mention %s = %v", target, err)
			}
		}
	})

	t.Run("重试时提醒成员变化产生冲突", func(t *testing.T) {
		replay, err := send.Execute(ctx, f.owner, noteInput)
		if err != nil || replay.ID != note.ID || len(replay.Mentions) != 1 {
			t.Fatalf("replay=%+v err=%v", replay, err)
		}
		changed := noteInput
		changed.MentionIdentityIDs = nil
		var conflict *conversationaction.ConflictError
		if _, err := send.Execute(ctx, f.owner, changed); !errors.As(err, &conflict) || conflict.Reason != conversationaction.ConflictReasonIdempotencyMismatch {
			t.Fatalf("changed mentions retry = %v", err)
		}
	})

	t.Run("周期关闭后移出待处理", func(t *testing.T) {
		// 关闭前留下一条未回应提醒，关闭后不再计入待处理。
		if _, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
			ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(),
			Body: "@成员 结单前再确认下", Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{memberID},
		}); err != nil {
			t.Fatal(err)
		}
		if _, counts := customerRow(f.member, mentioned); counts.Pending != 1 {
			t.Fatalf("unanswered mention before close counts=%+v", counts)
		}
		tasks := newTestTasks(f.db)
		closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, tasks, nil, testAttachmentReader(f.db), nil, nil), newTestTasks(f.db))
		if _, err := closeSession.Execute(ctx, f.owner, f.conversationID); err != nil {
			t.Fatal(err)
		}
		if row, counts := customerRow(f.member, mentioned); row != nil || counts.Pending != 0 {
			t.Fatalf("closed session still pending: %+v counts=%+v", row, counts)
		}
		// 客户再次来信开启新周期，上一周期的提醒不再归入 @我。
		if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
			ClientMessageID: uuid.NewV7().String(), Body: "又有新问题",
		}); err != nil {
			t.Fatal(err)
		}
		if row, _ := customerRow(f.member, mentioned); row != nil {
			t.Fatalf("new session inherits previous mentions: %+v", row)
		}
	})
}

// TestCustomerNoteMentionsCreateSubjectsInOrder 验证尚无聊天主体的两名成员在不同客户会话中同时互相提醒时都能发送成功。
func TestCustomerNoteMentionsCreateSubjectsInOrder(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	identities := make([]*servermodels.Identity, 0, 2)
	for index, email := range []string{uniqueEmail("first-note"), uniqueEmail("second-note")} {
		if _, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, memberSpec{HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: []string{"备注成员甲", "备注成员乙"}[index], Email: email, Password: "password123", RoleID: f.owner.User.RoleID}); err != nil {
			t.Fatal(err)
		}
		identities = append(identities, loginMember(t, f.db, f.owner.Organization.ID, email, "password123").Identity)
	}
	// high 的身份编号较大，旧顺序下两个事务会先创建各自的主体再等待对方。
	low, high := identities[0], identities[1]
	if low.OrganizationIdentity.ID > high.OrganizationIdentity.ID {
		low, high = high, low
	}
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:fedcba9876543210fedcba9876543210", ClientMessageID: uuid.NewV7().String(), Body: "另一位客户",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return strings.Contains(event.Query, `INSERT INTO "chat_subjects"`)
	})
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	// note 构造提醒对方的内部备注。
	note := func(conversationID string, target *servermodels.Identity) servicesessionaction.ServiceTextMessageInput {
		return servicesessionaction.ServiceTextMessageInput{
			ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "请协助",
			Visibility: domain.MessageVisibilityInternal, MentionIdentityIDs: []string{target.OrganizationIdentity.ID},
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
	waitChatDatabaseLock(t, ctx, f.db, `INSERT INTO "chat_subjects"`, low.OrganizationIdentity.ID)
	gate.open()
	if err := waitChatResult(t, ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := waitChatResult(t, ctx, second); err != nil {
		t.Fatal(err)
	}
}
