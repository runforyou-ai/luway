//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// TestInboxIndependentConversation 验证按编号读取单个会话、批量逐项结果及退群和解散的阅读边界。
func TestInboxIndependentConversation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	login := servertest.LoginMember(t, f.db, f.owner.Workspace.ID, f.member.Account.Email, "password123")
	backend := direct.New(f.db, direct.DeploymentConfig{Deployment: servertest.TestDeployment(t, f.db)}, nil, nil, nil, testEnqueuer, nil, nil, nil)
	meta := appservice.RequestMeta{Token: login.Token, WorkspaceID: f.owner.Workspace.ID}
	summary, err := backend.GetInboxConversation(ctx, meta, f.groupID)
	require.NoError(t, err)
	require.Equal(t, f.groupID, summary.ID)
	require.NotNil(t, summary.Group)
	missing := uuid.NewV7().String()
	foreign := newNavigationFixture(t)
	request := appservice.ReadInboxConversationsInput{ConversationIDs: []string{f.groupID, missing, foreign.groupID, f.groupID}, Query: appservice.InboxQuery{Scope: domain.InboxScopeAll}}
	batch, err := backend.ReadInboxConversations(ctx, meta, request)
	require.NoError(t, err)
	require.Len(t, batch.Results, 4)
	for index, item := range batch.Results {
		require.Equal(t, request.ConversationIDs[index], item.ID, "input order changed")
		if index == 0 || index == 3 {
			require.Equal(t, appservice.InboxConversationOutsideQuery, item.Availability, "readable outside filter=%+v", item)
			require.NotNil(t, item.Conversation, "readable outside filter=%+v", item)
		} else {
			require.Equal(t, appservice.InboxConversationUnavailable, item.Availability, "invisible entity leaked=%+v", item)
			require.Nil(t, item.Conversation, "invisible entity leaked=%+v", item)
		}
	}
	// 未指定范围时只核对阅读资格，可读会话一律匹配。
	readable, err := backend.ReadInboxConversations(ctx, meta, appservice.ReadInboxConversationsInput{ConversationIDs: request.ConversationIDs})
	require.NoError(t, err)
	require.Equal(t, appservice.InboxConversationMatching, readable.Results[0].Availability)
	require.Equal(t, appservice.InboxConversationUnavailable, readable.Results[1].Availability)
	// 核验不存在和跨企业会话返回相同错误。
	for _, id := range []string{missing, foreign.groupID} {
		_, err := backend.GetInboxConversation(ctx, meta, id)
		var apiError *appservice.Error
		require.ErrorAs(t, err, &apiError)
		require.Equal(t, "conversation_unavailable", apiError.Reason)
	}
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{ConversationID: f.groupID, MemberIdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	request.Query.Scope = domain.InboxScopeChat
	batch, err = backend.ReadInboxConversations(ctx, meta, request)
	require.NoError(t, err)
	require.Nil(t, batch.Results[0].Conversation)
	require.Equal(t, appservice.InboxConversationUnavailable, batch.Results[0].Availability)
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{ConversationID: f.groupID, MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	_, err = groupchataction.NewDissolveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.owner, f.groupID)
	require.NoError(t, err)
	summary, err = backend.GetInboxConversation(ctx, meta, f.groupID)
	require.NoError(t, err)
	require.NotNil(t, summary.Group)
	require.Equal(t, domain.ConversationStatusArchived, summary.Group.Status)
	batch, err = backend.ReadInboxConversations(ctx, meta, request)
	require.NoError(t, err)
	require.Equal(t, appservice.InboxConversationMatching, batch.Results[0].Availability)
}

// TestInboxCustomerDetailSnapshot 验证客服转交和关闭不撤销阅读，同轮批量摘要与资格保持一致。
func TestInboxCustomerDetailSnapshot(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "独立单聊摘要"})
	require.NoError(t, err)
	query := inboxaction.NewLoadInboxQuery(f.db)
	mine := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.owner.WorkspaceIdentity.ID}
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
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), agentrunaction.NewScheduler(testEnqueuer), testEnqueuer).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, done))
	require.True(t, snapshot[0].MatchesQuery)
	require.Equal(t, f.owner.WorkspaceIdentity.ID, snapshot[0].Conversation.Service.Assignee.IdentityID)
	require.False(t, snapshot[1].MatchesQuery)
	require.NotNil(t, snapshot[1].Conversation.Direct)
	current, err := query.ReadByIDs(ctx, f.owner, ids, &mine)
	require.NoError(t, err)
	require.False(t, current[0].MatchesQuery)
	require.Equal(t, f.member.WorkspaceIdentity.ID, current[0].Conversation.Service.Assignee.IdentityID)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, newTestAgentRun(f.db, nil, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), testEnqueuer).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	closed := inboxaction.LoadInput{Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: f.member.WorkspaceIdentity.ID, ServiceStatus: domain.ServiceSessionStatusClosed}
	current, err = query.ReadByIDs(ctx, f.owner, ids, &closed)
	require.NoError(t, err)
	require.True(t, current[0].MatchesQuery)
	require.Equal(t, domain.ServiceSessionStatusClosed, current[0].Conversation.Service.ServiceSessionStatus)
	_, err = query.ReadByIDs(ctx, f.owner, []string{"bad-id"}, &closed)
	require.ErrorIs(t, err, inboxaction.ErrQueryInvalid, "invalid ID")
	empty, err := query.ReadByIDs(ctx, f.owner, nil, &closed)
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}
