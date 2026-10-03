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
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/uptrace/bun"
	"uuid"
)

// TestDeploymentUsage 验证部署业务使用按工作区汇总客服周期，工作区指标与该工作区的 AI 表现、团队表现报表一致，部署合计在全部周期上计算，没有周期的工作区指标为零，并按所选方式排序。
func TestDeploymentUsage(t *testing.T) {
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

	ai, err := aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, aiperformanceaction.Input{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	team, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	expected := deploymentaction.UsageMetrics{
		ServiceSessions: team.Closed, Conversations: 2, AIClosed: ai.Summary.Closed, AIResolved: ai.Summary.AIResolved, HandedOff: ai.Summary.HandedOff,
		FirstResponseMedian: team.FirstResponseMedian, FirstResponseP90: team.FirstResponseP90, KnowledgeGaps: ai.KnowledgeGapTotal,
	}
	if expected.ServiceSessions != 2 || expected.AIClosed != 2 || expected.AIResolved != 1 || expected.HandedOff != 1 ||
		expected.FirstResponseMedian == nil || expected.KnowledgeGaps != 1 {
		t.Fatalf("workspace reports = %+v", expected)
	}

	query := deploymentaction.NewUsageQuery(db)
	total, err := query.Summary(ctx, 7)
	if err != nil || !reflect.DeepEqual(total, expected) {
		t.Fatalf("usage total = %+v, err = %v", total, err)
	}
	usage, err := query.ListWorkspaces(ctx, deploymentaction.UsageListInput{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if usage.Page.Total != 2 || len(usage.Workspaces) != 2 ||
		usage.Workspaces[0].ID != identity.Organization.ID || !reflect.DeepEqual(usage.Workspaces[0].UsageMetrics, expected) ||
		usage.Workspaces[1].ID != idle.Organization.ID || !reflect.DeepEqual(usage.Workspaces[1].UsageMetrics, deploymentaction.UsageMetrics{}) {
		t.Fatalf("workspace usage = %+v", usage)
	}
	// 按首响排序时没有样本的工作区排在最后，第二页从第二个工作区开始。
	sorted, err := query.ListWorkspaces(ctx, deploymentaction.UsageListInput{Days: 7, Sort: deploymentaction.UsageSortFirstResponse, Page: 2, PageSize: 1})
	if err != nil || sorted.Page.Total != 2 || len(sorted.Workspaces) != 1 || sorted.Workspaces[0].ID != idle.Organization.ID {
		t.Fatalf("sorted usage = %+v, err = %v", sorted, err)
	}
	// 有周期的工作区删除后只剩空闲工作区，合计为零，首响为空。
	if _, err := db.NewUpdate().Table("organizations").Set("lifecycle_status = ?", domain.OrganizationLifecycleDeleted).Where("id = ?", identity.Organization.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if empty, err := query.Summary(ctx, 7); err != nil || !reflect.DeepEqual(empty, deploymentaction.UsageMetrics{}) {
		t.Fatalf("empty usage total = %+v, err = %v", empty, err)
	}

	_, err = query.ListWorkspaces(ctx, deploymentaction.UsageListInput{Days: 0, Sort: "members"})
	if fieldError, ok := errors.AsType[*common.FieldError](err); !ok ||
		fieldError.Fields["days"] != deploymentaction.ValidationUsageDaysInvalid || fieldError.Fields["sort"] != deploymentaction.ValidationUsageSortInvalid {
		t.Fatalf("invalid usage input err = %v", err)
	}
}
