//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/runforyou-ai/einorun"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// resolutionRuntime 认领全部已持久化输入，把认领到的上下文交给 inspect，按需执行插入动作后返回给定的正文与结束方式。
func resolutionRuntime(content string, decision agentcontract.TerminalDecision, inspect func([]einorun.Message), during func()) *testAgentRuntime {
	return &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
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
	require.NoError(t, newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, f.db.NewSelect().Model(&run).WherePK().Scan(ctx))
	return run
}

// closedEvents 读取会话中的客服处理周期关闭事件。
func closedEvents(t *testing.T, db *bun.DB, conversationID string) []domain.ServiceSessionOperatedEvent {
	t.Helper()
	var messages []servermodels.Message
	require.NoError(t, db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionClosed).
		OrderExpr("msg.message_seq").Scan(context.Background()))
	events := make([]domain.ServiceSessionOperatedEvent, 0, len(messages))
	for _, message := range messages {
		event := domain.ServiceSessionOperatedEvent{}
		require.NoError(t, json.Unmarshal(message.SystemEventPayload, &event))
		events = append(events, event)
	}
	return events
}

// assertAgentClosed 校验周期由指定 AI 员工按给定结束方式关闭，并写入一条对应的关闭事件。
func assertAgentClosed(t *testing.T, db *bun.DB, session servermodels.ServiceSession, agentIdentityID string, reason domain.ServiceSessionCloseReason) {
	t.Helper()
	require.Equal(t, string(domain.ServiceSessionStatusClosed), session.Status)
	require.NotNil(t, session.CloseReason)
	require.Equal(t, string(reason), *session.CloseReason)
	require.NotNil(t, session.ClosedByIdentityID)
	require.Equal(t, agentIdentityID, *session.ClosedByIdentityID)
	require.Nil(t, session.ResolutionRequestedAt)
	events := closedEvents(t, db, session.ConversationID)
	require.Len(t, events, 1)
	require.Equal(t, agentIdentityID, events[0].ActorIdentityID)
	require.NotEmpty(t, events[0].ActorDisplayName)
	require.NotNil(t, events[0].CloseReason)
	require.Equal(t, reason, *events[0].CloseReason)
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
	_, err := f.db.NewUpdate().Table("service_sessions").
		Set("last_message_at = now() - make_interval(mins => ?)", minutes).
		Set("assignee_assigned_at = now() - make_interval(mins => ?)", minutes).
		Set("resolution_requested_at = CASE WHEN resolution_requested_at IS NULL THEN NULL ELSE now() - make_interval(mins => ?) END", minutes).
		Where("id = ?", sessionID).Exec(context.Background())
	require.NoError(t, err)
}

// process 执行单个周期的超时处理任务并返回处理后的周期。
func (f resolutionFixture) process(t *testing.T, sessionID string) servermodels.ServiceSession {
	t.Helper()
	require.NoError(t, f.timeouts.Process(context.Background(), servicetimeout.ProcessInput{WorkspaceID: f.identity.Workspace.ID, ServiceSessionID: sessionID}))
	return loadSession(t, f.db, sessionID)
}

// TestAgentResolution 验证 AI 确认解决后关单、确认请求与超时跟进后的客户失联关单，以及客户回复对确认请求的清除。
func TestAgentResolution(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	tasks := servertest.NewTasks()
	settings, err := customerserviceaction.LoadServiceTimeouts(context.Background(), db, identity.Workspace.ID)
	require.NoError(t, err)
	f := resolutionFixture{
		handoffFixture: handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID},
		timeouts:       servicetimeout.NewWorker(db, testEnqueuer, agentrunaction.NewScheduler(tasks)),
		settings:       settings,
	}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	t.Run("客户确认解决后关单", func(t *testing.T) { testAgentResolvesSession(t, f) })
	t.Run("结束语后仍有新消息时保持开放", func(t *testing.T) { testAgentResolveWithPendingInput(t, f) })
	t.Run("确认请求后客户回复", func(t *testing.T) { testResolutionRequestClearedByCustomer(t, f) })
	t.Run("确认请求后客户失联", func(t *testing.T) { testResolutionRequestUnresponsive(t, f) })
	t.Run("超时跟进后客户失联", func(t *testing.T) { testAgentFollowUpUnresponsive(t, f) })
	t.Run("跟进轮的结束语只记为确认请求", func(t *testing.T) { testFollowUpResolveIsRequest(t, f) })
	t.Run("转回同一 AI 后重新计时", func(t *testing.T) { testResolutionTimingAfterReassignment(t, f) })
}

