//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// resolutionRuntime 认领全部已持久化输入，把认领到的上下文交给 inspect，按需执行插入动作后返回给定的正文与结束方式。
func resolutionRuntime(content string, decision agentruntime.TerminalDecision, inspect func([]agentruntime.Message), during func()) *testAgentRuntime {
	return &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		triggers, err := feed.Peek(ctx, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		// 去掉开头系统提供的客户上下文消息，只把会话消息交给 inspect。
		if len(claimed.Messages) > 0 && strings.HasPrefix(claimed.Messages[0].ID, "customer-context:") {
			claimed.Messages = claimed.Messages[1:]
		}
		if inspect != nil {
			inspect(claimed.Messages)
		}
		if during != nil {
			during()
		}
		return agentruntime.RunResult{Content: content, Decision: decision, EndSeq: claimed.EndSeq}, nil
	}}
}

// executeQueuedRun 用给定运行时执行会话中排队的 AI 客服运行并返回执行后的运行记录。
func (f handoffFixture) executeQueuedRun(t *testing.T, conversationID string, runtime *testAgentRuntime) servermodels.AgentRun {
	t.Helper()
	ctx := context.Background()
	run := f.queuedRun(t, conversationID)
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, runtime, testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return run
}

// closedEvents 读取会话中的客服处理周期关闭事件。
func closedEvents(t *testing.T, db *bun.DB, conversationID string) []domain.ServiceSessionOperatedEvent {
	t.Helper()
	var messages []servermodels.Message
	if err := db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionClosed).
		OrderExpr("msg.message_seq").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := make([]domain.ServiceSessionOperatedEvent, 0, len(messages))
	for _, message := range messages {
		event := domain.ServiceSessionOperatedEvent{}
		if err := json.Unmarshal(message.SystemEventPayload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

// assertAgentClosed 校验周期由指定 AI 员工按给定结束方式关闭，并写入一条对应的关闭事件。
func assertAgentClosed(t *testing.T, db *bun.DB, session servermodels.ServiceSession, agentIdentityID string, reason domain.ServiceSessionCloseReason) {
	t.Helper()
	if session.Status != string(domain.ServiceSessionStatusClosed) || session.CloseReason == nil || *session.CloseReason != string(reason) ||
		session.ClosedByIdentityID == nil || *session.ClosedByIdentityID != agentIdentityID || session.ResolutionRequestedAt != nil {
		t.Fatalf("closed session = %+v", session)
	}
	events := closedEvents(t, db, session.ConversationID)
	if len(events) != 1 || events[0].ActorIdentityID != agentIdentityID || events[0].ActorDisplayName == "" ||
		events[0].CloseReason == nil || *events[0].CloseReason != reason {
		t.Fatalf("closed events = %+v", events)
	}
}

// resolutionFixture 在转人工夹具上建立 AI 超时跟进与关单所用的超时执行器与企业超时时长。
type resolutionFixture struct {
	handoffFixture
	timeouts *servicetimeout.Worker
	settings domain.ServiceTimeouts
}

// age 把周期最后一条对客消息、当前负责人接手与确认请求的时间同时拨回指定分钟数。
func (f resolutionFixture) age(t *testing.T, sessionID string, minutes int) {
	t.Helper()
	if _, err := f.db.NewUpdate().Table("service_sessions").
		Set("last_message_at = now() - make_interval(mins => ?)", minutes).
		Set("assignee_assigned_at = now() - make_interval(mins => ?)", minutes).
		Set("resolution_requested_at = CASE WHEN resolution_requested_at IS NULL THEN NULL ELSE now() - make_interval(mins => ?) END", minutes).
		Where("id = ?", sessionID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// process 执行单个周期的超时处理任务并返回处理后的周期。
func (f resolutionFixture) process(t *testing.T, sessionID string) servermodels.ServiceSession {
	t.Helper()
	if err := f.timeouts.Process(context.Background(), servicetimeout.ProcessInput{OrganizationID: f.identity.Organization.ID, ServiceSessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	return loadSession(t, f.db, sessionID)
}

// TestAgentResolution 验证 AI 确认解决后关单、确认请求与超时跟进后的客户失联关单，以及客户回复对确认请求的清除。
func TestAgentResolution(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	settings, err := customerserviceaction.LoadServiceTimeouts(context.Background(), db, identity.Organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := resolutionFixture{
		handoffFixture: handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID},
		timeouts:       servicetimeout.NewWorker(db, newTestTasks(db), agentrunaction.NewScheduler(tasks)),
		settings:       settings,
	}
	disableAutoAssignment(t, db, identity.Organization.ID)
	t.Run("客户确认解决后关单", func(t *testing.T) { testAgentResolvesSession(t, f) })
	t.Run("结束语后仍有新消息时保持开放", func(t *testing.T) { testAgentResolveWithPendingInput(t, f) })
	t.Run("确认请求后客户回复", func(t *testing.T) { testResolutionRequestClearedByCustomer(t, f) })
	t.Run("确认请求后客户失联", func(t *testing.T) { testResolutionRequestUnresponsive(t, f) })
	t.Run("超时跟进后客户失联", func(t *testing.T) { testAgentFollowUpUnresponsive(t, f) })
	t.Run("跟进轮的结束语只记为确认请求", func(t *testing.T) { testFollowUpResolveIsRequest(t, f) })
	t.Run("转回同一 AI 后重新计时", func(t *testing.T) { testResolutionTimingAfterReassignment(t, f) })
	t.Run("跟进时 AI 失去接待资格", func(t *testing.T) { testFollowUpReturnsIneligibleAgent(t, f) })
}

// testAgentResolvesSession 验证客户确认解决后 AI 发送结束语并关闭周期，客户再次来信开启新周期。
func testAgentResolvesSession(t *testing.T, f resolutionFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "解决确认客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "好的，问题解决了，谢谢")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("不客气，祝您生活愉快", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	if run.Status != string(domain.AgentRunStatusSucceeded) || run.Outcome == nil || *run.Outcome != string(domain.AgentRunOutcomeResolve) || run.ResponseMessageID == nil {
		t.Fatalf("resolve run = %+v", run)
	}
	assertAgentClosed(t, f.db, loadSession(t, f.db, run.ScopeID), agent.IdentityID, domain.ServiceSessionCloseAIResolved)
	visible, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channelID, ExternalID: input.ExternalID, ConversationID: first.Conversation.ID})
	if err != nil || len(visible.Messages) != 3 || visible.Messages[1].Body != "不客气，祝您生活愉快" ||
		visible.Messages[2].Event == nil || visible.Messages[2].Event.Type != customerchataction.VisitorEventSessionEnded ||
		len(visible.SessionRatings) != 1 || visible.SessionRatings[0].EndMessageID != visible.Messages[2].ID || !visible.SessionRatings[0].Rateable {
		t.Fatalf("visitor messages = %+v, error = %v", visible, err)
	}
	if next := f.receive(t, &input, "又有一个新问题"); !next.OpenedNewServiceSession {
		t.Fatalf("next message = %+v", next)
	}
}

// testAgentResolveWithPendingInput 验证结束语生成期间客户又发来消息时只发送结束语，周期保持开放并由下一次运行处理新消息。
func testAgentResolveWithPendingInput(t *testing.T, f resolutionFixture) {
	agent := f.newAgent(t, "待处理输入客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "解决了")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("感谢您的咨询", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil,
		func() { f.receive(t, &input, "等等，还有一个问题") }))
	if run.Status != string(domain.AgentRunStatusSucceeded) || run.Outcome == nil || *run.Outcome != string(domain.AgentRunOutcomeResolve) {
		t.Fatalf("resolve run = %+v", run)
	}
	session := loadSession(t, f.db, run.ScopeID)
	if session.Status != string(domain.ServiceSessionStatusOpen) || session.CloseReason != nil {
		t.Fatalf("session with pending input = %+v", session)
	}
	if next := f.queuedRun(t, first.Conversation.ID); next.ScopeID != session.ID {
		t.Fatalf("next run = %+v", next)
	}
	if events := closedEvents(t, f.db, first.Conversation.ID); len(events) != 0 {
		t.Fatalf("closed events = %+v", events)
	}
}

// testResolutionRequestClearedByCustomer 验证 AI 请求确认解决后记录请求时间，客户回复后清除并重新等待回复。
func testResolutionRequestClearedByCustomer(t *testing.T, f resolutionFixture) {
	agent := f.newAgent(t, "确认回复客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "怎么修改密码")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问问题解决了吗？", agentruntime.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution,
	}, nil, nil))
	session := loadSession(t, f.db, run.ScopeID)
	if session.ResolutionRequestedAt == nil || session.AwaitingReplySince != nil || session.LastMessageID != *run.ResponseMessageID {
		t.Fatalf("session after confirmation request = %+v", session)
	}
	f.receive(t, &input, "还没有")
	session = loadSession(t, f.db, run.ScopeID)
	if session.ResolutionRequestedAt != nil || session.AwaitingReplySince == nil || session.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("session after customer reply = %+v", session)
	}
	f.age(t, session.ID, f.settings.AICloseMinutes)
	if processed := f.process(t, session.ID); processed.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("session awaiting AI reply = %+v", processed)
	}
}

