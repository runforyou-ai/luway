//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// directServiceFixture 保存 单聊服务台测试共用的企业、发起人、处理人与 AI 员工。
type directServiceFixture struct {
	navigationFixture
	tasks     *servertest.Tasks
	execution agentaction.ExecutionInput
	team      *teamaction.TeamRecord
	agent     *agentaction.Agent
}

// newDirectServiceFixture 创建服务员工、转人工到 IT 团队的 AI 员工；群主作为发起人，开启接待的成员属于 IT 团队并作为处理人。
func newDirectServiceFixture(t *testing.T) directServiceFixture {
	t.Helper()
	ctx := context.Background()
	f := directServiceFixture{navigationFixture: newNavigationFixture(t)}
	disableAutoAssignment(t, f.db, f.owner.Workspace.ID)
	f.tasks = servertest.NewTasks()
	provider, err := aiprovideraction.NewCreateAIProviderAction(f.db).Execute(ctx, f.owner, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{
			Identifier: "chat-a", Name: "对话 A", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 4096,
		}},
	})
	require.NoError(t, err)
	f.execution = agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: aiModelID(t, f.db, provider.ID, "chat-a")}}
	f.team, err = teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "IT 支持"})
	require.NoError(t, err)
	_, err = teamaction.NewAddMembersAction(f.db, f.tasks).Execute(ctx, f.owner, f.team.ID, []teamaction.MemberIdentity{{
		IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: f.member.WorkspaceIdentity.ID,
	}})
	require.NoError(t, err)
	f.agent = f.newAgent(t, "IT 服务台", domain.ServiceAudienceEmployee)
	f.updateAgent(t, f.agent, []domain.ServiceAudience{domain.ServiceAudienceEmployee}, f.team.ID)
	return f
}

// newAgent 创建指定服务对象的 AI 员工。
func (f directServiceFixture) newAgent(t *testing.T, name string, audiences ...domain.ServiceAudience) *agentaction.Agent {
	t.Helper()
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(context.Background(), f.owner, agentaction.CreateInput{
		DisplayName: name, ServiceAudiences: audiences, Execution: f.execution,
	})
	require.NoError(t, err)
	return created
}

// updateAgent 修改 AI 员工的服务对象与转人工团队。
func (f directServiceFixture) updateAgent(t *testing.T, agent *agentaction.Agent, audiences []domain.ServiceAudience, handoffTeamID string) {
	t.Helper()
	_, err := agentaction.NewUpdateAgentAction(f.db, testEnqueuer, testServiceSessionReturner(f.db)).Execute(context.Background(), f.owner, agent.ID, agentaction.UpdateInput{
		DisplayName: agent.DisplayName, TeamIDs: []string{}, WorkStatus: domain.WorkStatusWorking, ServiceAudiences: audiences, HandoffTeamID: handoffTeamID,
	})
	require.NoError(t, err)
}

// startChat 由发起人向 AI 员工发出首条消息并返回会话编号。
func (f directServiceFixture) startChat(t *testing.T, agentIdentityID, body string) string {
	t.Helper()
	conversationID := uuid.NewV7().String()
	_, err := directchataction.NewSendFirstAgentTextMessageAction(f.db, testEnqueuer, agentrunaction.NewScheduler(f.tasks)).Execute(context.Background(), f.owner, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: agentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: body,
	})
	require.NoError(t, err)
	return conversationID
}

// ask 由发起人在已有 AI 聊天中继续发言。
func (f directServiceFixture) ask(t *testing.T, conversationID, body string) conversationaction.ConversationMessage {
	t.Helper()
	message, err := directchataction.NewSendAgentTextMessageAction(f.db, testEnqueuer, agentrunaction.NewScheduler(f.tasks)).Execute(context.Background(), f.owner, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body,
	})
	require.NoError(t, err)
	return message
}

// reply 由处理人在服务会话中发送共享回复或内部备注。
func (f directServiceFixture) reply(conversationID, body string, visibility domain.MessageVisibility, mentions ...string) (conversationaction.ConversationMessage, error) {
	return servicesessionaction.NewSendServiceTextMessageAction(f.db, f.tasks).Execute(context.Background(), f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body, Visibility: visibility, MentionIdentityIDs: mentions,
	})
}