// testAgentResolvesSession 验证客户确认解决后 AI 发送结束语并关闭周期，客户再次来信开启新周期。
func testAgentResolvesSession(t *testing.T, f resolutionFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "解决确认客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "好的，问题解决了，谢谢")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("不客气，祝您生活愉快", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status)
	require.NotNil(t, run.Outcome)
	require.Equal(t, string(domain.AgentRunOutcomeResolve), *run.Outcome)
	require.NotNil(t, run.ResponseMessageID)
	assertAgentClosed(t, f.db, loadSession(t, f.db, run.ScopeID), agent.IdentityID, domain.ServiceSessionCloseAIResolved)
	visible, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channelID, ExternalID: input.ExternalID, ConversationID: first.Conversation.ID})
	require.NoError(t, err)
	require.Len(t, visible.Messages, 3)
	require.Equal(t, "不客气，祝您生活愉快", visible.Messages[1].Body)
	require.NotNil(t, visible.Messages[2].Event)
	require.Equal(t, customerchataction.VisitorEventSessionEnded, visible.Messages[2].Event.Type)
	require.Len(t, visible.SessionRatings, 1)
	require.Equal(t, visible.Messages[2].ID, visible.SessionRatings[0].EndMessageID)
	require.True(t, visible.SessionRatings[0].Rateable)
	next := f.receive(t, &input, "又有一个新问题")
	require.True(t, next.OpenedNewServiceSession, "next message = %+v", next)
}

// testAgentResolveWithPendingInput 验证结束语生成期间客户又发来消息时只发送结束语，周期保持开放并由下一次运行处理新消息。
func testAgentResolveWithPendingInput(t *testing.T, f resolutionFixture) {
	agent := f.newAgent(t, "待处理输入客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "解决了")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("感谢您的咨询", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil,
		func() { f.receive(t, &input, "等等，还有一个问题") }))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status)
	require.NotNil(t, run.Outcome)
	require.Equal(t, string(domain.AgentRunOutcomeResolve), *run.Outcome)
	session := loadSession(t, f.db, run.ScopeID)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), session.Status, "session with pending input")
	require.Nil(t, session.CloseReason, "session with pending input")
	require.Equal(t, session.ID, f.queuedRun(t, first.Conversation.ID).ScopeID, "next run")
	require.Empty(t, closedEvents(t, f.db, first.Conversation.ID), "closed events")
}

// testResolutionRequestClearedByCustomer 验证 AI 请求确认解决后记录请求时间，客户回复后清除并重新等待回复。
func testResolutionRequestClearedByCustomer(t *testing.T, f resolutionFixture) {
	agent := f.newAgent(t, "确认回复客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "怎么修改密码")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问问题解决了吗？", agentcontract.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution,
	}, nil, nil))
	session := loadSession(t, f.db, run.ScopeID)
	require.NotNil(t, session.ResolutionRequestedAt)
	require.Nil(t, session.AwaitingReplySince)
	require.Equal(t, *run.ResponseMessageID, session.LastMessageID)
	f.receive(t, &input, "还没有")
	session = loadSession(t, f.db, run.ScopeID)
	require.Nil(t, session.ResolutionRequestedAt)
	require.NotNil(t, session.AwaitingReplySince)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), session.Status)
	f.age(t, session.ID, f.settings.AICloseMinutes)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), f.process(t, session.ID).Status, "session awaiting AI reply")
}

