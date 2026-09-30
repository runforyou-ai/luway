//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	aiperformanceaction "github.com/runforyou-ai/cervi/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	deliveryaction "github.com/runforyou-ai/cervi/internal/actions/customerdelivery"
	customerserviceaction "github.com/runforyou-ai/cervi/internal/actions/customerservice"
	"github.com/runforyou-ai/cervi/internal/actions/serviceissue"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	teamperformanceaction "github.com/runforyou-ai/cervi/internal/actions/teamperformance"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"uuid"
)

// TestTeamPerformanceReport 验证服务周期记录需要真人、真人首次负责与首次回复的时间以及按工作时间计的首响，团队表现按这些时间统计真人承接、首响与处理时长，AI 处理时长停在首次需要真人，按客服、渠道与队列拆分和筛选，并列出真人质检标出的问题会话。
func TestTeamPerformanceReport(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Registry().RegisterJSON(deliveryaction.SendActionName, func(context.Context, deliveryaction.Input) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Organization.ID)
	agent := f.newAgent(t, "团队表现客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	coordinator := testServiceSessionReturner(db)

	// AI 独立解决的周期不需要真人。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "问题解决了，谢谢")
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("不客气", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	if session := loadSession(t, db, resolveRun.ScopeID); session.HumanRequestedAt != nil || session.HumanAssignedAt != nil || session.HumanFirstResponseAt != nil {
		t.Fatalf("ai resolved session = %+v", session)
	}

	// AI 转人工进入公共队列时记录需要真人；成员回复即领取并记录真人首次负责与首次回复。
	handoffInput := visitorInput(channelID, "")
	handedOff := f.receive(t, &handoffInput, "海外仓发货要几天")
	handoffRun := f.executeQueuedRun(t, handedOff.Conversation.ID, handoffRuntime("资料里没有海外仓时效", nil))
	queued := loadSession(t, db, handoffRun.ScopeID)
	if queued.HumanRequestedAt == nil || queued.HumanAssignedAt != nil || queued.HumanFirstResponseAt != nil {
		t.Fatalf("queued session = %+v", queued)
	}
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, nil).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: handedOff.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "海外仓一般 5 天送达",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, newTestTasks(db)).Execute(ctx, identity, handedOff.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	// 关单推迟 1 小时，人工处理时长随之变长，AI 处理时长仍停在转人工。
	if _, err := db.NewUpdate().Table("service_sessions").Set("closed_at = closed_at + interval '1 hour'").Where("id = ?", handoffRun.ScopeID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	handled := loadSession(t, db, handoffRun.ScopeID)
	if !handled.HumanRequestedAt.Equal(*queued.HumanRequestedAt) || handled.HumanAssignedAt == nil || handled.HumanFirstResponseAt == nil ||
		handled.HumanAssignedAt.Before(*handled.HumanRequestedAt) || !handled.HumanFirstResponseAt.Equal(*handled.HumanAssignedAt) ||
		handled.HumanFirstResponseSec == nil || *handled.HumanFirstResponseSec != int(handled.HumanFirstResponseAt.Sub(*handled.HumanRequestedAt)/time.Second) {
		t.Fatalf("handled session = %+v", handled)
	}
	// 真人质检标出答错。
	if _, err := db.NewInsert().Model(&servermodels.ServiceSessionReview{
		OrganizationID: identity.Organization.ID, ServiceSessionID: handled.ID, ClosedAt: *handled.ClosedAt,
		HumanIncorrect: new(true), HumanPoorAttitude: new(false),
	}).Column("organization_id", "service_session_id", "closed_at", "human_incorrect", "human_poor_attitude").Exec(ctx); err != nil {
		t.Fatal(err)
	}

	scope := teamperformanceaction.Input{Days: 7, ChannelID: channelID}
	summary, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, scope)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Closed != 2 || summary.HumanRequested != 1 || summary.HumanResponded != 1 || summary.FirstResponseSampled != 1 ||
		summary.FirstResponseMedian == nil || summary.FirstResponseP90 == nil || summary.AIHandled != 2 || summary.AIHandlingMedian == nil || *summary.AIHandlingMedian > 60 ||
		summary.HumanHandled != 1 || summary.HumanHandlingMedian == nil || *summary.HumanHandlingMedian < 3600 ||
		summary.HumanIncorrect != 1 || summary.HumanIncorrectReviewed != 1 || summary.HumanPoorAttitude != 0 || summary.HumanPoorAttitudeReviewed != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	members, err := teamperformanceaction.NewMemberListQuery(db).Execute(ctx, identity, teamperformanceaction.ListInput{Input: scope})
	if err != nil || members.Total != 1 || len(members.Rows) != 1 || members.Rows[0].IdentityID != identity.OrganizationIdentity.ID ||
		members.Rows[0].Closed != 1 || members.Rows[0].Issues != 1 {
		t.Fatalf("members = %+v, error = %v", members, err)
	}
	channels, err := teamperformanceaction.NewBreakdownQuery(db).Execute(ctx, identity, teamperformanceaction.BreakdownInput{
		ListInput: teamperformanceaction.ListInput{Input: scope}, Dimension: domain.ServiceReportDimensionChannel,
	})
	if err != nil || channels.Total != 1 || channels.Rows[0].Closed != 2 || channels.Rows[0].HumanRequested != 1 || channels.Rows[0].FirstResponseMedian == nil {
		t.Fatalf("channels = %+v, error = %v", channels, err)
	}
	// 公共队列筛选统计公共队列的周期，指定其他团队时不统计。
	public, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7, ChannelID: channelID, PublicQueue: true})
	if err != nil || public.Closed != 2 {
		t.Fatalf("public queue summary = %+v, error = %v", public, err)
	}
	other, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7, TeamID: uuid.NewV7().String()})
	if err != nil || other.Closed != 0 || other.FirstResponseMedian != nil {
		t.Fatalf("other team summary = %+v, error = %v", other, err)
	}

	// 真人问题会话只出现在团队表现中，详情带出真人质检结论。
	issues, err := teamperformanceaction.NewIssueListQuery(db).Execute(ctx, identity, teamperformanceaction.IssueListInput{
		ListInput: teamperformanceaction.ListInput{Input: scope}, Issue: domain.ServiceIssueTypeAll,
	})
	if err != nil || issues.Total != 1 || issues.Issues[0].ServiceSessionID != handled.ID || issues.Issues[0].HumanIncorrect == nil || !*issues.Issues[0].HumanIncorrect {
		t.Fatalf("team issues = %+v, error = %v", issues, err)
	}
	if _, err := teamperformanceaction.NewIssueListQuery(db).Execute(ctx, identity, teamperformanceaction.IssueListInput{
		ListInput: teamperformanceaction.ListInput{Input: scope}, Issue: domain.ServiceIssueTypeAIIncorrect,
	}); err != teamperformanceaction.ErrIssueInvalid {
		t.Fatalf("ai issue type in team report error = %v", err)
	}
	aiIssues, err := aiperformanceaction.NewIssueListQuery(db).Execute(ctx, identity, aiperformanceaction.IssueListInput{
		Input: aiperformanceaction.Input{Days: 7, ChannelID: channelID}, Issue: domain.ServiceIssueTypeAll,
	})
	if err != nil || aiIssues.Total != 0 {
		t.Fatalf("ai issues = %+v, error = %v", aiIssues, err)
	}
	detail, err := serviceissue.NewQuery(db).Execute(ctx, identity, handled.ID)
	if err != nil || detail.HumanIncorrect == nil || !*detail.HumanIncorrect || len(detail.Messages) == 0 {
		t.Fatalf("issue detail = %+v, error = %v", detail, err)
	}

	// 真人首接待的周期开启即需要真人并由真人负责；其间没有经过工作时间的回复不记首响用时。
	closedAllWeek := domain.BusinessHours{Enabled: true, TimeZone: "UTC"}
	if _, err := customerserviceaction.NewUpdateBusinessHoursAction(db).Execute(ctx, identity, closedAllWeek); err != nil {
		t.Fatal(err)
	}
	memberChannelID := f.newChannel(t, identity.OrganizationIdentity.ID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	memberInput := visitorInput(memberChannelID, "")
	direct := f.receive(t, &memberInput, "发票怎么开")
	var directSessionID string
	if err := db.NewSelect().Table("service_conversations").Column("current_service_session_id").
		Where("conversation_id = ?", direct.Conversation.ID).Scan(ctx, &directSessionID); err != nil {
		t.Fatal(err)
	}
	opened := loadSession(t, db, directSessionID)
	if opened.HumanRequestedAt == nil || opened.HumanAssignedAt == nil || !opened.HumanAssignedAt.Equal(*opened.HumanRequestedAt) {
		t.Fatalf("member first reception session = %+v", opened)
	}
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, nil).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: direct.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "在设置里申请",
	}); err != nil {
		t.Fatal(err)
	}
	if responded := loadSession(t, db, directSessionID); responded.HumanFirstResponseAt == nil || responded.HumanFirstResponseSec != nil {
		t.Fatalf("off hours response session = %+v", responded)
	}
}

