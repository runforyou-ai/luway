//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// directServiceFixture 保存 单聊服务台测试共用的企业、发起人、处理人与 AI 员工。
type directServiceFixture struct {
	navigationFixture
	tasks     *servertask.Runtime
	execution agentaction.ExecutionInput
	team      *teamaction.TeamRecord
	agent     *agentaction.Agent
}

// newDirectServiceFixture 创建服务员工、转人工到 IT 团队的 AI 员工；群主作为发起人，开启接待的成员属于 IT 团队并作为处理人。
func newDirectServiceFixture(t *testing.T) directServiceFixture {
	t.Helper()
	ctx := context.Background()
	f := directServiceFixture{navigationFixture: newNavigationFixture(t)}
	disableAutoAssignment(t, f.db, f.owner.Organization.ID)
	f.tasks = newTestTasks(f.db)
	if err := f.tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	provider, err := aiprovideraction.NewCreateAIProviderAction(f.db).Execute(ctx, f.owner, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{
			Identifier: "chat-a", Name: "对话 A", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 4096,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.execution = agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: aiModelID(t, f.db, provider.ID, "chat-a")}}
	if f.team, err = teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "IT 支持"}); err != nil {
		t.Fatal(err)
	}
	if _, err := teamaction.NewAddMembersAction(f.db, f.tasks).Execute(ctx, f.owner, f.team.ID, []teamaction.MemberIdentity{{
		IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: f.member.OrganizationIdentity.ID,
	}}); err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	return created
}

// updateAgent 修改 AI 员工的服务对象与转人工团队。
func (f directServiceFixture) updateAgent(t *testing.T, agent *agentaction.Agent, audiences []domain.ServiceAudience, handoffTeamID string) {
	t.Helper()
	if _, err := agentaction.NewUpdateAgentAction(f.db, testServiceSessionReturner(f.db)).Execute(context.Background(), f.owner, agent.ID, agentaction.UpdateInput{
		DisplayName: agent.DisplayName, TeamIDs: []string{}, WorkStatus: domain.WorkStatusWorking, ServiceAudiences: audiences, HandoffTeamID: handoffTeamID,
	}); err != nil {
		t.Fatal(err)
	}
}

// startChat 由发起人向 AI 员工发出首条消息并返回会话编号。
func (f directServiceFixture) startChat(t *testing.T, agentIdentityID, body string) string {
	t.Helper()
	conversationID := uuid.NewV7().String()
	if _, err := directchataction.NewSendFirstAgentTextMessageAction(f.db, agentrunaction.NewScheduler(f.tasks)).Execute(context.Background(), f.owner, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: agentIdentityID, ClientMessageID: uuid.NewV7().String(), Body: body,
	}); err != nil {
		t.Fatal(err)
	}
	return conversationID
}

// ask 由发起人在已有 AI 聊天中继续发言。
func (f directServiceFixture) ask(t *testing.T, conversationID, body string) conversationaction.ConversationMessage {
	t.Helper()
	message, err := directchataction.NewSendAgentTextMessageAction(f.db, agentrunaction.NewScheduler(f.tasks)).Execute(context.Background(), f.owner, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body,
	})
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	return history.Messages
}

