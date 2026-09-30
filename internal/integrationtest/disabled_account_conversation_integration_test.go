//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/cervi/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// testDisabledAgentConversation 验证禁用 AI 员工后会话保留在列表与未读汇总中、禁止新消息，恢复后允许发送。
func testDisabledAgentConversation(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentID, agentIdentityID string, tasks *servertask.Runtime) {
	ctx := context.Background()
	first, _ := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	query := inboxaction.NewLoadInboxQuery(db)
	input := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeAgent}}
	unreadMark := conversationaction.NewUpdateConversationUnreadMarkAction(db)
	if err := unreadMark.Execute(ctx, identity, first.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unreadMark.Execute(context.Background(), identity, first.Conversation.ID, false)
	})
	_, before, err := query.Execute(ctx, identity, input)
	if err != nil || before.Attention == 0 {
		t.Fatalf("unread counts before disable=%+v %v", before, err)
	}
	updateStatus := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db))
	if _, err := updateStatus.Execute(ctx, identity, agentID, domain.IdentityStatusInactive); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = updateStatus.Execute(context.Background(), identity, agentID, domain.IdentityStatusActive)
	})
	assertAgentStatus := func(want domain.IdentityStatus) {
		t.Helper()
		page, counts, err := query.Execute(ctx, identity, input)
		if err != nil || counts != before {
			t.Fatalf("unread counts=%+v before=%+v %v", counts, before, err)
		}
		listed := false
		for _, row := range page.Conversations {
			if row.ID == first.Conversation.ID {
				listed = row.Agent != nil && row.Agent.AgentStatus == want
			}
		}
		if !listed {
			t.Fatalf("disabled agent conversation not listed with status %s: %+v", want, page.Conversations)
		}
		results, err := query.ReadByIDs(ctx, identity, []string{first.Conversation.ID}, &input)
		if err != nil || len(results) != 1 || !results[0].MatchesQuery || results[0].Conversation == nil || results[0].Conversation.Agent.AgentStatus != want {
			t.Fatalf("read by id=%+v %v", results, err)
		}
	}
	assertAgentStatus(domain.IdentityStatusInactive)
	send := directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks))
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后发送"}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("send to disabled agent=%v", err)
	}
	if _, err := directchataction.NewSendFirstAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{ConversationID: uuid.NewV7().String(), AgentIdentityID: agentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后新建"}); !errors.Is(err, conversationaction.ErrAgentTargetNotFound) {
		t.Fatalf("start chat with disabled agent=%v", err)
	}
	if _, err := updateStatus.Execute(ctx, identity, agentID, domain.IdentityStatusActive); err != nil {
		t.Fatal(err)
	}
	assertAgentStatus(domain.IdentityStatusActive)
	if _, err := send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "恢复后发送"}); err != nil {
		t.Fatalf("send after reactivation=%v", err)
	}
}

// TestDisabledMemberDirectConversation 验证真人成员禁用后对方仍保留单聊与未读汇总、不能发送或发起新消息，恢复后允许发送。
func TestDisabledMemberDirectConversation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	first, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用前消息"})
	if err != nil {
		t.Fatal(err)
	}
	send := directchataction.NewSendDirectTextMessageAction(f.db)
	if _, err := send.Execute(ctx, f.member, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用前回复"}); err != nil {
		t.Fatal(err)
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	input := inboxaction.LoadInput{Scope: domain.InboxScopeChat, Kinds: []domain.ConversationType{domain.ConversationTypeDirect}}
	_, before, err := query.Execute(ctx, f.owner, input)
	if err != nil || before.Unread == 0 {
		t.Fatalf("unread counts before disable=%+v %v", before, err)
	}
	updateStatus := testUserStatusAction(f.db)
	if _, err := updateStatus.Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive); err != nil {
		t.Fatal(err)
	}
	assertPeerStatus := func(want domain.IdentityStatus) {
		t.Helper()
		page, counts, err := query.Execute(ctx, f.owner, input)
		if err != nil || counts != before {
			t.Fatalf("unread counts=%+v before=%+v %v", counts, before, err)
		}
		if len(page.Conversations) != 1 || page.Conversations[0].ID != first.Conversation.ID || page.Conversations[0].Direct == nil || page.Conversations[0].Direct.PeerStatus != want {
			t.Fatalf("direct conversations with peer status %s: %+v", want, page.Conversations)
		}
		results, err := query.ReadByIDs(ctx, f.owner, []string{first.Conversation.ID}, &input)
		if err != nil || len(results) != 1 || !results[0].MatchesQuery || results[0].Conversation == nil || results[0].Conversation.Direct.PeerStatus != want {
			t.Fatalf("read by id=%+v %v", results, err)
		}
	}
	assertPeerStatus(domain.IdentityStatusInactive)
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: first.Conversation.ID})
	if err != nil || len(history.Messages) != 2 {
		t.Fatalf("history after peer disabled=%+v %v", history, err)
	}
	if _, err := send.Execute(ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后发送"}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("send to disabled member=%v", err)
	}
	if _, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "禁用后发起"}); !errors.Is(err, conversationaction.ErrDirectTargetNotFound) {
		t.Fatalf("start chat with disabled member=%v", err)
	}
	if _, err := updateStatus.Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusActive); err != nil {
		t.Fatal(err)
	}
	assertPeerStatus(domain.IdentityStatusActive)
	if _, err := send.Execute(ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: first.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "恢复后发送"}); err != nil {
		t.Fatalf("send after reactivation=%v", err)
	}
}
