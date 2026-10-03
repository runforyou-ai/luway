//go:build server

package integrationtest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/uptrace/bun"
	"uuid"
)

// TestPlatformUsage 验证平台业务使用按工作区汇总客服周期与平台模型调用，客服指标与该工作区的 AI 表现、团队表现报表一致，平台合计在全部周期上计算，没有周期的工作区客服指标为零，并按所选方式排序。
func TestPlatformUsage(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	ctx := context.Background()
	identity, providerID, modelID := newAIWorkspaceIn(t, db)
	idle := installWorkspace(t, db, workspaceSpec{Name: "空闲工作区", DisplayName: "管理员", Email: uniqueEmail("idle"), Password: "password123"}).Identity
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Registry().RegisterJSON(deliveryaction.SendActionName, func(context.Context, deliveryaction.Input) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Organization.ID)
	agent := f.newAgent(t, "业务使用客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})

	// AI 独立解决一个周期。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "问题解决了，谢谢")
	f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("不客气", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))

	// AI 因资料不足转人工，真人回复后关闭，并登记待补知识。
	handoffInput := visitorInput(channelID, "")
	handedOff := f.receive(t, &handoffInput, "海外仓发货要几天")
	handoffRun := f.executeQueuedRun(t, handedOff.Conversation.ID, handoffRuntime("资料里没有海外仓时效", nil))
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, nil).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: handedOff.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "海外仓一般 5 天送达",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(db, testServiceSessionReturner(db), newTestTasks(db)).Execute(ctx, identity, handedOff.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	handoffSession := loadSession(t, db, handoffRun.ScopeID)
	if err := realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		return knowledgegap.RecordClosed(ctx, tx, tasks, &handoffSession)
	}); err != nil {
		t.Fatal(err)
	}

	// 统计期间的平台模型调用计入，工作区模型调用与期间之前的调用不计入；失败率的分母不含进行中与已取消的调用。
	for _, call := range []struct {
		organizationID string
		scope          domain.AIModelScope
		status         domain.AIModelCallStatus
		daysAgo        int
		input, cached  int64
		output         int64
	}{
		{identity.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusSucceeded, 1, 100, 20, 30},
		{identity.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusFailed, 1, 0, 0, 0},
		{identity.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusTimedOut, 1, 0, 0, 0},
		{identity.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusCanceled, 1, 0, 0, 0},
		{identity.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusRunning, 0, 0, 0, 0},
		{identity.Organization.ID, domain.AIModelScopeWorkspace, domain.AIModelCallStatusSucceeded, 1, 5000, 0, 5000},
		{identity.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusSucceeded, 10, 5000, 0, 5000},
		{idle.Organization.ID, domain.AIModelScopePlatform, domain.AIModelCallStatusSucceeded, 2, 1000, 0, 0},
	} {
		if _, err := db.NewRaw(`INSERT INTO ai_model_calls
			(created_at, organization_id, model_id, model_name, model_usage, actor_type, source_type, status, input_tokens, cached_input_tokens, output_tokens, model_scope)
			VALUES (now() - make_interval(days => ?), ?, ?, '平台模型', ?, ?, ?, ?, ?, ?, ?, ?)`,
			call.daysAgo, call.organizationID, uuid.NewV7().String(), domain.AIModelUsageAgent, domain.AIModelCallActorSystem, domain.AIModelCallSourceAgentRun,
			call.status, call.input, call.cached, call.output, call.scope).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	idleMetrics := platformaction.UsageMetrics{ModelCalls: 1, ModelCallsConcluded: 1, InputTokens: 1000}

	ai, err := aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	team, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	expected := platformaction.UsageMetrics{
		ServiceSessions: team.Closed, Conversations: 2, AIClosed: ai.Summary.Closed, AIResolved: ai.Summary.AIResolved, HandedOff: ai.Summary.HandedOff,
		FirstResponseMedian: team.FirstResponseMedian, FirstResponseP90: team.FirstResponseP90, KnowledgeGaps: ai.KnowledgeGapTotal,
		ModelCalls: 5, ModelCallsConcluded: 3, ModelCallsFailed: 2, InputTokens: 100, CachedInputTokens: 20, OutputTokens: 30,
	}
	if expected.ServiceSessions != 2 || expected.AIClosed != 2 || expected.AIResolved != 1 || expected.HandedOff != 1 ||
		expected.FirstResponseMedian == nil || expected.KnowledgeGaps != 1 {
		t.Fatalf("workspace reports = %+v", expected)
	}

	query := platformaction.NewUsageQuery(db)
	expectedTotal := expected
	expectedTotal.ModelCalls, expectedTotal.ModelCallsConcluded, expectedTotal.InputTokens = 6, 4, 1100
	total, err := query.Summary(ctx, 7)
	if err != nil || !reflect.DeepEqual(total, expectedTotal) {
		t.Fatalf("usage total = %+v, err = %v", total, err)
	}
	usage, err := query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Page.Total != 2 || len(usage.Workspaces) != 2 ||
		usage.Workspaces[0].ID != identity.Organization.ID || !reflect.DeepEqual(usage.Workspaces[0].UsageMetrics, expected) ||
		usage.Workspaces[1].ID != idle.Organization.ID || !reflect.DeepEqual(usage.Workspaces[1].UsageMetrics, idleMetrics) {
		t.Fatalf("workspace usage = %+v", usage)
	}
	// 按首响排序时没有样本的工作区排在最后，第二页从第二个工作区开始。
	sorted, err := query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 7, Sort: platformaction.UsageSortFirstResponse, Page: 2, PageSize: 1})
	if err != nil || sorted.Page.Total != 2 || len(sorted.Workspaces) != 1 || sorted.Workspaces[0].ID != idle.Organization.ID {
		t.Fatalf("sorted usage = %+v, err = %v", sorted, err)
	}
	// 按模型 Token 排序时输入与输出合计更多的空闲工作区在前。
	byTokens, err := query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 7, Sort: platformaction.UsageSortModelTokens})
	if err != nil || len(byTokens.Workspaces) != 2 || byTokens.Workspaces[0].ID != idle.Organization.ID {
		t.Fatalf("usage sorted by model tokens = %+v, err = %v", byTokens, err)
	}
	// 有周期的工作区删除后只剩空闲工作区，客服指标为零，首响为空。
	if _, err := db.NewUpdate().Table("organizations").Set("lifecycle_status = ?", domain.OrganizationLifecycleDeleted).Where("id = ?", identity.Organization.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if empty, err := query.Summary(ctx, 7); err != nil || !reflect.DeepEqual(empty, idleMetrics) {
		t.Fatalf("empty usage total = %+v, err = %v", empty, err)
	}

	_, err = query.ListWorkspaces(ctx, platformaction.UsageListInput{Days: 0, Sort: "members"})
	if fieldError, ok := errors.AsType[*common.FieldError](err); !ok ||
		fieldError.Fields["days"] != platformaction.ValidationUsageDaysInvalid || fieldError.Fields["sort"] != platformaction.ValidationUsageSortInvalid {
		t.Fatalf("invalid usage input err = %v", err)
	}
}