// testResolutionRequestUnresponsive 验证 AI 请求确认解决后客户超过关单时长未回复，周期按客户失联关闭。
func testResolutionRequestUnresponsive(t *testing.T, f resolutionFixture) {
	agent := f.newAgent(t, "确认失联客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "发票怎么开")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问还有其他问题吗？", agentruntime.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution,
	}, nil, nil))
	f.age(t, run.ScopeID, f.settings.AICloseMinutes-1)
	if session := f.process(t, run.ScopeID); session.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("session before close timeout = %+v", session)
	}
	f.age(t, run.ScopeID, f.settings.AICloseMinutes)
	assertAgentClosed(t, f.db, f.process(t, run.ScopeID), agent.IdentityID, domain.ServiceSessionCloseCustomerUnresponsive)
}

// testAgentFollowUpUnresponsive 验证 AI 回答后客户超过跟进时长未回复时追加一次跟进输入，跟进结果记为确认请求，之后仍未回复则按客户失联关闭。
func testAgentFollowUpUnresponsive(t *testing.T, f resolutionFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "超时跟进客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "营业时间是几点")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("我们每天 9 点营业", agentruntime.TerminalDecision{}, nil, nil))
	sessionID := run.ScopeID
	if session := loadSession(t, f.db, sessionID); session.ResolutionRequestedAt != nil {
		t.Fatalf("session after answer = %+v", session)
	}
	f.age(t, sessionID, f.settings.AIFollowUpMinutes-1)
	f.process(t, sessionID)
	if queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", sessionID, domain.AgentRunStatusQueued).Exists(ctx); err != nil || queued {
		t.Fatalf("follow-up before timeout = %t, error = %v", queued, err)
	}
	f.age(t, sessionID, f.settings.AIFollowUpMinutes)
	f.process(t, sessionID)
	followUp := f.queuedRun(t, first.Conversation.ID)
	var kind string
	if err := f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).Column("kind").Where("lane_id = ?", followUp.LaneID).OrderExpr("input_seq DESC").Limit(1).Scan(ctx, &kind); err != nil ||
		kind != string(domain.AgentInputKindFollowUp) {
		t.Fatalf("follow-up input kind = %q, error = %v", kind, err)
	}
	// 跟进运行在途时不重复安排跟进。
	f.process(t, sessionID)
	if count, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", sessionID, domain.AgentRunStatusQueued).Count(ctx); err != nil || count != 1 {
		t.Fatalf("queued follow-up runs = %d, error = %v", count, err)
	}
	var claimed []agentruntime.Message
	followUp = f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问还需要其他帮助吗？", agentruntime.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeClarify,
	}, func(messages []agentruntime.Message) { claimed = messages }, nil))
	if len(claimed) != 3 || claimed[1].Content != "我们每天 9 点营业" || claimed[2].Role != agentruntime.MessageRoleUser || claimed[2].Content != agentruntime.CustomerIdleMessage {
		t.Fatalf("follow-up context = %+v", claimed)
	}
	session := loadSession(t, f.db, sessionID)
	if followUp.Status != string(domain.AgentRunStatusSucceeded) || session.ResolutionRequestedAt == nil || session.LastMessageID != *followUp.ResponseMessageID {
		t.Fatalf("follow-up run = %+v, session = %+v", followUp, session)
	}
	f.age(t, sessionID, f.settings.AICloseMinutes)
	assertAgentClosed(t, f.db, f.process(t, sessionID), agent.IdentityID, domain.ServiceSessionCloseCustomerUnresponsive)
	if runs, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ?", sessionID).Count(ctx); err != nil || runs != 2 {
		t.Fatalf("session runs = %d, error = %v", runs, err)
	}
}

