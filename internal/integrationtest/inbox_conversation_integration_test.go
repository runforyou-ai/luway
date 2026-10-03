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
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/uptrace/bun"
)

// TestInboxIndependentConversation 验证按编号读取单个会话、批量逐项结果及退群和解散的阅读边界。
func TestInboxIndependentConversation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	login := loginMember(t, f.db, f.owner.Organization.ID, f.member.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{}, nil, serverfilecontent.S3Config{}, nil, nil, nil, nil, nil, nil)
	meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Organization.ID}
	summary, err := backend.GetInboxConversation(ctx, meta, f.groupID)
	if err != nil || summary.ID != f.groupID || summary.Group == nil {
		t.Fatalf("deep link=%+v err=%v", summary, err)
	}
	missing := uuid.NewV7().String()
	foreign := newNavigationFixture(t)
	request := appservice.ReadInboxConversationsInput{ConversationIDs: []string{f.groupID, missing, foreign.groupID, f.groupID}, Query: appservice.InboxQuery{Scope: appservice.InboxScopeAll}}
	batch, err := backend.ReadInboxConversations(ctx, meta, request)
	if err != nil || len(batch.Results) != 4 {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	for index, item := range batch.Results {
		if item.ID != request.ConversationIDs[index] {
			t.Fatal("input order changed")
		}
		if index == 0 || index == 3 {
			if item.Availability != appservice.InboxConversationOutsideQuery || item.Conversation == nil {
				t.Fatalf("readable outside filter=%+v", item)
			}
		} else if item.Availability != appservice.InboxConversationUnavailable || item.Conversation != nil {
			t.Fatalf("invisible entity leaked=%+v", item)
		}
	}
	// 未指定范围时只核对阅读资格，可读会话一律匹配。
	readable, err := backend.ReadInboxConversations(ctx, meta, appservice.ReadInboxConversationsInput{ConversationIDs: request.ConversationIDs})
	if err != nil || readable.Results[0].Availability != appservice.InboxConversationMatching || readable.Results[1].Availability != appservice.InboxConversationUnavailable {
		t.Fatalf("readable batch=%+v err=%v", readable, err)
	}
	// 核验不存在和跨企业会话返回相同错误。
	for _, id := range []string{missing, foreign.groupID} {
		_, err := backend.GetInboxConversation(ctx, meta, id)
		var apiError *appservice.Error
		if !errors.As(err, &apiError) || apiError.Reason != "conversation_unavailable" {
			t.Fatalf("unavailable error=%v", err)
		}
	}
	if _, err := groupchataction.NewRemoveGroupConversationMemberAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	request.Query.Scope = appservice.InboxScopeChat
	batch, err = backend.ReadInboxConversations(ctx, meta, request)
	if err != nil || batch.Results[0].Conversation != nil || batch.Results[0].Availability != appservice.InboxConversationUnavailable {
		t.Fatalf("removed=%+v err=%v", batch, err)
	}
	if _, err := groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.OrganizationIdentity.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID); err != nil {
		t.Fatal(err)
	}
	summary, err = backend.GetInboxConversation(ctx, meta, f.groupID)
	if err != nil || summary.Group == nil || summary.Group.Status != appservice.ConversationStatusArchived {
		t.Fatalf("dissolved=%+v err=%v", summary, err)
	}
	batch, err = backend.ReadInboxConversations(ctx, meta, request)
	if err != nil || batch.Results[0].Availability != appservice.InboxConversationMatching {
		t.Fatalf("rejoined=%+v err=%v", batch, err)
	}
}

// TestInboxCustomerDetailSnapshot 验证客服转交和关闭不撤销阅读，同轮批量摘要与资格保持一致。
func TestInboxCustomerDetailSnapshot(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, newTestTasks(f.db)).Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "独立单聊摘要"})
	if err != nil {
		t.Fatal(err)
	}
	query := inboxaction.NewLoadInboxQuery(f.db)
	mine := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.owner.OrganizationIdentity.ID}
	ids := []string{f.conversationID, direct.Conversation.ID}
	f.db.AddQueryHook(chatQueryHook{})
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return event.Operation() == "SELECT" && strings.Contains(event.Query, "AS service_session_id") && strings.Contains(event.Query, "cv.id IN")
	})
	var snapshot []inboxaction.ConversationResult
	done := make(chan error, 1)
	go func() {
		var err error
		snapshot, err = query.ReadByIDs(context.WithValue(ctx, chatQueryGateKey{}, gate), f.owner, ids, &mine)
		done <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db)).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.OrganizationIdentity.ID}); err != nil {
		t.Fatal(err)
	}
	gate.open()
	if err := waitChatResult(t, ctx, done); err != nil {
		t.Fatal(err)
	}
	if !snapshot[0].MatchesQuery || snapshot[0].Conversation.Service.Assignee.IdentityID != f.owner.OrganizationIdentity.ID || snapshot[1].MatchesQuery || snapshot[1].Conversation.Direct == nil {
		t.Fatalf("mixed snapshot=%+v", snapshot)
	}
	current, err := query.ReadByIDs(ctx, f.owner, ids, &mine)
	if err != nil || current[0].MatchesQuery || current[0].Conversation.Service.Assignee.IdentityID != f.member.OrganizationIdentity.ID {
		t.Fatalf("transferred=%+v err=%v", current, err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, agentrunaction.NewExecuteAction(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil), newTestTasks(f.db)).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	closed := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.member.OrganizationIdentity.ID, ServiceStatus: domain.ServiceSessionStatusClosed}
	current, err = query.ReadByIDs(ctx, f.owner, ids, &closed)
	if err != nil || !current[0].MatchesQuery || current[0].Conversation.Service.ServiceSessionStatus != domain.ServiceSessionStatusClosed {
		t.Fatalf("closed=%+v err=%v", current, err)
	}
	if _, err := query.ReadByIDs(ctx, f.owner, []string{"bad-id"}, &closed); !errors.Is(err, inboxaction.ErrQueryInvalid) {
		t.Fatalf("invalid ID=%v", err)
	}
	empty, err := query.ReadByIDs(ctx, f.owner, nil, &closed)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}
