//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/actions/serviceissue"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceSessionReviews 验证周期质检：按完整周期的参与方出题，AI 独立处理的周期额外判断应转人工未转，有真人回复的周期判断真人答错与态度，AI 答错时登记待补知识，客服改过小结的周期仍会质检，重开后清除结果且过期任务不写入，报表统计满意度与质检分布并列出问题会话。
func TestServiceSessionReviews(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := newKnowledgeTasks(t, db)
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	coordinator := newGroupAgentCoordinator(db)
	claim := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, tasks)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, tasks)
	reopen := servicesessionaction.NewReopenServiceSessionAction(db, testEnqueuer)
	reply := servicesessionaction.NewSendServiceTextMessageAction(db, tasks)
	agent := f.newAgent(t, "质检客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})

	// 判断模型在本测试内启用，结束后恢复为不使用。
	summaryProviderID := seedSummaryModels(t, db, identity)
	settings := customerservice.NewUpdateServiceSummarySettingsAction(db)
	_, err := settings.Execute(ctx, identity, domain.ServiceSummarySettings{
		DecisionModelID: new(aiModelID(t, db, summaryProviderID, "decision-model")),
		Locale:          domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := settings.Execute(context.Background(), identity, domain.ServiceSummarySettings{Locale: domain.LocaleChineseSimplified})
		assert.NoError(t, err)
	})

	decider := &summaryDecider{answers: map[string]decision.Answer{
		"satisfaction":        {Kind: decision.KindScore, LevelProbabilities: []float64{0.1, 0.2, 0.7}},
		"ai_incorrect":        {Kind: decision.KindYesNo, Probability: 0.1},
		"ai_poor_attitude":    {Kind: decision.KindYesNo, Probability: 0.2},
		"ai_missed_handoff":   {Kind: decision.KindYesNo, Probability: 0.9},
		"human_incorrect":     {Kind: decision.KindYesNo, Probability: 0.1},
		"human_poor_attitude": {Kind: decision.KindYesNo, Probability: 0.9},
	}}
	worker := servicesummary.NewWorker(db, tasks, modelcall.New(db, modelcall.Upstreams{Decider: decider, Chat: (&summaryCaller{}).chat}, nil))
	// loadReview 读取周期的质检结果，没有时返回 nil。
	loadReview := func(sessionID string) *servermodels.ServiceSessionReview {
		t.Helper()
		reviews := make([]servermodels.ServiceSessionReview, 0, 1)
		require.NoError(t, db.NewSelect().Model(&reviews).Where("ssr.service_session_id = ?", sessionID).Scan(ctx))
		if len(reviews) == 0 {
			return nil
		}
		return &reviews[0]
	}
	// asked 返回最近一次判断的题目键集合。
	asked := func() map[string]bool {
		keys := make(map[string]bool, len(decider.questions))
		for key := range decider.questions {
			keys[key] = true
		}
		return keys
	}

	// AI 独立解决的周期判断全部四项，应转人工未转成立；AI 答复之后的客户消息超过沟通记录上限时仍按完整周期出题。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "我要投诉，找你们人工")
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("已为您记录", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	_, err = db.NewRaw(`INSERT INTO messages
SELECT (jsonb_populate_record(m, jsonb_build_object('id', uuidv7(), 'client_message_id', uuidv7(), 'idempotency_key', NULL,
	'message_seq', (SELECT max(message_seq) FROM messages WHERE conversation_id = m.conversation_id) + g))).*
FROM messages m, generate_series(1, 200) g
WHERE m.conversation_id = ? AND m.service_session_id = ? AND m.id = ?`,
		resolved.Conversation.ID, resolveRun.ScopeID, resolved.Message.ID).Exec(ctx)
	require.NoError(t, err)
	require.NoError(t, worker.Review(ctx, loadReviewInput(t, resolveRun.ScopeID, tasks, testEnqueuer)))
	require.Len(t, asked(), 4, "AI 独立处理周期的题目")
	aiOnly := loadReview(resolveRun.ScopeID)
	require.NotNil(t, aiOnly)
	require.Equal(t, new(string(domain.ServiceSessionSatisfactionSatisfied)), aiOnly.Satisfaction)
	require.Equal(t, new(true), aiOnly.AIMissedHandoff)
	require.Equal(t, new(false), aiOnly.AIIncorrect)
	require.Equal(t, new(false), aiOnly.AIPoorAttitude)

	// AI 答复后由真人接管并回复的周期不判断应转人工未转，同时判断真人答错与态度，AI 答错时登记待补知识。
	takenInput := visitorInput(channelID, "")
	taken := f.receive(t, &takenInput, "退货运费谁承担")
	takenRun := f.executeQueuedRun(t, taken.Conversation.ID, resolutionRuntime("运费由您承担", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeReply}, nil, nil))
	_, err = claim.Execute(ctx, identity, taken.Conversation.ID)
	require.NoError(t, err)
	_, err = reply.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: taken.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "抱歉，退货运费由我们承担",
	})
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, identity, taken.Conversation.ID)
	require.NoError(t, err)
	decider.answers["ai_incorrect"] = decision.Answer{Kind: decision.KindYesNo, Probability: 0.9}
	decider.answers["satisfaction"] = decision.Answer{Kind: decision.KindScore, LevelProbabilities: []float64{0.8, 0.1, 0.1}}
	require.NoError(t, worker.Review(ctx, loadReviewInput(t, takenRun.ScopeID, tasks, testEnqueuer)))
	keys := asked()
	require.Len(t, keys, 5, "真人接管周期的题目")
	require.False(t, keys["ai_missed_handoff"])
	require.True(t, keys["human_incorrect"])
	require.True(t, keys["human_poor_attitude"])
	takenReview := loadReview(takenRun.ScopeID)
	require.NotNil(t, takenReview)
	require.Equal(t, new(true), takenReview.AIIncorrect)
	require.Nil(t, takenReview.AIMissedHandoff)
	require.Equal(t, new(string(domain.ServiceSessionSatisfactionDissatisfied)), takenReview.Satisfaction)
	require.Equal(t, new(false), takenReview.HumanIncorrect)
	require.Equal(t, new(true), takenReview.HumanPoorAttitude)
	gaps := make([]servermodels.KnowledgeGap, 0, 1)
	require.NoError(t, db.NewSelect().Model(&gaps).Where("kg.service_session_id = ?", takenRun.ScopeID).Scan(ctx))
	require.Len(t, gaps, 1)
	require.Equal(t, string(domain.KnowledgeGapSourcePossiblyWrong), gaps[0].Source)
	require.Equal(t, string(domain.KnowledgeGapStatusPending), gaps[0].Status)

	// 真人处理且没有 AI 答复的周期只判断满意度；客服改过小结后再次关闭仍投递质检，重开时清除结果，过期任务不写入。
	humanChannelID := f.newChannel(t, identity.WorkspaceIdentity.ID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	humanInput := visitorInput(humanChannelID, "")
	human := f.receive(t, &humanInput, "发票怎么开")
	_, err = claim.Execute(ctx, identity, human.Conversation.ID)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, identity, human.Conversation.ID)
	require.NoError(t, err)
	var humanSessionID string
	require.NoError(t, db.NewSelect().Table("service_conversations").Column("current_service_session_id").
		Where("conversation_id = ?", human.Conversation.ID).Scan(ctx, &humanSessionID))
	decider.answers["satisfaction"] = decision.Answer{Kind: decision.KindScore, LevelProbabilities: []float64{0.4, 0.3, 0.3}}
	firstInput := loadReviewInput(t, humanSessionID, tasks, testEnqueuer)
	require.NoError(t, worker.Review(ctx, firstInput))
	keys = asked()
	require.Len(t, keys, 1, "真人处理周期的题目")
	require.True(t, keys["satisfaction"])
	review := loadReview(humanSessionID)
	require.NotNil(t, review)
	require.Nil(t, review.Satisfaction)
	require.Nil(t, review.AIIncorrect)
	require.Nil(t, review.AIPoorAttitude)
	require.Nil(t, review.HumanIncorrect)
	_, err = db.NewUpdate().Table("service_sessions").Set("summary_edited_by_identity_id = ?, summary_status = ?, summary = ?",
		identity.WorkspaceIdentity.ID, domain.ServiceSessionSummaryReady, "客服填写的小结").Where("id = ?", humanSessionID).Exec(ctx)
	require.NoError(t, err)
	_, err = reopen.Execute(ctx, identity, human.Conversation.ID)
	require.NoError(t, err)
	require.Nil(t, loadReview(humanSessionID), "重开后的质检")
	_, err = closeSession.Execute(ctx, identity, human.Conversation.ID)
	require.NoError(t, err)
	secondInput := loadReviewInput(t, humanSessionID, tasks, testEnqueuer)
	require.True(t, secondInput.ClosedAt.After(firstInput.ClosedAt), "客服改过小结后再次关闭未投递质检：%+v", secondInput)
	require.NoError(t, worker.Review(ctx, firstInput))
	require.Nil(t, loadReview(humanSessionID), "过期任务写入了质检")

	// 报表统计满意度与质检分布，问题会话按类型筛选并给出详情。
	scope := aiperformanceaction.Input{Days: 7, ChannelID: channelID}
	overview, err := aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, scope)
	require.NoError(t, err)
	summary := overview.Summary
	type reviewCounts struct {
		Satisfied, Dissatisfied, Neutral, AIIncorrect, AIIncorrectReviewed, AIMissedHandoff, AIMissedHandoffReviewed, AIPoorAttitude, AIPoorAttitudeReviewed int
	}
	require.Equal(t, reviewCounts{1, 1, 0, 1, 2, 1, 1, 0, 2}, reviewCounts{
		summary.Satisfied, summary.Dissatisfied, summary.Neutral, summary.AIIncorrect, summary.AIIncorrectReviewed,
		summary.AIMissedHandoff, summary.AIMissedHandoffReviewed, summary.AIPoorAttitude, summary.AIPoorAttitudeReviewed,
	}, "质检统计")
	issues := aiperformanceaction.NewIssueListQuery(db)
	for issue, want := range map[domain.ServiceIssueType]string{
		domain.ServiceIssueTypeAIMissedHandoff: resolveRun.ScopeID,
		domain.ServiceIssueTypeAIIncorrect:     takenRun.ScopeID,
		domain.ServiceIssueTypeDissatisfied:    takenRun.ScopeID,
	} {
		list, err := issues.Execute(ctx, identity, aiperformanceaction.IssueListInput{Input: scope, Issue: issue})
		require.NoError(t, err, "%s 问题会话", issue)
		require.Equal(t, 1, list.Total, "%s 问题会话", issue)
		require.Len(t, list.Issues, 1, "%s 问题会话", issue)
		require.Equal(t, want, list.Issues[0].ServiceSessionID, "%s 问题会话", issue)
	}
	all, err := issues.Execute(ctx, identity, aiperformanceaction.IssueListInput{Input: scope, Issue: domain.ServiceIssueTypeAll})
	require.NoError(t, err)
	require.Equal(t, 2, all.Total)
	require.Equal(t, takenRun.ScopeID, all.Issues[0].ServiceSessionID)
	require.NotNil(t, all.Issues[0].ChannelName)
	require.Equal(t, "退货运费谁承担", all.Issues[0].Preview)
	_, err = issues.Execute(ctx, identity, aiperformanceaction.IssueListInput{Input: scope, Issue: "unknown"})
	require.Same(t, aiperformanceaction.ErrIssueInvalid, err, "未知问题类型")
	detail, err := serviceissue.NewQuery(db).Execute(ctx, identity, takenRun.ScopeID)
	require.NoError(t, err)
	require.Equal(t, new(true), detail.AIIncorrect)
	require.Equal(t, new(true), detail.HumanPoorAttitude)
	require.Equal(t, taken.Message.ID, detail.OpeningMessageID)
	require.Len(t, detail.Messages, 3)
	require.Equal(t, "customer", detail.Messages[0].Sender)
	require.Equal(t, "ai", detail.Messages[1].Sender)
	require.Equal(t, "staff", detail.Messages[2].Sender)
	_, err = serviceissue.NewQuery(db).Execute(ctx, identity, humanSessionID)
	require.Same(t, serviceissue.ErrNotFound, err, "未质检周期的详情")
}

// loadReviewInput 返回登记器中周期最近一次投递的质检任务输入。
func loadReviewInput(t *testing.T, serviceSessionID string, recorders ...*servertest.Tasks) servicesummary.ReviewInput {
	t.Helper()
	return servertest.LatestInput(t, servicesummary.ReviewActionName, func(input servicesummary.ReviewInput) bool { return input.ServiceSessionID == serviceSessionID }, recorders...)
}
