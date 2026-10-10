//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	"uuid"

	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/serviceissue"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamperformanceaction "github.com/runforyou-ai/luway/internal/actions/teamperformance"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestTeamPerformanceReport 验证服务周期记录需要真人、真人首次负责与首次回复的时间以及按工作时间计的首响，团队表现按这些时间统计真人承接、首响与处理时长，AI 处理时长停在首次需要真人，按客服、渠道与队列拆分和筛选，并列出真人质检标出的问题会话。
func TestTeamPerformanceReport(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	agent := f.newAgent(t, "团队表现客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	coordinator := testServiceSessionReturner(db)

	// AI 独立解决的周期不需要真人。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "问题解决了，谢谢")
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("不客气", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	session := loadSession(t, db, resolveRun.ScopeID)
	require.Nil(t, session.HumanRequestedAt, "ai resolved session")
	require.Nil(t, session.HumanAssignedAt, "ai resolved session")
	require.Nil(t, session.HumanFirstResponseAt, "ai resolved session")

	// AI 转人工进入公共队列时记录需要真人；成员回复即领取并记录真人首次负责与首次回复。
	handoffInput := visitorInput(channelID, "")
	handedOff := f.receive(t, &handoffInput, "海外仓发货要几天")
	handoffRun := f.executeQueuedRun(t, handedOff.Conversation.ID, handoffRuntime("资料里没有海外仓时效", nil))
	queued := loadSession(t, db, handoffRun.ScopeID)
	require.NotNil(t, queued.HumanRequestedAt, "queued session")
	require.Nil(t, queued.HumanAssignedAt, "queued session")
	require.Nil(t, queued.HumanFirstResponseAt, "queued session")
	_, err := servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: handedOff.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "海外仓一般 5 天送达",
	})
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(db, coordinator, testEnqueuer).Execute(ctx, identity, handedOff.Conversation.ID)
	require.NoError(t, err)
	// 关单推迟 1 小时，人工处理时长随之变长，AI 处理时长仍停在转人工。
	_, err = db.NewUpdate().Table("service_sessions").Set("closed_at = closed_at + interval '1 hour'").Where("id = ?", handoffRun.ScopeID).Exec(ctx)
	require.NoError(t, err)
	handled := loadSession(t, db, handoffRun.ScopeID)
	require.True(t, handled.HumanRequestedAt.Equal(*queued.HumanRequestedAt), "handled session = %+v", handled)
	require.NotNil(t, handled.HumanAssignedAt, "handled session")
	require.NotNil(t, handled.HumanFirstResponseAt, "handled session")
	require.False(t, handled.HumanAssignedAt.Before(*handled.HumanRequestedAt), "handled session = %+v", handled)
	require.True(t, handled.HumanFirstResponseAt.Equal(*handled.HumanAssignedAt), "handled session = %+v", handled)
	require.NotNil(t, handled.HumanFirstResponseSec, "handled session")
	require.Equal(t, int(handled.HumanFirstResponseAt.Sub(*handled.HumanRequestedAt)/time.Second), *handled.HumanFirstResponseSec, "handled session")
	// 真人质检标出答错。
	_, err = db.NewInsert().Model(&servermodels.ServiceSessionReview{
		WorkspaceID: identity.Workspace.ID, ServiceSessionID: handled.ID, ClosedAt: *handled.ClosedAt,
		HumanIncorrect: new(true), HumanPoorAttitude: new(false),
	}).Column("workspace_id", "service_session_id", "closed_at", "human_incorrect", "human_poor_attitude").Exec(ctx)
	require.NoError(t, err)

	scope := teamperformanceaction.Input{Days: 7, ChannelID: channelID}
	summary, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, scope)
	require.NoError(t, err)
	require.Equal(t, struct{ Closed, HumanRequested, HumanResponded, FirstResponseSampled, AIHandled, HumanHandled, HumanIncorrect, HumanIncorrectReviewed, HumanPoorAttitude, HumanPoorAttitudeReviewed int }{2, 1, 1, 1, 2, 1, 1, 1, 0, 1},
		struct{ Closed, HumanRequested, HumanResponded, FirstResponseSampled, AIHandled, HumanHandled, HumanIncorrect, HumanIncorrectReviewed, HumanPoorAttitude, HumanPoorAttitudeReviewed int }{
			summary.Closed, summary.HumanRequested, summary.HumanResponded, summary.FirstResponseSampled, summary.AIHandled, summary.HumanHandled,
			summary.HumanIncorrect, summary.HumanIncorrectReviewed, summary.HumanPoorAttitude, summary.HumanPoorAttitudeReviewed,
		}, "summary = %+v", summary)
	require.NotNil(t, summary.FirstResponseMedian, "summary")
	require.NotNil(t, summary.FirstResponseP90, "summary")
	require.NotNil(t, summary.AIHandlingMedian, "summary")
	require.LessOrEqual(t, *summary.AIHandlingMedian, 60, "summary")
	require.NotNil(t, summary.HumanHandlingMedian, "summary")
	require.GreaterOrEqual(t, *summary.HumanHandlingMedian, 3600, "summary")
	members, err := teamperformanceaction.NewMemberListQuery(db).Execute(ctx, identity, teamperformanceaction.ListInput{Input: scope})
	require.NoError(t, err)
	require.Equal(t, 1, members.Total, "members")
	require.Len(t, members.Rows, 1, "members")
	require.Equal(t, identity.WorkspaceIdentity.ID, members.Rows[0].IdentityID, "members")
	require.Equal(t, 1, members.Rows[0].Closed, "members")
	require.Equal(t, 1, members.Rows[0].Issues, "members")
	channels, err := teamperformanceaction.NewBreakdownQuery(db).Execute(ctx, identity, teamperformanceaction.BreakdownInput{
		ListInput: teamperformanceaction.ListInput{Input: scope}, Dimension: domain.ServiceReportDimensionChannel,
	})
	require.NoError(t, err)
	require.Equal(t, 1, channels.Total, "channels")
	require.Equal(t, 2, channels.Rows[0].Closed, "channels")
	require.Equal(t, 1, channels.Rows[0].HumanRequested, "channels")
	require.NotNil(t, channels.Rows[0].FirstResponseMedian, "channels")
	// 公共队列筛选统计公共队列的周期，指定其他团队时不统计。
	public, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7, ChannelID: channelID, PublicQueue: true})
	require.NoError(t, err)
	require.Equal(t, 2, public.Closed, "public queue summary")
	other, err := teamperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, teamperformanceaction.Input{Days: 7, TeamID: uuid.NewV7().String()})
	require.NoError(t, err)
	require.Equal(t, 0, other.Closed, "other team summary")
	require.Nil(t, other.FirstResponseMedian, "other team summary")

	// 真人问题会话只出现在团队表现中，详情带出真人质检结论。
	issues, err := teamperformanceaction.NewIssueListQuery(db).Execute(ctx, identity, teamperformanceaction.IssueListInput{
		ListInput: teamperformanceaction.ListInput{Input: scope}, Issue: domain.ServiceIssueTypeAll,
	})
	require.NoError(t, err)
	require.Equal(t, 1, issues.Total, "team issues")
	require.Equal(t, handled.ID, issues.Issues[0].ServiceSessionID, "team issues")
	require.NotNil(t, issues.Issues[0].HumanIncorrect, "team issues")
	require.True(t, *issues.Issues[0].HumanIncorrect, "team issues")
	_, err = teamperformanceaction.NewIssueListQuery(db).Execute(ctx, identity, teamperformanceaction.IssueListInput{
		ListInput: teamperformanceaction.ListInput{Input: scope}, Issue: domain.ServiceIssueTypeAIIncorrect,
	})
	require.Same(t, teamperformanceaction.ErrIssueInvalid, err, "ai issue type in team report error")
	aiIssues, err := aiperformanceaction.NewIssueListQuery(db).Execute(ctx, identity, aiperformanceaction.IssueListInput{
		Input: aiperformanceaction.Input{Days: 7, ChannelID: channelID}, Issue: domain.ServiceIssueTypeAll,
	})
	require.NoError(t, err)
	require.Equal(t, 0, aiIssues.Total, "ai issues")
	detail, err := serviceissue.NewQuery(db).Execute(ctx, identity, handled.ID)
	require.NoError(t, err)
	require.NotNil(t, detail.HumanIncorrect, "issue detail")
	require.True(t, *detail.HumanIncorrect, "issue detail")
	require.NotEmpty(t, detail.Messages, "issue detail")

	// 真人首接待的周期开启即需要真人并由真人负责；其间没有经过工作时间的回复不记首响用时。
	closedAllWeek := domain.BusinessHours{Enabled: true, TimeZone: "UTC"}
	_, err = customerserviceaction.NewUpdateBusinessHoursAction(db).Execute(ctx, identity, closedAllWeek)
	require.NoError(t, err)
	memberChannelID := f.newChannel(t, identity.WorkspaceIdentity.ID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	memberInput := visitorInput(memberChannelID, "")
	direct := f.receive(t, &memberInput, "发票怎么开")
	var directSessionID string
	require.NoError(t, db.NewSelect().Table("service_conversations").Column("current_service_session_id").
		Where("conversation_id = ?", direct.Conversation.ID).Scan(ctx, &directSessionID))
	opened := loadSession(t, db, directSessionID)
	require.NotNil(t, opened.HumanRequestedAt, "member first reception session")
	require.NotNil(t, opened.HumanAssignedAt, "member first reception session")
	require.True(t, opened.HumanAssignedAt.Equal(*opened.HumanRequestedAt), "member first reception session = %+v", opened)
	_, err = servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer).Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: direct.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "在设置里申请",
	})
	require.NoError(t, err)
	responded := loadSession(t, db, directSessionID)
	require.NotNil(t, responded.HumanFirstResponseAt, "off hours response session")
	require.Nil(t, responded.HumanFirstResponseSec, "off hours response session")
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
		require.NoError(t, f.db.NewSelect().TableExpr("service_sessions AS ss").ColumnExpr("? AS replied", messagequery.HumanReplied("ss")).
			Where("ss.id = ?", sessionID).Scan(ctx, &replied))
		return replied
	}
	require.False(t, humanReplied(), "发起人的消息被算作真人客服回复")
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, testServiceSessionReturner(f.db), f.tasks).Execute(ctx, f.member, conversationID)
	require.NoError(t, err)
	_, err = f.reply(conversationID, "请重启试试", domain.MessageVisibilityShared)
	require.NoError(t, err)
	require.True(t, humanReplied(), "处理人的共享回复未算作真人客服回复")
}