// TestServiceSessionHumanReplied 验证服务发起人的消息不算作真人客服回复，处理人对发起人的共享回复才算。
func TestServiceSessionHumanReplied(t *testing.T) {
	t.Parallel()
	f := newDirectServiceFixture(t)
	ctx := context.Background()
	conversationID := f.startChat(t, f.agent.IdentityID, "电脑蓝屏了")
	sessionID := *f.service(t, conversationID).CurrentServiceSessionID
	// humanReplied 读取周期当前是否有真人客服回复。
	humanReplied := func() bool {
		t.Helper()
		var replied bool
		if err := f.db.NewSelect().TableExpr("service_sessions AS ss").ColumnExpr("? AS replied", messagequery.HumanReplied("ss")).
			Where("ss.id = ?", sessionID).Scan(ctx, &replied); err != nil {
			t.Fatal(err)
		}
		return replied
	}
	if humanReplied() {
		t.Fatal("发起人的消息被算作真人客服回复")
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, testServiceSessionReturner(f.db), f.tasks).Execute(ctx, f.member, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.reply(conversationID, "请重启试试", domain.MessageVisibilityShared); err != nil {
		t.Fatal(err)
	}
	if !humanReplied() {
		t.Fatal("处理人的共享回复未算作真人客服回复")
	}
}
