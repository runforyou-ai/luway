//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestPlatformUsage 验证平台业务使用按工作区汇总客服周期与模型调用，客服指标与该工作区的 AI 表现、团队表现报表一致，平台合计在全部周期上计算，没有周期的工作区客服指标为零，并按所选方式排序。
func TestPlatformUsage(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	ctx := context.Background()
	identity, providerID, modelID := newAIWorkspaceIn(t, db)
	idle := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "空闲工作区", DisplayName: "管理员", Email: servertest.UniqueEmail("idle"), Password: "password123"}).Identity
	tasks := servertest.NewTasks()
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	agent := f.newAgent(t, "业务使用客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})

	// AI 独立解决一个周期。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "问题解决了，谢谢")
	f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("不客气", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))

	// AI 因资料不足转人工，真人回复后关闭，并登记待补知识。
	handoffInput := visitorInput(channelID, "")
	handedOff := f.receive(t, &handoffInput, "海外仓发货要几天")
	handoffRun := f.executeQueuedRun(t, handedOff.Conversation.ID, handoffRuntime("资料里没有海外仓时效", nil))
	_, err := servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: handedOff.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "海外仓一般 5 天送达",
	})
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(db, testServiceSessionReturner(db), testEnqueuer).Execute(ctx, identity, handedOff.Conversation.ID)
	require.NoError(t, err)
	handoffSession := loadSession(t, db, handoffRun.ScopeID)
	require.NoError(t, realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		return knowledgegap.RecordClosed(ctx, tx, tasks, &handoffSession)
	}))

	// 统计期间的全部模型调用计入，期间之前的调用不计入；失败率的分母不含进行中与已取消的调用。
	for _, call := range []struct {
		workspaceID   string
		scope         domain.AIModelScope
		status        domain.AIModelCallStatus
		daysAgo       int
		input, cached int64
		output        int64
	}{
		{identity.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusSucceeded, 1, 100, 20, 30},
		{identity.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusFailed, 1, 0, 0, 0},
		{identity.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusTimedOut, 1, 0, 0, 0},
		{identity.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusCanceled, 1, 0, 0, 0},
		{identity.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusRunning, 0, 0, 0, 0},
		{identity.Workspace.ID, domain.AIModelScopeWorkspace, domain.AIModelCallStatusSucceeded, 1, 50, 0, 50},
		{identity.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusSucceeded, 10, 5000, 0, 5000},
		{idle.Workspace.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusSucceeded, 2, 1000, 0, 0},
	} {
		_, err := db.NewRaw(`INSERT INTO ai_model_calls
			(created_at, workspace_id, model_id, model_name, model_usage, actor_type, source_type, status, input_tokens, cached_input_tokens, output_tokens, model_scope)
			VALUES (now() - make_interval(days => ?), ?, ?, '平台模型', ?, ?, ?, ?, ?, ?, ?, ?)`,
			call.daysAgo, call.workspaceID, uuid.NewV7().String(), domain.AIModelUsageAgent, domain.AIModelCallActorSystem, domain.AIModelCallSourceAgentRun,
			call.status, call.input, call.cached, call.output, call.scope).Exec(ctx)
		require.NoError(t, err)
	}
	idleMetrics := platformaction.UsageMetrics{ModelCalls: 1, ModelCallsConcluded: 1, InputTokens: 1000}

	ai, err := aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7})
	require.NoError(t, err)
	team, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7})
	require.NoError(t, err)
	expected := platformaction.UsageMetrics{
		ServiceSessions: team.Closed, Conversations: 2, AIClosed: ai.Summary.Closed, AIResolved: ai.Summary.AIResolved, HandedOff: ai.Summary.HandedOff,
		FirstResponseMedian: team.FirstResponseMedian, FirstResponseP90: team.FirstResponseP90, KnowledgeGaps: ai.KnowledgeGapTotal,
		ModelCalls: 6, ModelCallsConcluded: 4, ModelCallsFailed: 2, InputTokens: 150, CachedInputTokens: 20, OutputTokens: 80,
	}
	type reportCounts struct{ ServiceSessions, AIClosed, AIResolved, HandedOff, KnowledgeGaps int }
	require.Equal(t, reportCounts{ServiceSessions: 2, AIClosed: 2, AIResolved: 1, HandedOff: 1, KnowledgeGaps: 1},
		reportCounts{expected.ServiceSessions, expected.AIClosed, expected.AIResolved, expected.HandedOff, expected.KnowledgeGaps})
	require.NotNil(t, expected.FirstResponseMedian)

	query := platformaction.NewUsageQuery(db)
	expectedTotal := expected
	expectedTotal.ModelCalls, expectedTotal.ModelCallsConcluded, expectedTotal.InputTokens = 7, 5, 1150
	total, err := query.Summary(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, expectedTotal, total)
	usage, err := query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 7})
	require.NoError(t, err)
	require.EqualValues(t, 2, usage.Page.Total)
	require.Len(t, usage.Workspaces, 2)
	require.Equal(t, identity.Workspace.ID, usage.Workspaces[0].ID)
	require.Equal(t, expected, usage.Workspaces[0].UsageMetrics)
	require.Equal(t, idle.Workspace.ID, usage.Workspaces[1].ID)
	require.Equal(t, idleMetrics, usage.Workspaces[1].UsageMetrics)
	// 按首响排序时没有样本的工作区排在最后，第二页从第二个工作区开始。
	sorted, err := query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 7, Sort: platformaction.UsageSortFirstResponse, Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, sorted.Page.Total)
	require.Len(t, sorted.Workspaces, 1)
	require.Equal(t, idle.Workspace.ID, sorted.Workspaces[0].ID)
	// 按模型 Token 排序时输入与输出合计更多的空闲工作区在前。
	byTokens, err := query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 7, Sort: platformaction.UsageSortModelTokens})
	require.NoError(t, err)
	require.Len(t, byTokens.Workspaces, 2)
	require.Equal(t, idle.Workspace.ID, byTokens.Workspaces[0].ID)
	// 有周期的工作区删除后只剩空闲工作区，客服指标为零，首响为空。
	_, err = db.NewUpdate().Table("workspaces").Set("lifecycle_status = ?", domain.WorkspaceLifecycleDeleted).Where("id = ?", identity.Workspace.ID).Exec(ctx)
	require.NoError(t, err)
	empty, err := query.Summary(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, idleMetrics, empty)
}