// service 读取会话承载的服务会话。
func (f directServiceFixture) service(t *testing.T, conversationID string) servermodels.ServiceConversation {
	t.Helper()
	service := servermodels.ServiceConversation{}
	if err := f.db.NewSelect().Model(&service).Where("svc.conversation_id = ?", conversationID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	if served, err := f.db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Where("svc.conversation_id = ?", trialID).Exists(ctx); err != nil || served {
		t.Fatalf("试聊不应产生服务会话：%t, %v", served, err)
	}
	if run := (handoffFixture{db: f.db}).queuedRun(t, trialID); run.ScopeKind != string(domain.AgentExecutionScopeConversation) {
		t.Fatalf("trial run = %+v", run)
	}

	// 服务员工的 AI 员工单聊开启由其负责的服务周期。
	conversationID := f.startChat(t, f.agent.IdentityID, "电脑连不上 VPN")
	service := f.service(t, conversationID)
	if service.Source != string(domain.ServiceSourceDirect) || service.Audience != string(domain.ServiceAudienceEmployee) || service.CurrentServiceSessionID == nil {
		t.Fatalf("service = %+v", service)
	}
	first := loadSession(t, f.db, *service.CurrentServiceSessionID)
	if first.AssigneeIdentityID == nil || *first.AssigneeIdentityID != f.agent.IdentityID || first.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("first session = %+v", first)
	}
	run := (handoffFixture{db: f.db}).queuedRun(t, conversationID)
	if run.ScopeKind != string(domain.AgentExecutionScopeServiceSession) || run.ScopeID != first.ID {
		t.Fatalf("service run = %+v", run)
	}

	// AI 员工以员工服务场景运行，办不了时转给「办不了交给谁」配置的团队。
	var scene agentruntime.Scene
	runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		scene = request.Assignment.Scene
		return handoffRuntime("需要 IT 同事排查", nil).Run(ctx, request, feed)
	}}
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if scene != agentruntime.SceneEmployeeService {
		t.Fatalf("scene = %q", scene)
	}
	first = loadSession(t, f.db, first.ID)
	if first.AssigneeIdentityID != nil || first.TeamID == nil || *first.TeamID != f.team.ID || first.AwaitingReplySince == nil {
		t.Fatalf("handed off session = %+v", first)
	}
	requesterView := f.history(t, f.owner, conversationID)
	if !slices.Equal(statuses(requesterView), []domain.ServiceRequestStatus{domain.ServiceRequestStatusHandedOff}) || hasVisibility(requesterView, domain.MessageVisibilityInternal) {
		t.Fatalf("requester view = %+v", requesterView)
	}
	handlerView := f.history(t, f.member, conversationID)
	if len(statuses(handlerView)) != 0 || !hasVisibility(handlerView, domain.MessageVisibilityInternal) {
		t.Fatalf("handler view = %+v", handlerView)
	}

	// 处理人在收件箱按来源筛选看到该服务会话。
	page, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.member, inboxaction.LoadInput{Scope: domain.InboxScopeAll, Source: domain.ServiceSourceDirect})
	if err != nil || !slices.ContainsFunc(page.Conversations, func(summary inboxaction.ConversationSummary) bool {
		return summary.ID == conversationID && summary.Service != nil && summary.Service.AgentIdentityID != nil && *summary.Service.AgentIdentityID == f.agent.IdentityID
	}) {
		t.Fatalf("inbox page = %+v, error = %v", page, err)
	}

	// 处理人领取后发起人看到处理中；内部备注不能提醒发起人，也不出现在发起人的时间线和聊天预览中。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.member, conversationID); err != nil {
		t.Fatal(err)
	}
	var conflict *conversationaction.ConflictError
	if _, err := f.reply(conversationID, "@群主 看一下", domain.MessageVisibilityInternal, f.owner.OrganizationIdentity.ID); !errors.As(err, &conflict) || conflict.Reason != servicesessionaction.ConflictReasonNoteMentionTargetInvalid {
		t.Fatalf("提醒发起人的内部备注应被拒绝：%v", err)
	}
	if _, err := f.reply(conversationID, "先查一下账号", domain.MessageVisibilityInternal); err != nil {
		t.Fatal(err)
	}
	// 发起人的 AI 聊天以服务进度作预览并计入未读，任何视角的列表都不露出内部备注。
	summary, err := inboxaction.NewLoadInboxQuery(f.db).LoadAgentConversation(ctx, f.owner, conversationID)
	if err != nil || summary.Agent == nil || summary.LastMessageType == nil || *summary.LastMessageType != domain.MessageTypeSystem || summary.UnreadCount != 2 {
		t.Fatalf("requester chat summary = %+v, error = %v", summary, err)
	}
	ownerPage, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(ctx, f.owner, inboxaction.LoadInput{Scope: domain.InboxScopeAll, Source: domain.ServiceSourceDirect})
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range ownerPage.Conversations {
		if conversation.ID == conversationID && (conversation.Service == nil || conversation.LastMessageType == nil || *conversation.LastMessageType != domain.MessageTypeSystem ||
			(conversation.Service.PreviewVisibility != nil && *conversation.Service.PreviewVisibility == domain.MessageVisibilityInternal)) {
			t.Fatalf("发起人的服务列表应以服务进度作预览且不露出内部备注：%+v", conversation.Service)
		}
	}
	// 发起人即使开启接待也不能领取自己的请求。
	if _, err := f.db.NewUpdate().Table("organization_identities").Set("handles_service_requests = true").Where("id = ?", f.owner.OrganizationIdentity.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.owner, conversationID); !errors.As(err, &conflict) || conflict.Reason != servicesessionaction.ConflictReasonServiceSessionOwnRequest {
		t.Fatalf("发起人不能领取自己的请求：%v", err)
	}
	answer, err := f.reply(conversationID, "请重启客户端再试", domain.MessageVisibilityShared)
	if err != nil {
		t.Fatal(err)
	}
	requesterView = f.history(t, f.owner, conversationID)
	if !slices.Equal(statuses(requesterView), []domain.ServiceRequestStatus{domain.ServiceRequestStatusHandedOff, domain.ServiceRequestStatusProcessing}) ||
		hasVisibility(requesterView, domain.MessageVisibilityInternal) || requesterView[len(requesterView)-1].ID != answer.ID {
		t.Fatalf("requester view after reply = %+v", requesterView)
	}
	if first = loadSession(t, f.db, first.ID); first.AwaitingReplySince != nil {
		t.Fatalf("回复后不应再等待：%+v", first)
	}

	// 真人负责期间发起人的消息进入当前周期并开始等待，不调度 AI 员工。
	followUp := f.ask(t, conversationID, "还是不行")
	if first = loadSession(t, f.db, first.ID); first.AwaitingReplySince == nil || first.LastMessageID != followUp.ID {
		t.Fatalf("follow up session = %+v", first)
	}
	if queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Exists(ctx); err != nil || queued {
		t.Fatalf("真人负责时不应调度 AI 员工：%t, %v", queued, err)
	}

	// 关闭后发起人看到服务结束，下一次提问开启由 AI 员工负责的新周期。
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.member, conversationID); err != nil {
		t.Fatal(err)
	}
	// 周期的接待 AI 员工记为该 AI 员工；待补知识以发起人在转人工前的提问作为问题。
	closedFirst := loadSession(t, f.db, first.ID)
	if closedFirst.AgentIdentityID == nil || *closedFirst.AgentIdentityID != f.agent.IdentityID {
		t.Fatalf("closed first session = %+v", closedFirst)
	}
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		return knowledgegap.RecordClosed(ctx, tx, f.tasks, &closedFirst)
	}); err != nil {
		t.Fatal(err)
	}
	var gap servermodels.KnowledgeGap
	if err := f.db.NewSelect().Model(&gap).Where("kg.service_session_id = ?", first.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if gap.QuestionMessageID == nil || *gap.QuestionMessageID != first.OpeningMessageID {
		t.Fatalf("direct service gap = %+v", gap)
	}
	f.ask(t, conversationID, "另一个问题：打印机没反应")
	second := loadSession(t, f.db, *f.service(t, conversationID).CurrentServiceSessionID)
	if second.ID == first.ID || second.Sequence != 2 || second.AssigneeIdentityID == nil || *second.AssigneeIdentityID != f.agent.IdentityID {
		t.Fatalf("second session = %+v", second)
	}
	if !slices.Equal(statuses(f.history(t, f.owner, conversationID)), []domain.ServiceRequestStatus{
		domain.ServiceRequestStatusHandedOff, domain.ServiceRequestStatusProcessing, domain.ServiceRequestStatusClosed,
	}) {
		t.Fatal("发起人应看到已转交、处理中、已结束")
	}

	// 真人只能把 单聊交还给该会话的 AI 员工。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, f.tasks).Execute(ctx, f.member, conversationID); err != nil {
		t.Fatal(err)
	}
	other := f.newAgent(t, "行政服务台", domain.ServiceAudienceEmployee)
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, f.tasks)
	var validation *conversationaction.ValidationError
	if _, err := transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: other.IdentityID,
	}); !errors.As(err, &validation) {
		t.Fatalf("不应转给其他 AI 员工：%v", err)
	}
	if _, err := transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.agent.IdentityID,
	}); err != nil {
		t.Fatalf("交还本会话的 AI 员工失败：%v", err)
	}
	if second = loadSession(t, f.db, second.ID); second.AssigneeIdentityID == nil || *second.AssigneeIdentityID != f.agent.IdentityID {
		t.Fatalf("returned to agent = %+v", second)
	}
	handedBack := f.history(t, f.owner, conversationID)
	if last := handedBack[len(handedBack)-1]; last.SystemEvent == nil || last.SystemEvent.Status == nil || *last.SystemEvent.Status != domain.ServiceRequestStatusProcessing ||
		last.SystemEvent.Target == nil || last.SystemEvent.Target.DisplayName == nil || *last.SystemEvent.Target.DisplayName != f.agent.DisplayName {
		t.Fatalf("交还后发起人应看到 AI 员工处理中：%+v", last)
	}

	// AI 员工不再服务员工时把其负责的单聊周期退回「办不了交给谁」的团队，发起人看到已转交该团队；之后的新对话按试聊处理。
	f.updateAgent(t, f.agent, []domain.ServiceAudience{domain.ServiceAudienceCustomer}, f.team.ID)
	if second = loadSession(t, f.db, second.ID); second.AssigneeIdentityID != nil || second.Status != string(domain.ServiceSessionStatusOpen) ||
		second.TeamID == nil || *second.TeamID != f.team.ID {
		t.Fatalf("returned session = %+v", second)
	}
	messages := f.history(t, f.owner, conversationID)
	last := messages[len(messages)-1]
	if last.SystemEvent == nil || last.SystemEvent.Status == nil || *last.SystemEvent.Status != domain.ServiceRequestStatusHandedOff ||
		last.SystemEvent.Target == nil || last.SystemEvent.Target.TeamID == nil || *last.SystemEvent.Target.TeamID != f.team.ID {
		t.Fatalf("last requester message = %+v", last)
	}
	laterID := f.startChat(t, f.agent.IdentityID, "再问一个")
	if served, err := f.db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Where("svc.conversation_id = ?", laterID).Exists(ctx); err != nil || served {
		t.Fatalf("不服务员工后不应产生服务会话：%t, %v", served, err)
	}
	// AI 员工停用后，发起人仍能继续进行中的周期。
	if _, err := f.db.NewUpdate().Table("agents").Set("status = ?", domain.IdentityStatusInactive).Where("identity_id = ?", f.agent.IdentityID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if message := f.ask(t, conversationID, "还在等处理"); message.ID == "" {
		t.Fatal("停用 AI 员工后应能继续进行中的周期")
	}
	var payload domain.ServiceStatusChangedEvent
	var raw servermodels.Message
	if err := f.db.NewSelect().Model(&raw).Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceStatusChanged).
		OrderExpr("msg.message_seq").Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw.SystemEventPayload, &payload); err != nil || raw.Visibility != string(domain.MessageVisibilityRequester) ||
		payload.Target == nil || payload.Target.TeamName == nil || *payload.Target.TeamName != f.team.Name {
		t.Fatalf("handed off status = %+v, payload = %+v, error = %v", raw, payload, err)
	}

	// 进入服务周期的消息按同一客户端消息编号重发时返回已保存的消息。
	replayInput := directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "重发的消息"}
	sendAgentText := directchataction.NewSendAgentTextMessageAction(f.db, agentrunaction.NewScheduler(f.tasks))
	sent, err := sendAgentText.Execute(ctx, f.owner, replayInput)
	if err != nil {
		t.Fatal(err)
	}
	if joined, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.id = ? AND msg.service_session_id IS NOT NULL", sent.ID).Exists(ctx); err != nil || !joined {
		t.Fatalf("重发测试的消息未进入服务周期：%t, %v", joined, err)
	}
	if replayed, err := sendAgentText.Execute(ctx, f.owner, replayInput); err != nil || replayed.ID != sent.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
}
