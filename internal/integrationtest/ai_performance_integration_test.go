//go:build server

package integrationtest

import (
	"context"
	"slices"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/actions/reportpage"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestAIPerformanceReport 验证 AI 表现报表只统计 AI 员工接待过的周期，按小结的是否解决统计解决情况，只把 AI 员工关闭且无真人参与的已解决周期计入独立解决，排除无实质诉求的周期，按已关闭周期统计转人工原因，统计待处理的待补知识，并按 AI 员工与负责人筛选和列出服务记录。
func TestAIPerformanceReport(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	agent := f.newAgent(t, "表现报表客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	// closeSession 把周期按指定结束方式关闭，并写入访客评价与小结的是否解决。
	closeSession := func(sessionID string, reason domain.ServiceSessionCloseReason, rating, resolved *bool) {
		_, err := db.NewUpdate().Table("service_sessions").
			Set("status = ?, close_reason = ?, closed_at = now(), rating_resolved = ?, resolved = ?", domain.ServiceSessionStatusClosed, reason, rating, resolved).
			Where("id = ?", sessionID).Exec(ctx)
		require.NoError(t, err)
	}

	// currentSession 读取客户会话当前的客服周期编号。
	currentSession := func(conversationID string) string {
		var sessionID string
		require.NoError(t, db.NewSelect().Table("service_conversations").Column("current_service_session_id").
			Where("conversation_id = ?", conversationID).Scan(ctx, &sessionID))
		return sessionID
	}

	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "问题解决了，谢谢")
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("不客气", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	assertAgentClosed(t, db, loadSession(t, db, resolveRun.ScopeID), agent.IdentityID, domain.ServiceSessionCloseAIResolved)

	handoffInput := visitorInput(channelID, "")
	handedOff := f.receive(t, &handoffInput, "海外仓发货要几天")
	handoffRun := f.executeQueuedRun(t, handedOff.Conversation.ID, handoffRuntime("资料里没有海外仓时效", nil))
	unresolved := false
	closeSession(handoffRun.ScopeID, domain.ServiceSessionCloseAIResolved, &unresolved, &unresolved)
	handoffSession := loadSession(t, db, handoffRun.ScopeID)
	require.NoError(t, realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		return knowledgegap.RecordClosed(ctx, tx, tasks, &handoffSession)
	}))

	// 无实质诉求的周期不计入报表。
	greetingInput := visitorInput(channelID, "")
	greeting := f.receive(t, &greetingInput, "你好")
	greetingSessionID := currentSession(greeting.Conversation.ID)
	closeSession(greetingSessionID, domain.ServiceSessionCloseCustomerUnresponsive, nil, nil)
	_, err := db.NewUpdate().Table("service_sessions").Set("summary_status = ?", domain.ServiceSessionSummaryNoRequest).
		Where("id = ?", greetingSessionID).Exec(ctx)
	require.NoError(t, err)

	scope := aiperformanceaction.Input{Days: 7, ChannelID: channelID}
	overview, err := aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, scope)
	require.NoError(t, err)
	summary := overview.Summary
	type overviewCounts struct {
		Closed, Resolved, Unresolved, AIOnly, AIResolved, AIUnresolved, HandedOff int
		CloseAIResolved, CustomerUnresponsive, Manual, Rated, RatedResolved       int
		KnowledgeGapTotal                                                         int
	}
	require.Equal(t, overviewCounts{
		Closed: 2, Resolved: 1, Unresolved: 1, AIOnly: 1, AIResolved: 1, AIUnresolved: 0, HandedOff: 1,
		CloseAIResolved: 2, CustomerUnresponsive: 0, Manual: 0, Rated: 1, RatedResolved: 0,
		KnowledgeGapTotal: 1,
	}, overviewCounts{
		Closed: summary.Closed, Resolved: summary.Resolved, Unresolved: summary.Unresolved, AIOnly: summary.AIOnly, AIResolved: summary.AIResolved, AIUnresolved: summary.AIUnresolved, HandedOff: summary.HandedOff,
		CloseAIResolved: summary.CloseAIResolved, CustomerUnresponsive: summary.CustomerUnresponsive, Manual: summary.Manual, Rated: summary.Rated, RatedResolved: summary.RatedResolved,
		KnowledgeGapTotal: overview.KnowledgeGapTotal,
	}, "overview = %+v", overview)
	require.Equal(t, []aiperformanceaction.ReasonCount{{Reason: string(domain.AgentHandoffReasonKnowledgeGap), Count: 1}}, overview.HandoffReasons)
	breakdowns := aiperformanceaction.NewBreakdownQuery(db)
	channels, err := breakdowns.Execute(ctx, identity, aiperformanceaction.BreakdownInput{Input: scope, Dimension: domain.ServiceReportDimensionChannel})
	require.NoError(t, err)
	require.Equal(t, 1, channels.Total)
	require.Len(t, channels.Rows, 1)
	require.Equal(t, channelID, *channels.Rows[0].ID)
	require.Equal(t, 2, channels.Rows[0].Closed)
	require.Equal(t, 1, channels.Rows[0].Resolved)
	require.Equal(t, 1, channels.Rows[0].AIResolved)
	categories, err := breakdowns.Execute(ctx, identity, aiperformanceaction.BreakdownInput{Input: scope, Dimension: domain.ServiceReportDimensionCategory})
	require.NoError(t, err)
	require.Equal(t, 1, categories.Total)
	require.Len(t, categories.Rows, 1)
	require.Nil(t, categories.Rows[0].ID)
	require.Equal(t, 2, categories.Rows[0].Closed)
	// 按 AI 员工筛选只统计其接待的周期及其中登记的待补知识；负责人范围只含本人负责的 AI 员工。
	_, err = db.NewUpdate().Table("agents").Set("responsible_user_id = ?", identity.User.ID).Where("id = ?", agent.ID).Exec(ctx)
	require.NoError(t, err)
	for _, agents := range []reportpage.AgentScope{{AgentID: agent.ID}, {AgentID: agent.ID, ResponsibleUserID: identity.User.ID}} {
		overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7, Agents: agents})
		require.NoError(t, err)
		require.Equal(t, 2, overview.Summary.Closed, "agent overview %+v", agents)
		require.Equal(t, 1, overview.Summary.AIResolved, "agent overview %+v", agents)
		require.Equal(t, 1, overview.KnowledgeGapTotal, "agent overview %+v", agents)
	}
	overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7, Agents: reportpage.AgentScope{AgentID: agent.ID, ResponsibleUserID: uuid.NewV7().String()}})
	require.NoError(t, err)
	require.Zero(t, overview.Summary.Closed, "other responsible overview")
	require.Zero(t, overview.KnowledgeGapTotal, "other responsible overview")
	gaps, err := knowledgegap.NewListQuery(db).Execute(ctx, identity, knowledgegap.ListInput{
		Scope: knowledgegap.Scope{Agents: reportpage.AgentScope{ResponsibleUserID: identity.User.ID}}, Status: domain.KnowledgeGapStatusPending,
	})
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(gaps.Gaps, func(gap knowledgegap.Summary) bool { return gap.ConversationID == handedOff.Conversation.ID }), "responsible gaps = %+v", gaps)
	// 服务记录按开启时间倒序列出该 AI 员工接待的全部周期，包括无实质诉求的周期。
	records, err := aiperformanceaction.NewServiceSessionListQuery(db).Execute(ctx, identity, aiperformanceaction.ServiceSessionListInput{AgentID: agent.ID})
	require.NoError(t, err)
	require.Equal(t, 3, records.Total)
	require.Len(t, records.Sessions, 3)
	latest := records.Sessions[0]
	require.Equal(t, greeting.Conversation.ID, latest.ConversationID)
	require.Equal(t, "你好", latest.Preview)
	require.Equal(t, string(domain.ServiceSourceChannel), latest.Source)
	require.NotNil(t, latest.ChannelName)
	require.Equal(t, string(domain.ServiceSessionStatusClosed), latest.Status)
	require.NotNil(t, latest.ClosedAt)
	// 开启时由真人负责的周期首次转给 AI 员工后记为其接待，计入其服务记录。
	transferChannelID := f.newChannel(t, identity.WorkspaceIdentity.ID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	transferInput := visitorInput(transferChannelID, "")
	transferred := f.receive(t, &transferInput, "想改一下发货地址")
	transferredSessionID := currentSession(transferred.Conversation.ID)
	require.Nil(t, loadSession(t, db, transferredSessionID).AgentIdentityID, "开启时由真人负责的周期不应记接待 AI 员工")
	_, err = servicesessionaction.NewTransferServiceSessionAction(db, testServiceSessionReturner(db), agentrunaction.NewScheduler(tasks), tasks).Execute(ctx, identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: transferred.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	})
	require.NoError(t, err)
	transferredSession := loadSession(t, db, transferredSessionID)
	require.NotNil(t, transferredSession.AgentIdentityID, "转给 AI 员工后的周期")
	require.Equal(t, agent.IdentityID, *transferredSession.AgentIdentityID, "转给 AI 员工后的周期")
	records, err = aiperformanceaction.NewServiceSessionListQuery(db).Execute(ctx, identity, aiperformanceaction.ServiceSessionListInput{AgentID: agent.ID})
	require.NoError(t, err)
	require.Equal(t, 4, records.Total, "service records after transfer")

	// 停用 AI 员工把其负责的周期退回队列，记为 AI 员工不可用的转人工。
	retired := f.newAgent(t, "表现报表停用客服")
	retiredChannelID := f.newChannel(t, retired.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	returnedInput := visitorInput(retiredChannelID, "")
	returned := f.receive(t, &returnedInput, "订单什么时候到")
	returnedSessionID := currentSession(returned.Conversation.ID)
	_, err = agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, retired.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	closeSession(returnedSessionID, domain.ServiceSessionCloseManual, nil, nil)
	overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7, ChannelID: retiredChannelID})
	require.NoError(t, err)
	type returnedCounts struct{ Closed, HandedOff, Manual, Resolved, Unresolved, KnowledgeGapTotal int }
	require.Equal(t, returnedCounts{Closed: 1, HandedOff: 1, Manual: 1, Resolved: 0, Unresolved: 0, KnowledgeGapTotal: 0}, returnedCounts{
		Closed: overview.Summary.Closed, HandedOff: overview.Summary.HandedOff, Manual: overview.Summary.Manual,
		Resolved: overview.Summary.Resolved, Unresolved: overview.Summary.Unresolved, KnowledgeGapTotal: overview.KnowledgeGapTotal,
	}, "returned overview = %+v", overview)
	require.Equal(t, []aiperformanceaction.ReasonCount{{Reason: string(domain.AgentHandoffReasonAgentUnavailable), Count: 1}}, overview.HandoffReasons)

	// AI 接待后由真人接管并对客回复的已解决周期计入已解决，不计入独立解决。
	claim := servicesessionaction.NewClaimServiceSessionAction(db, testServiceSessionReturner(db), tasks)
	humanChannelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	repliedInput := visitorInput(humanChannelID, "")
	replied := f.receive(t, &repliedInput, "怎么修改收货地址")
	_, err = claim.Execute(ctx, identity, replied.Conversation.ID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewSendServiceTextMessageAction(db, tasks).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: replied.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "我来帮您修改",
	})
	require.NoError(t, err)
	resolvedByHuman := true
	closeSession(currentSession(replied.Conversation.ID), domain.ServiceSessionCloseAIResolved, nil, &resolvedByHuman)
	overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7, ChannelID: humanChannelID})
	require.NoError(t, err)
	type humanRepliedCounts struct{ Closed, CloseAIResolved, Resolved, AIOnly, AIResolved, HandedOff int }
	require.Equal(t, humanRepliedCounts{Closed: 1, CloseAIResolved: 1, Resolved: 1, AIOnly: 0, AIResolved: 0, HandedOff: 0}, humanRepliedCounts{
		Closed: overview.Summary.Closed, CloseAIResolved: overview.Summary.CloseAIResolved, Resolved: overview.Summary.Resolved,
		AIOnly: overview.Summary.AIOnly, AIResolved: overview.Summary.AIResolved, HandedOff: overview.Summary.HandedOff,
	}, "human replied overview = %+v", overview.Summary)

	// AI 接待后由真人接管、未对客回复直接关闭的已解决周期计入已解决，不计入独立处理。
	closedByHumanChannelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	manualInput := visitorInput(closedByHumanChannelID, "")
	manual := f.receive(t, &manualInput, "发票怎么开")
	manualSessionID := currentSession(manual.Conversation.ID)
	_, err = claim.Execute(ctx, identity, manual.Conversation.ID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(db, testServiceSessionReturner(db), tasks).Execute(ctx, identity, manual.Conversation.ID)
	require.NoError(t, err)
	_, err = db.NewUpdate().Table("service_sessions").Set("resolved = true").Where("id = ?", manualSessionID).Exec(ctx)
	require.NoError(t, err)
	overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7, ChannelID: closedByHumanChannelID})
	require.NoError(t, err)
	type closedByHumanCounts struct{ Closed, Manual, Resolved, AIOnly, AIResolved int }
	require.Equal(t, closedByHumanCounts{Closed: 1, Manual: 1, Resolved: 1, AIOnly: 0, AIResolved: 0}, closedByHumanCounts{
		Closed: overview.Summary.Closed, Manual: overview.Summary.Manual, Resolved: overview.Summary.Resolved,
		AIOnly: overview.Summary.AIOnly, AIResolved: overview.Summary.AIResolved,
	}, "closed by human overview = %+v", overview.Summary)

	// 从未由 AI 员工接待的周期不计入 AI 表现。
	pureHumanChannelID := f.newChannel(t, identity.WorkspaceIdentity.ID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	pureHumanInput := visitorInput(pureHumanChannelID, "")
	pureHuman := f.receive(t, &pureHumanInput, "退款多久到账")
	closeSession(currentSession(pureHuman.Conversation.ID), domain.ServiceSessionCloseManual, nil, nil)
	overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7, ChannelID: pureHumanChannelID})
	require.NoError(t, err)
	require.Zero(t, overview.Summary.Closed, "pure human overview")

	// 全部渠道不按渠道过滤，包含上述四个渠道。
	overview, err = aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7})
	require.NoError(t, err)
	require.GreaterOrEqual(t, overview.Summary.Closed, 5, "all channels overview")
	channels, err = breakdowns.Execute(ctx, identity, aiperformanceaction.BreakdownInput{Input: aiperformanceaction.Input{Days: 7}, Dimension: domain.ServiceReportDimensionChannel})
	require.NoError(t, err)
	require.GreaterOrEqual(t, channels.Total, 4, "all channels breakdown")
}