// answerAndFollowUp 创建 AI 员工与渠道，让 AI 回答一次后超过跟进时长，并安排一次超时跟进；返回会话编号、周期编号与 AI 员工身份编号。
func (f resolutionFixture) answerAndFollowUp(t *testing.T, name string) (string, string, string) {
	t.Helper()
	agent := f.newAgent(t, name)
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "退货地址是哪里")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("退货地址见订单详情页", agentruntime.TerminalDecision{}, nil, nil))
	f.age(t, run.ScopeID, f.settings.AIFollowUpMinutes)
	f.process(t, run.ScopeID)
	return first.Conversation.ID, run.ScopeID, agent.IdentityID
}

// testFollowUpResolveIsRequest 验证超时跟进轮中 AI 调用结束服务时只发送结束语并记为确认请求，不按 AI 解决关闭，之后仍未回复则按客户失联关闭。
func testFollowUpResolveIsRequest(t *testing.T, f resolutionFixture) {
	conversationID, sessionID, agentID := f.answerAndFollowUp(t, "跟进结束语客服")
	followUp := f.executeQueuedRun(t, conversationID, resolutionRuntime("感谢您的咨询，祝您愉快", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	session := loadSession(t, f.db, sessionID)
	if followUp.Status != string(domain.AgentRunStatusSucceeded) || session.Status != string(domain.ServiceSessionStatusOpen) ||
		session.CloseReason != nil || session.ResolutionRequestedAt == nil || session.LastMessageID != *followUp.ResponseMessageID {
		t.Fatalf("follow-up run = %+v, session = %+v", followUp, session)
	}
	f.age(t, sessionID, f.settings.AICloseMinutes)
	assertAgentClosed(t, f.db, f.process(t, sessionID), agentID, domain.ServiceSessionCloseCustomerUnresponsive)
}

// testResolutionTimingAfterReassignment 验证确认请求后真人接管再转回同一 AI 时，跟进与关单从转回时重新计时，接手前的确认请求不计入关单。
func testResolutionTimingAfterReassignment(t *testing.T, f resolutionFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "转回计时客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "积分怎么用")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问问题解决了吗？", agentruntime.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution,
	}, nil, nil))
	coordinator := testServiceSessionReturner(f.db)
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.identity, first.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	f.age(t, run.ScopeID, f.settings.AICloseMinutes+f.settings.AIFollowUpMinutes)
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(f.tasks), newTestTasks(f.db)).Execute(ctx, f.identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: first.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	}); err != nil {
		t.Fatal(err)
	}
	session := f.process(t, run.ScopeID)
	if session.Status != string(domain.ServiceSessionStatusOpen) || session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != agent.IdentityID {
		t.Fatalf("session after transfer back = %+v", session)
	}
	if queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", run.ScopeID, domain.AgentRunStatusQueued).Exists(ctx); err != nil || queued {
		t.Fatalf("follow-up right after transfer back = %t, error = %v", queued, err)
	}
	// 从转回时起超过跟进时长后先跟进，不直接关单。
	if _, err := f.db.NewUpdate().Table("service_sessions").
		Set("assignee_assigned_at = now() - make_interval(mins => ?)", f.settings.AIFollowUpMinutes).
		Where("id = ?", run.ScopeID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if session := f.process(t, run.ScopeID); session.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("session after follow-up timeout = %+v", session)
	}
	if followUp := f.queuedRun(t, first.Conversation.ID); followUp.ScopeID != run.ScopeID {
		t.Fatalf("follow-up run = %+v", followUp)
	}
}