// testResolutionRequestUnresponsive 验证 AI 请求确认解决后客户超过关单时长未回复，周期按客户失联关闭。
func testResolutionRequestUnresponsive(t *testing.T, f resolutionFixture) {
	agent := f.newAgent(t, "确认失联客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "发票怎么开")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问还有其他问题吗？", agentcontract.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution,
	}, nil, nil))
	f.age(t, run.ScopeID, f.settings.AICloseMinutes-1)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), f.process(t, run.ScopeID).Status, "session before close timeout")
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
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("我们每天 9 点营业", agentcontract.TerminalDecision{}, nil, nil))
	sessionID := run.ScopeID
	require.Nil(t, loadSession(t, f.db, sessionID).ResolutionRequestedAt, "session after answer")
	f.age(t, sessionID, f.settings.AIFollowUpMinutes-1)
	f.process(t, sessionID)
	queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", sessionID, domain.AgentRunStatusQueued).Exists(ctx)
	require.NoError(t, err)
	require.False(t, queued, "follow-up before timeout")
	f.age(t, sessionID, f.settings.AIFollowUpMinutes)
	f.process(t, sessionID)
	followUp := f.queuedRun(t, first.Conversation.ID)
	var kind string
	require.NoError(t, f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).Column("kind").Where("lane_id = ?", followUp.LaneID).OrderExpr("input_seq DESC").Limit(1).Scan(ctx, &kind))
	require.Equal(t, string(domain.AgentInputKindFollowUp), kind)
	// 跟进运行在途时不重复安排跟进。
	f.process(t, sessionID)
	count, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", sessionID, domain.AgentRunStatusQueued).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "queued follow-up runs")
	var claimed []einorun.Message
	followUp = f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问还需要其他帮助吗？", agentcontract.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeClarify,
	}, func(messages []einorun.Message) { claimed = messages }, nil))
	require.Len(t, claimed, 3, "follow-up context")
	require.Equal(t, "我们每天 9 点营业", claimed[1].Content)
	require.Equal(t, einorun.RoleUser, claimed[2].Role)
	require.Equal(t, agentruntime.CustomerIdleMessage, claimed[2].Content)
	session := loadSession(t, f.db, sessionID)
	require.Equal(t, string(domain.AgentRunStatusSucceeded), followUp.Status)
	require.NotNil(t, session.ResolutionRequestedAt)
	require.Equal(t, *followUp.ResponseMessageID, session.LastMessageID)
	f.age(t, sessionID, f.settings.AICloseMinutes)
	assertAgentClosed(t, f.db, f.process(t, sessionID), agent.IdentityID, domain.ServiceSessionCloseCustomerUnresponsive)
	runs, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ?", sessionID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), runs, "session runs")
}

// answerAndFollowUp 创建 AI 员工与渠道，让 AI 回答一次后超过跟进时长，并安排一次超时跟进；返回会话编号、周期编号与 AI 员工身份编号。
func (f resolutionFixture) answerAndFollowUp(t *testing.T, name string) (string, string, string) {
	t.Helper()
	agent := f.newAgent(t, name)
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "退货地址是哪里")
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("退货地址见订单详情页", agentcontract.TerminalDecision{}, nil, nil))
	f.age(t, run.ScopeID, f.settings.AIFollowUpMinutes)
	f.process(t, run.ScopeID)
	return first.Conversation.ID, run.ScopeID, agent.IdentityID
}

// testFollowUpResolveIsRequest 验证超时跟进轮中 AI 调用结束服务时只发送结束语并记为确认请求，不按 AI 解决关闭，之后仍未回复则按客户失联关闭。
func testFollowUpResolveIsRequest(t *testing.T, f resolutionFixture) {
	conversationID, sessionID, agentID := f.answerAndFollowUp(t, "跟进结束语客服")
	followUp := f.executeQueuedRun(t, conversationID, resolutionRuntime("感谢您的咨询，祝您愉快", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	session := loadSession(t, f.db, sessionID)
	require.Equal(t, string(domain.AgentRunStatusSucceeded), followUp.Status)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), session.Status)
	require.Nil(t, session.CloseReason)
	require.NotNil(t, session.ResolutionRequestedAt)
	require.Equal(t, *followUp.ResponseMessageID, session.LastMessageID)
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
	run := f.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("请问问题解决了吗？", agentcontract.TerminalDecision{
		Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution,
	}, nil, nil))
	coordinator := testServiceSessionReturner(f.db)
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.identity, first.Conversation.ID)
	require.NoError(t, err)
	f.age(t, run.ScopeID, f.settings.AICloseMinutes+f.settings.AIFollowUpMinutes)
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(f.tasks), testEnqueuer).Execute(ctx, f.identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: first.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	})
	require.NoError(t, err)
	session := f.process(t, run.ScopeID)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), session.Status)
	require.NotNil(t, session.AssigneeIdentityID)
	require.Equal(t, agent.IdentityID, *session.AssigneeIdentityID)
	queued, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.scope_id = ? AND agr.status = ?", run.ScopeID, domain.AgentRunStatusQueued).Exists(ctx)
	require.NoError(t, err)
	require.False(t, queued, "follow-up right after transfer back")
	// 从转回时起超过跟进时长后先跟进，不直接关单。
	_, err = f.db.NewUpdate().Table("service_sessions").
		Set("assignee_assigned_at = now() - make_interval(mins => ?)", f.settings.AIFollowUpMinutes).
		Where("id = ?", run.ScopeID).Exec(ctx)
	require.NoError(t, err)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), f.process(t, run.ScopeID).Status, "session after follow-up timeout")
	require.Equal(t, run.ScopeID, f.queuedRun(t, first.Conversation.ID).ScopeID, "follow-up run")
}
