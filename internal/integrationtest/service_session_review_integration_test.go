//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiperformanceaction "github.com/runforyou-ai/luway/internal/actions/aiperformance"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/serviceissue"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TestServiceSessionReviews 验证周期质检：按完整周期的参与方出题，AI 独立处理的周期额外判断应转人工未转，有真人回复的周期判断真人答错与态度，AI 答错时登记待补知识，客服改过小结的周期仍会质检，重开后清除结果且过期任务不写入，报表统计满意度与质检分布并列出问题会话。
func TestServiceSessionReviews(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := newKnowledgeTasks(t, db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Registry().RegisterJSON(deliveryaction.SendActionName, func(context.Context, deliveryaction.Input) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Organization.ID)
	coordinator := newGroupAgentCoordinator(db)
	claim := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, tasks)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, tasks)
	reopen := servicesessionaction.NewReopenServiceSessionAction(db)
	reply := servicesessionaction.NewSendServiceTextMessageAction(db, tasks)
	agent := f.newAgent(t, "质检客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})

	// 判断模型在本测试内启用，结束后恢复为不使用。
	summaryProviderID := seedSummaryModels(t, db, identity)
	settings := customerservice.NewUpdateServiceSummarySettingsAction(db)
	if _, err := settings.Execute(ctx, identity, domain.ServiceSummarySettings{
		Decision: &domain.AIModelReference{ProviderID: summaryProviderID, ModelIdentifier: "decision-model"},
		Locale:   domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := settings.Execute(context.Background(), identity, domain.ServiceSummarySettings{Locale: domain.LocaleChineseSimplified}); err != nil {
			t.Error(err)
		}
	})

	decider := &summaryDecider{answers: map[string]decision.Answer{
		"satisfaction":        {Kind: decision.KindScore, LevelProbabilities: []float64{0.1, 0.2, 0.7}},
		"ai_incorrect":        {Kind: decision.KindYesNo, Probability: 0.1},
		"ai_poor_attitude":    {Kind: decision.KindYesNo, Probability: 0.2},
		"ai_missed_handoff":   {Kind: decision.KindYesNo, Probability: 0.9},
		"human_incorrect":     {Kind: decision.KindYesNo, Probability: 0.1},
		"human_poor_attitude": {Kind: decision.KindYesNo, Probability: 0.9},
	}}
	worker := servicesummary.NewWorker(db, tasks, decider, &summaryCaller{})
	// loadReview 读取周期的质检结果，没有时返回 nil。
	loadReview := func(sessionID string) *servermodels.ServiceSessionReview {
		t.Helper()
		reviews := make([]servermodels.ServiceSessionReview, 0, 1)
		if err := db.NewSelect().Model(&reviews).Where("ssr.service_session_id = ?", sessionID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
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
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("已为您记录", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	if _, err := db.NewRaw(`INSERT INTO messages
SELECT (jsonb_populate_record(m, jsonb_build_object('id', uuidv7(), 'client_message_id', uuidv7(), 'idempotency_key', NULL,
	'message_seq', (SELECT max(message_seq) FROM messages WHERE conversation_id = m.conversation_id) + g))).*
FROM messages m, generate_series(1, 200) g
WHERE m.conversation_id = ? AND m.service_session_id = ? AND m.id = ?`,
		resolved.Conversation.ID, resolveRun.ScopeID, resolved.Message.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.Review(ctx, loadReviewInput(t, db, resolveRun.ScopeID)); err != nil {
		t.Fatal(err)
	}
	if keys := asked(); len(keys) != 4 {
		t.Fatalf("AI 独立处理周期的题目 = %v", keys)
	}
	aiOnly := loadReview(resolveRun.ScopeID)
	if aiOnly == nil || aiOnly.Satisfaction == nil || *aiOnly.Satisfaction != string(domain.ServiceSessionSatisfactionSatisfied) ||
		aiOnly.AIMissedHandoff == nil || !*aiOnly.AIMissedHandoff || aiOnly.AIIncorrect == nil || *aiOnly.AIIncorrect || aiOnly.AIPoorAttitude == nil || *aiOnly.AIPoorAttitude {
		t.Fatalf("AI 独立处理周期的质检 = %+v", aiOnly)
	}

	// AI 答复后由真人接管并回复的周期不判断应转人工未转，同时判断真人答错与态度，AI 答错时登记待补知识。
	takenInput := visitorInput(channelID, "")
	taken := f.receive(t, &takenInput, "退货运费谁承担")
	takenRun := f.executeQueuedRun(t, taken.Conversation.ID, resolutionRuntime("运费由您承担", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeReply}, nil, nil))
	if _, err := claim.Execute(ctx, identity, taken.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reply.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: taken.Conversation.ID, ClientMessageID: uuid.NewV7().String(), Body: "抱歉，退货运费由我们承担",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, identity, taken.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	decider.answers["ai_incorrect"] = decision.Answer{Kind: decision.KindYesNo, Probability: 0.9}
	decider.answers["satisfaction"] = decision.Answer{Kind: decision.KindScore, LevelProbabilities: []float64{0.8, 0.1, 0.1}}
	if err := worker.Review(ctx, loadReviewInput(t, db, takenRun.ScopeID)); err != nil {
		t.Fatal(err)
	}
	if keys := asked(); len(keys) != 5 || keys["ai_missed_handoff"] || !keys["human_incorrect"] || !keys["human_poor_attitude"] {
		t.Fatalf("真人接管周期的题目 = %v", keys)
	}
	takenReview := loadReview(takenRun.ScopeID)
	if takenReview == nil || takenReview.AIIncorrect == nil || !*takenReview.AIIncorrect || takenReview.AIMissedHandoff != nil ||
		takenReview.Satisfaction == nil || *takenReview.Satisfaction != string(domain.ServiceSessionSatisfactionDissatisfied) ||
		takenReview.HumanIncorrect == nil || *takenReview.HumanIncorrect || takenReview.HumanPoorAttitude == nil || !*takenReview.HumanPoorAttitude {
		t.Fatalf("真人接管周期的质检 = %+v", takenReview)
	}
	gaps := make([]servermodels.KnowledgeGap, 0, 1)
	if err := db.NewSelect().Model(&gaps).Where("kg.service_session_id = ?", takenRun.ScopeID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].Source != string(domain.KnowledgeGapSourcePossiblyWrong) || gaps[0].Status != string(domain.KnowledgeGapStatusPending) {
		t.Fatalf("答错登记的待补知识 = %+v", gaps)
	}

	// 真人处理且没有 AI 答复的周期只判断满意度；客服改过小结后再次关闭仍投递质检，重开时清除结果，过期任务不写入。
	humanChannelID := f.newChannel(t, identity.OrganizationIdentity.ID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	humanInput := visitorInput(humanChannelID, "")
	human := f.receive(t, &humanInput, "发票怎么开")
	if _, err := claim.Execute(ctx, identity, human.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, identity, human.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	var humanSessionID string
	if err := db.NewSelect().Table("service_conversations").Column("current_service_session_id").
		Where("conversation_id = ?", human.Conversation.ID).Scan(ctx, &humanSessionID); err != nil {
		t.Fatal(err)
	}
	decider.answers["satisfaction"] = decision.Answer{Kind: decision.KindScore, LevelProbabilities: []float64{0.4, 0.3, 0.3}}
	firstInput := loadReviewInput(t, db, humanSessionID)
	if err := worker.Review(ctx, firstInput); err != nil {
		t.Fatal(err)
	}
	if keys := asked(); len(keys) != 1 || !keys["satisfaction"] {
		t.Fatalf("真人处理周期的题目 = %v", keys)
	}
	if review := loadReview(humanSessionID); review == nil || review.Satisfaction != nil || review.AIIncorrect != nil || review.AIPoorAttitude != nil || review.HumanIncorrect != nil {
		t.Fatalf("真人处理周期的质检 = %+v", review)
	}
	if _, err := db.NewUpdate().Table("service_sessions").Set("summary_edited_by_identity_id = ?, summary_status = ?, summary = ?",
		identity.OrganizationIdentity.ID, domain.ServiceSessionSummaryReady, "客服填写的小结").Where("id = ?", humanSessionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reopen.Execute(ctx, identity, human.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if review := loadReview(humanSessionID); review != nil {
		t.Fatalf("重开后的质检 = %+v", review)
	}
	if _, err := closeSession.Execute(ctx, identity, human.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if secondInput := loadReviewInput(t, db, humanSessionID); !secondInput.ClosedAt.After(firstInput.ClosedAt) {
		t.Fatalf("客服改过小结后再次关闭未投递质检：%+v", secondInput)
	}
	if err := worker.Review(ctx, firstInput); err != nil {
		t.Fatal(err)
	}
	if review := loadReview(humanSessionID); review != nil {
		t.Fatalf("过期任务写入了质检 = %+v", review)
	}

	// 报表统计满意度与质检分布，问题会话按类型筛选并给出详情。
	scope := aiperformanceaction.Input{Days: 7, ChannelID: channelID}
	overview, err := aiperformanceaction.NewOverviewQuery(db).Execute(ctx, identity, scope)
	if err != nil {
		t.Fatal(err)
	}
	summary := overview.Summary
	if summary.Satisfied != 1 || summary.Dissatisfied != 1 || summary.Neutral != 0 ||
		summary.AIIncorrect != 1 || summary.AIIncorrectReviewed != 2 || summary.AIMissedHandoff != 1 || summary.AIMissedHandoffReviewed != 1 ||
		summary.AIPoorAttitude != 0 || summary.AIPoorAttitudeReviewed != 2 {
		t.Fatalf("质检统计 = %+v", summary)
	}
	issues := aiperformanceaction.NewIssueListQuery(db)
	for issue, want := range map[domain.ServiceIssueType]string{
		domain.ServiceIssueTypeAIMissedHandoff: resolveRun.ScopeID,
		domain.ServiceIssueTypeAIIncorrect:     takenRun.ScopeID,
		domain.ServiceIssueTypeDissatisfied:    takenRun.ScopeID,
	} {
		list, err := issues.Execute(ctx, identity, aiperformanceaction.IssueListInput{Input: scope, Issue: issue})
		if err != nil || list.Total != 1 || len(list.Issues) != 1 || list.Issues[0].ServiceSessionID != want {
			t.Fatalf("%s 问题会话 = %+v, %v", issue, list, err)
		}
	}
	all, err := issues.Execute(ctx, identity, aiperformanceaction.IssueListInput{Input: scope, Issue: domain.ServiceIssueTypeAll})
	if err != nil || all.Total != 2 || all.Issues[0].ServiceSessionID != takenRun.ScopeID || all.Issues[0].ChannelName == nil || all.Issues[0].Preview != "退货运费谁承担" {
		t.Fatalf("全部问题会话 = %+v, %v", all, err)
	}
	if _, err := issues.Execute(ctx, identity, aiperformanceaction.IssueListInput{Input: scope, Issue: "unknown"}); err != aiperformanceaction.ErrIssueInvalid {
		t.Fatalf("未知问题类型 = %v", err)
	}
	detail, err := serviceissue.NewQuery(db).Execute(ctx, identity, takenRun.ScopeID)
	if err != nil || detail.AIIncorrect == nil || !*detail.AIIncorrect || detail.HumanPoorAttitude == nil || !*detail.HumanPoorAttitude || detail.OpeningMessageID != taken.Message.ID || len(detail.Messages) != 3 ||
		detail.Messages[0].Sender != "customer" || detail.Messages[1].Sender != "ai" || detail.Messages[2].Sender != "staff" {
		t.Fatalf("问题会话详情 = %+v, %v", detail, err)
	}
	if _, err := serviceissue.NewQuery(db).Execute(ctx, identity, humanSessionID); err != serviceissue.ErrNotFound {
		t.Fatalf("未质检周期的详情 = %v", err)
	}
}

// loadReviewInput 读取周期最近一次投递的质检任务输入。
func loadReviewInput(t *testing.T, db *bun.DB, serviceSessionID string) servicesummary.ReviewInput {
	t.Helper()
	var row struct {
		Payload json.RawMessage `bun:"payload"`
	}
	if err := db.NewSelect().TableExpr("task_runs AS tr").Column("tr.payload").
		Where("tr.action_name = ? AND tr.payload->>'serviceSessionId' = ?", servicesummary.ReviewActionName, serviceSessionID).
		OrderExpr("tr.created_at DESC, tr.id DESC").Limit(1).
		Scan(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	var input servicesummary.ReviewInput
	if err := json.Unmarshal(row.Payload, &input); err != nil {
		t.Fatal(err)
	}
	return input
}