// history 按指定成员的可见范围读取会话消息。
func (f directServiceFixture) history(t *testing.T, identity *servermodels.Identity, conversationID string) []conversationaction.ConversationMessage {
	t.Helper()
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(context.Background(), identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	require.NoError(t, err)
	return history.Messages
}

// service 读取会话承载的服务会话。
func (f directServiceFixture) service(t *testing.T, conversationID string) servermodels.ServiceConversation {
	t.Helper()
	service := servermodels.ServiceConversation{}
	require.NoError(t, f.db.NewSelect().Model(&service).Where("svc.conversation_id = ?", conversationID).Scan(context.Background()))
	return service
}

// statuses 按时间顺序读取发起人看到的服务进度。
func statuses(messages []conversationaction.ConversationMessage) []domain.ServiceRequestStatus {
	result := make([]domain.ServiceRequestStatus, 0)
	for _, message := range messages {
		if message.SystemEvent != nil && message.SystemEvent.Type == domain.ConversationSystemEventServiceStatusChanged && message.SystemEvent.Status != nil {
			result = append(result, *message.SystemEvent.Status)
		}
	}
	return result
}

// hasVisibility 判断消息列表中是否含有指定可见范围的消息。
func hasVisibility(messages []conversationaction.ConversationMessage, visibility domain.MessageVisibility) bool {
	return slices.ContainsFunc(messages, func(message conversationaction.ConversationMessage) bool { return message.Visibility == visibility })
}

// TestDirectServiceConversation 验证成员单聊服务员工的 AI 员工时的服务周期、转人工、角色可见性、交还限制与服务对象变更。
func TestDirectServiceConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newDirectServiceFixture(t)
	coordinator := testServiceSessionReturner(f.db)
	scheduler := agentrunaction.NewScheduler(f.tasks)

	// 服务对象不含员工的 AI 员工只是试聊，不产生服务会话。
	trial := f.newAgent(t, "客服专员", domain.ServiceAudienceCustomer)
	trialID := f.startChat(t, trial.IdentityID, "试一下")
	served, err := f.db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Where("svc.conversation_id = ?", trialID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, served, "试聊不应产生服务会话")
	require.Equal(t, string(domain.AgentExecutionScopeConversation), (handoffFixture{db: f.db}).queuedRun(t, trialID).ScopeKind)

	// 服务员工的 AI 员工单聊开启由其负责的服务周期。
	conversationID := f.startChat(t, f.agent.IdentityID, "电脑连不上 VPN")
	service := f.service(t, conversationID)
	require.Equal(t, string(domain.ServiceSourceDirect), service.Source)
	require.Equal(t, string(domain.ServiceAudienceEmployee), service.Audience)
	require.NotNil(t, service.CurrentServiceSessionID)
	first := loadSession(t, f.db, *service.CurrentServiceSessionID)
	require.NotNil(t, first.AssigneeIdentityID)
	require.Equal(t, f.agent.IdentityID, *first.AssigneeIdentityID)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), first.Status)
	run := (handoffFixture{db: f.db}).queuedRun(t, conversationID)
	require.Equal(t, string(domain.AgentExecutionScopeServiceSession), run.ScopeKind)
	require.Equal(t, first.ID, run.ScopeID)

	// AI 员工以员工服务场景运行，办不了时转给「办不了交给谁」配置的团队。
	var scene agentruntime.Scene
	runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		scene = request.Assignment.Scene
		return handoffRuntime("需要 IT 同事排查", nil).Run(ctx, request, feed)
	}}
	require.NoError(t, newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.Equal(t, agentruntime.SceneEmployeeService, scene)
	first = loadSession(t, f.db, first.ID)
	require.Nil(t, first.AssigneeIdentityID)
	require.NotNil(t, first.TeamID)
	require.Equal(t, f.team.ID, *first.TeamID)
	require.NotNil(t, first.AwaitingReplySince)
	requesterView := f.history(t, f.owner, conversationID)
	require.Equal(t, []domain.ServiceRequestStatus{domain.ServiceRequestStatusHandedOff}, statuses(requesterView))
	require.False(t, hasVisibility(requesterView, domain.MessageVisibilityInternal), "requester view")
	handlerView := f.history(t, f.member, conversationID)
	require.Empty(t, statuses(handlerView))
	require.True(t, hasVisibility(handlerView, domain.MessageVisibilityInternal), "handler view")

	// 处理人在收件箱按来源筛选看到该服务会话。
	page, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll, Source: domain.ServiceSourceDirect})
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(page.Conversations, func(summary inboxaction.ConversationSummary) bool {
		return summary.ID == conversationID && summary.Service != nil && summary.Service.AgentIdentityID != nil && *summary.Service.AgentIdentityID == f.agent.IdentityID
	}), "inbox page = %+v", page)

	// 处理人领取后发起人看到处理中；内部备注不能提醒发起人，也不出现在发起人的时间线和聊天预览中。
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.member, conversationID)
	require.NoError(t, err)
	var conflict *conversationaction.ConflictError
	_, err = f.reply(conversationID, "@群主 看一下", domain.MessageVisibilityInternal, f.owner.WorkspaceIdentity.ID)
	require.ErrorAs(t, err, &conflict, "提醒发起人的内部备注应被拒绝")
	require.Equal(t, servicesessionaction.ConflictReasonNoteMentionTargetInvalid, conflict.Reason, "提醒发起人的内部备注应被拒绝")
	_, err = f.reply(conversationID, "先查一下账号", domain.MessageVisibilityInternal)
	require.NoError(t, err)
	// 发起人的 AI 聊天以服务进度作预览并计入未读，任何视角的列表都不露出内部备注。
	summary, err := inboxaction.NewLoadInboxQuery(f.db).LoadAgentConversation(ctx, f.owner, conversationID)
	require.NoError(t, err)
	require.NotNil(t, summary.Agent)
	require.NotNil(t, summary.LastMessageType)
	require.Equal(t, domain.MessageTypeSystem, *summary.LastMessageType)
	require.Equal(t, 2, summary.UnreadCount)
	ownerPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, Source: domain.ServiceSourceDirect})
	require.NoError(t, err)
	for _, conversation := range ownerPage.Conversations {
		if conversation.ID == conversationID {
			require.NotNil(t, conversation.Service)
			require.NotNil(t, conversation.LastMessageType)
			require.Equal(t, domain.MessageTypeSystem, *conversation.LastMessageType)
			if conversation.Service.PreviewVisibility != nil {
				require.NotEqual(t, domain.MessageVisibilityInternal, *conversation.Service.PreviewVisibility, "发起人的服务列表不应露出内部备注")
			}
		}
	}
	// 发起人即使开启接待也不能领取自己的请求。
	_, err = f.db.NewUpdate().Table("workspace_identities").Set("handles_service_requests = true").Where("id = ?", f.owner.WorkspaceIdentity.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.owner, conversationID)
	require.ErrorAs(t, err, &conflict, "发起人不能领取自己的请求")
	require.Equal(t, servicesessionaction.ConflictReasonServiceSessionOwnRequest, conflict.Reason, "发起人不能领取自己的请求")
	answer, err := f.reply(conversationID, "请重启客户端再试", domain.MessageVisibilityShared)
	require.NoError(t, err)
	requesterView = f.history(t, f.owner, conversationID)
	require.Equal(t, []domain.ServiceRequestStatus{domain.ServiceRequestStatusHandedOff, domain.ServiceRequestStatusProcessing}, statuses(requesterView))
	require.False(t, hasVisibility(requesterView, domain.MessageVisibilityInternal), "requester view after reply")
	require.Equal(t, answer.ID, requesterView[len(requesterView)-1].ID)
	first = loadSession(t, f.db, first.ID)
	require.Nil(t, first.AwaitingReplySince, "回复后不应再等待")

	// 真人负责期间发起人的消息进入当前周期并开始等待，不调度 AI 员工。
	followUp := f.ask(t, conversationID, "还是不行")
	first = loadSession(t, f.db, first.ID)
	require.NotNil(t, first.AwaitingReplySince)
	require.Equal(t, followUp.ID, first.LastMessageID)
	queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Exists(ctx)
	require.NoError(t, err)
	require.False(t, queued, "真人负责时不应调度 AI 员工")

	// 关闭后发起人看到服务结束，下一次提问开启由 AI 员工负责的新周期。
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.member, conversationID)
	require.NoError(t, err)
	// 周期的接待 AI 员工记为该 AI 员工；待补知识以发起人在转人工前的提问作为问题。
	closedFirst := loadSession(t, f.db, first.ID)
	require.NotNil(t, closedFirst.AgentIdentityID)
	require.Equal(t, f.agent.IdentityID, *closedFirst.AgentIdentityID)
	require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		return knowledgegap.RecordClosed(ctx, tx, f.tasks, &closedFirst)
	}))
	var gap servermodels.KnowledgeGap
	require.NoError(t, f.db.NewSelect().Model(&gap).Where("kg.service_session_id = ?", first.ID).Scan(ctx))
	require.NotNil(t, gap.QuestionMessageID)
	require.Equal(t, first.OpeningMessageID, *gap.QuestionMessageID)
	f.ask(t, conversationID, "另一个问题：打印机没反应")
	second := loadSession(t, f.db, *f.service(t, conversationID).CurrentServiceSessionID)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, int64(2), second.Sequence)
	require.NotNil(t, second.AssigneeIdentityID)
	require.Equal(t, f.agent.IdentityID, *second.AssigneeIdentityID)
	require.Equal(t, []domain.ServiceRequestStatus{
		domain.ServiceRequestStatusHandedOff, domain.ServiceRequestStatusProcessing, domain.ServiceRequestStatusClosed,
	}, statuses(f.history(t, f.owner, conversationID)), "发起人应看到已转交、处理中、已结束")

	// 真人只能把 单聊交还给该会话的 AI 员工。
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.member, conversationID)
	require.NoError(t, err)
	other := f.newAgent(t, "行政服务台", domain.ServiceAudienceEmployee)
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, f.tasks)
	var validation *conversationaction.ValidationError
	_, err = transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: other.IdentityID,
	})
	require.ErrorAs(t, err, &validation, "不应转给其他 AI 员工")
	_, err = transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.agent.IdentityID,
	})
	require.NoError(t, err, "交还本会话的 AI 员工失败")
	second = loadSession(t, f.db, second.ID)
	require.NotNil(t, second.AssigneeIdentityID)
	require.Equal(t, f.agent.IdentityID, *second.AssigneeIdentityID)
	handedBack := f.history(t, f.owner, conversationID)
	handedBackLast := handedBack[len(handedBack)-1]
	require.NotNil(t, handedBackLast.SystemEvent)
	require.NotNil(t, handedBackLast.SystemEvent.Status)
	require.Equal(t, domain.ServiceRequestStatusProcessing, *handedBackLast.SystemEvent.Status)
	require.NotNil(t, handedBackLast.SystemEvent.Target)
	require.NotNil(t, handedBackLast.SystemEvent.Target.DisplayName)
	require.Equal(t, f.agent.DisplayName, *handedBackLast.SystemEvent.Target.DisplayName)

	// AI 员工不再服务员工时把其负责的单聊周期退回「办不了交给谁」的团队，发起人看到已转交该团队；之后的新对话按试聊处理。
	f.updateAgent(t, f.agent, []domain.ServiceAudience{domain.ServiceAudienceCustomer}, f.team.ID)
	second = loadSession(t, f.db, second.ID)
	require.Nil(t, second.AssigneeIdentityID)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), second.Status)
	require.NotNil(t, second.TeamID)
	require.Equal(t, f.team.ID, *second.TeamID)
	messages := f.history(t, f.owner, conversationID)
	last := messages[len(messages)-1]
	require.NotNil(t, last.SystemEvent)
	require.NotNil(t, last.SystemEvent.Status)
	require.Equal(t, domain.ServiceRequestStatusHandedOff, *last.SystemEvent.Status)
	require.NotNil(t, last.SystemEvent.Target)
	require.NotNil(t, last.SystemEvent.Target.TeamID)
	require.Equal(t, f.team.ID, *last.SystemEvent.Target.TeamID)
	laterID := f.startChat(t, f.agent.IdentityID, "再问一个")
	served, err = f.db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Where("svc.conversation_id = ?", laterID).Exists(ctx)
	require.NoError(t, err)
	require.False(t, served, "不服务员工后不应产生服务会话")
	// AI 员工停用后，发起人仍能继续进行中的周期。
	_, err = f.db.NewUpdate().Table("agents").Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", f.agent.IdentityID).Exec(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, f.ask(t, conversationID, "还在等处理").ID, "停用 AI 员工后应能继续进行中的周期")
	var payload domain.ServiceStatusChangedEvent
	var raw servermodels.Message
	require.NoError(t, f.db.NewSelect().Model(&raw).Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceStatusChanged).
		OrderExpr("msg.message_seq").Limit(1).Scan(ctx))
	require.NoError(t, json.Unmarshal(raw.SystemEventPayload, &payload))
	require.Equal(t, string(domain.MessageVisibilityRequester), raw.Visibility)
	require.NotNil(t, payload.Target)
	require.NotNil(t, payload.Target.TeamName)
	require.Equal(t, f.team.Name, *payload.Target.TeamName)

	// 进入服务周期的消息按同一客户端消息编号重发时返回已保存的消息。
	replayInput := directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "重发的消息"}
	sendAgentText := directchataction.NewSendAgentTextMessageAction(f.db, testEnqueuer, agentrunaction.NewScheduler(f.tasks))
	sent, err := sendAgentText.Execute(ctx, f.owner, replayInput)
	require.NoError(t, err)
	joined, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.id = ? AND msg.service_session_id IS NOT NULL", sent.ID).Exists(ctx)
	require.NoError(t, err)
	require.True(t, joined, "重发测试的消息未进入服务周期")
	replayed, err := sendAgentText.Execute(ctx, f.owner, replayInput)
	require.NoError(t, err)
	require.Equal(t, sent.ID, replayed.ID)
}