// testFollowUpReturnsIneligibleAgent 验证超时跟进时 AI 负责人已失去接待资格，周期退回原队列且不安排跟进。
func testFollowUpReturnsIneligibleAgent(t *testing.T, f resolutionFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "失去资格客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "运费多少")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("运费按重量计算", agentruntime.TerminalDecision{}, nil, nil))
	// 直接改写服务对象，模拟未经管理操作退回的资格变化。
	if _, err := f.db.NewUpdate().Table("agents").Set("service_audiences = '{}'").Where("identity_id = ?", agent.IdentityID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	f.age(t, run.ScopeID, f.settings.AIFollowUpMinutes)
	session := f.process(t, run.ScopeID)
	if session.Status != string(domain.ServiceSessionStatusOpen) || session.AssigneeIdentityID != nil {
		t.Fatalf("session after ineligible follow-up = %+v", session)
	}
	if events := returnedEvents(t, f.db, first.Conversation.ID); len(events) != 1 || events[0].FromIdentityID != agent.IdentityID {
		t.Fatalf("returned events = %+v", events)
	}
	if queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", run.ScopeID, domain.AgentRunStatusQueued).Exists(ctx); err != nil || queued {
		t.Fatalf("follow-up run for ineligible agent = %t, error = %v", queued, err)
	}
}
