//go:build server

package integrationtest

import (
	"context"
	"errors"
	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	"strings"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestKnowledgeGaps 验证待补知识的登记、起草、清单、详情、加入知识库与忽略：知识不足转人工的周期在关闭时登记，重开后保留待处理条目并在再次关闭时重新起草，AI 员工关闭的周期被评价未解决或质检判断答错时登记复核条目，已处理的周期出现新的触发事件时再次登记。
func TestKnowledgeGaps(t *testing.T) {
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
	agent := f.newAgent(t, "待补知识客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	list := knowledgegap.NewListQuery(db)
	dismiss := knowledgegap.NewDismissAction(db)

	// 小结与判断模型在本测试内启用，结束后恢复为不使用。
	summaryProviderID := seedSummaryModels(t, db, identity)
	settings := customerservice.NewUpdateServiceSummarySettingsAction(db)
	if _, err := settings.Execute(ctx, identity, domain.ServiceSummarySettings{
		Decision: &domain.AIModelReference{ProviderID: summaryProviderID, ModelIdentifier: "decision-model"},
		Summary:  &domain.AIModelReference{ProviderID: summaryProviderID, ModelIdentifier: "chat-model"},
		Locale:   domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := settings.Execute(context.Background(), identity, domain.ServiceSummarySettings{Locale: domain.LocaleChineseSimplified}); err != nil {
			t.Error(err)
		}
	})

	// loadGaps 按登记顺序读取周期的全部待补知识。
	loadGaps := func(sessionID string) []servermodels.KnowledgeGap {
		t.Helper()
		gaps := make([]servermodels.KnowledgeGap, 0, 2)
		if err := db.NewSelect().Model(&gaps).Where("kg.service_session_id = ?", sessionID).OrderExpr("kg.created_at, kg.id").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		return gaps
	}
	// draftInput 返回条目当前起草请求对应的任务输入。
	draftInput := func(gap servermodels.KnowledgeGap) knowledgegap.DraftInput {
		return knowledgegap.DraftInput{OrganizationID: identity.Organization.ID, KnowledgeGapID: gap.ID, RequestedAt: gap.DraftRequestedAt}
	}
	// handOff 让访客提问后由 AI 员工以知识不足转人工，返回会话与周期编号。
	handOff := func(question string) (string, string) {
		t.Helper()
		input := visitorInput(channelID, "")
		received := f.receive(t, &input, question)
		run := f.executeQueuedRun(t, received.Conversation.ID, handoffRuntime("资料里没有相关说明", nil))
		return received.Conversation.ID, run.ScopeID
	}

	// 真人接手答复并关闭后登记待补知识，并投递起草任务。
	conversationID, sessionID := handOff("海外仓发货要几天")
	if _, err := claim.Execute(ctx, identity, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := reply.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "海外仓订单一般 3 到 5 个工作日送达",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, identity, conversationID); err != nil {
		t.Fatal(err)
	}
	gaps := loadGaps(sessionID)
	if len(gaps) != 1 || gaps[0].Source != string(domain.KnowledgeGapSourceKnowledgeGap) || gaps[0].Status != string(domain.KnowledgeGapStatusPending) ||
		gaps[0].DraftStatus != string(domain.KnowledgeGapDraftStatusPending) || gaps[0].QuestionMessageID == nil {
		t.Fatalf("登记的待补知识 = %+v", gaps)
	}
	gap := gaps[0]
	var drafts int
	if err := db.NewSelect().Table("task_runs").ColumnExpr("count(*)").
		Where("action_name = ? AND payload->>'knowledgeGapId' = ?", knowledgegap.DraftActionName, gap.ID).Scan(ctx, &drafts); err != nil || drafts != 1 {
		t.Fatalf("起草任务数 = %d, %v", drafts, err)
	}

	// 小结模型按真人答复起草问答，重复执行不再调用模型。
	caller := &summaryCaller{text: `{"question":"海外仓发货需要多久？","questionIndex":0,"similarQuestions":["海外仓几天到货"," ","海外仓发货需要多久？"],"answer":"海外仓订单一般 3 到 5 个工作日送达。"}`}
	worker := servicesummary.NewWorker(db, tasks, &summaryDecider{}, caller)
	if err := worker.DraftKnowledgeGap(ctx, draftInput(gap)); err != nil {
		t.Fatal(err)
	}
	if err := worker.DraftKnowledgeGap(ctx, draftInput(gap)); err != nil || caller.calls != 1 {
		t.Fatalf("重复起草调用次数 = %d, %v", caller.calls, err)
	}

	// 默认知识库取 AI 员工绑定的问答知识库。
	base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "待补知识 FAQ "+uuid.NewV7().String(), domain.KnowledgeBaseCategoryQA))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewUpdate().Table("agent_revisions").
		Set("configuration = jsonb_set(configuration, '{knowledgeBaseIds}', to_jsonb(ARRAY[?::text]))", base.ID).
		Where("id = (SELECT active_revision_id FROM agents WHERE identity_id = ?)", agent.IdentityID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := list.Execute(ctx, identity, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusPending})
	if err != nil || pending.Total != 1 || len(pending.Gaps) != 1 || pending.Gaps[0].Question != "海外仓发货需要多久？" || !pending.Gaps[0].HasDraft ||
		pending.Gaps[0].ConversationID != conversationID {
		t.Fatalf("待处理清单 = %+v, %v", pending, err)
	}
	detail, err := knowledgegap.NewGetQuery(db).Execute(ctx, identity, gap.ID)
	if err != nil {
		t.Fatal(err)
	}
	senders := make([]string, 0, len(detail.Messages))
	for _, message := range detail.Messages {
		senders = append(senders, message.Sender)
	}
	if detail.Question != "海外仓发货要几天" || detail.DraftStatus != string(domain.KnowledgeGapDraftStatusReady) || detail.DraftQuestion == nil ||
		len(detail.DraftSimilarQuestions) != 1 || detail.DraftSimilarQuestions[0] != "海外仓几天到货" ||
		detail.DraftAnswer == nil || *detail.DraftAnswer != "海外仓订单一般 3 到 5 个工作日送达。" ||
		detail.QuestionMessageID == nil || *detail.QuestionMessageID != *gap.QuestionMessageID ||
		detail.DefaultKnowledgeBaseID == nil || *detail.DefaultKnowledgeBaseID != base.ID || !detail.Evaluable ||
		len(senders) < 3 || senders[0] != "customer" || senders[len(senders)-1] != "staff" {
		t.Fatalf("详情 = %+v, 发送方 = %v", detail, senders)
	}

	// 加入知识库后创建问答并记为已加入，不能再次加入或忽略。
	accept := knowledgegap.NewAcceptAction(db, knowledgeaction.NewSaveQAEntryAction(db, tasks), agentevaluationaction.KnowledgeGapCases{})
	acceptInput := knowledgegap.AcceptInput{KnowledgeBaseID: base.ID, AddToEvaluation: true, QA: knowledgeaction.QAInput{
		Question: *detail.DraftQuestion, Answer: *detail.DraftAnswer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: detail.DraftSimilarQuestions[0]}},
	}}
	if err := accept.Execute(ctx, identity, gap.ID, acceptInput); err != nil {
		t.Fatal(err)
	}
	// 同时加入评测时，以待补知识的提问为用例，期望答复，标准答案为保存的问答答案，并保存客户上下文。
	evaluationCase := &servermodels.AgentEvaluationCase{}
	if err := db.NewSelect().Model(evaluationCase).Where("aec.agent_id = ? AND aec.service_session_id = ?", agent.ID, sessionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if evaluationCase.Source != string(domain.AgentEvaluationCaseSourceKnowledgeGap) || evaluationCase.Question != "海外仓发货要几天" ||
		evaluationCase.ExpectedAction != string(domain.AgentRunOutcomeReply) || evaluationCase.ExpectedAnswer != "海外仓订单一般 3 到 5 个工作日送达。" ||
		evaluationCase.OccurredAt == nil || !strings.Contains(string(evaluationCase.Context), "customerContext") {
		t.Fatalf("加入评测的用例 = %+v context = %s", evaluationCase, evaluationCase.Context)
	}
	gap = loadGaps(sessionID)[0]
	if gap.Status != string(domain.KnowledgeGapStatusAccepted) || gap.KnowledgeBaseID == nil || *gap.KnowledgeBaseID != base.ID || gap.QAEntryID == nil || gap.HandledAt == nil {
		t.Fatalf("加入后的待补知识 = %+v", gap)
	}
	entry, err := knowledgeaction.NewGetQAEntryQuery(db).Execute(ctx, identity, base.ID, *gap.QAEntryID)
	if err != nil || entry.Question != "海外仓发货需要多久？" || len(entry.SimilarQuestions) != 1 {
		t.Fatalf("加入的问答 = %+v, %v", entry, err)
	}
	if err := accept.Execute(ctx, identity, gap.ID, acceptInput); !errors.Is(err, knowledgegap.ErrHandled) {
		t.Fatalf("再次加入 = %v", err)
	}
	if err := dismiss.Execute(ctx, identity, gap.ID); !errors.Is(err, knowledgegap.ErrHandled) {
		t.Fatalf("忽略已加入的条目 = %v", err)
	}
	// 已处理的周期重开再关闭时，同一转人工事件不再登记。
	if _, err := reopen.Execute(ctx, identity, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, identity, conversationID); err != nil {
		t.Fatal(err)
	}
	if gaps := loadGaps(sessionID); len(gaps) != 1 {
		t.Fatalf("已处理周期再次关闭后的待补知识 = %+v", gaps)
	}

	// 重开保留待处理条目，再次关闭时重新起草，旧的起草任务不再写入。
	second, secondSessionID := handOff("海外仓能发哪些国家")
	if _, err := claim.Execute(ctx, identity, second); err != nil {
		t.Fatal(err)
	}
	if _, err := closeSession.Execute(ctx, identity, second); err != nil {
		t.Fatal(err)
	}
	first := loadGaps(secondSessionID)[0]
	if _, err := reopen.Execute(ctx, identity, second); err != nil {
		t.Fatal(err)
	}
	if kept := loadGaps(secondSessionID); len(kept) != 1 || kept[0].ID != first.ID || kept[0].Status != string(domain.KnowledgeGapStatusPending) {
		t.Fatalf("重开后的待补知识 = %+v", kept)
	}
	if _, err := closeSession.Execute(ctx, identity, second); err != nil {
		t.Fatal(err)
	}
	redrafted := loadGaps(secondSessionID)
	if len(redrafted) != 1 || redrafted[0].ID != first.ID || redrafted[0].DraftStatus != string(domain.KnowledgeGapDraftStatusPending) ||
		!redrafted[0].DraftRequestedAt.After(first.DraftRequestedAt) {
		t.Fatalf("再次关闭后的待补知识 = %+v", redrafted)
	}
	staleCaller := &summaryCaller{text: `{"question":"旧问题","answer":""}`}
	stale := servicesummary.NewWorker(db, tasks, &summaryDecider{}, staleCaller)
	if err := stale.DraftKnowledgeGap(ctx, draftInput(first)); err != nil || staleCaller.calls != 0 {
		t.Fatalf("旧起草任务调用次数 = %d, %v", staleCaller.calls, err)
	}
	if err := stale.FinalizeKnowledgeGapDraftFailure(ctx, draftInput(first), errors.New("旧任务失败")); err != nil {
		t.Fatal(err)
	}
	if current := loadGaps(secondSessionID)[0]; current.DraftStatus != string(domain.KnowledgeGapDraftStatusPending) {
		t.Fatalf("旧任务失败后的草稿状态 = %s", current.DraftStatus)
	}
	if err := stale.FinalizeKnowledgeGapDraftFailure(ctx, draftInput(redrafted[0]), errors.New("起草失败")); err != nil {
		t.Fatal(err)
	}
	if current := loadGaps(secondSessionID)[0]; current.DraftStatus != string(domain.KnowledgeGapDraftStatusFailed) {
		t.Fatalf("起草失败后的草稿状态 = %s", current.DraftStatus)
	}

	// 并入已有问答时追加相似问题并保留原有内容编号。
	if err := accept.Execute(ctx, identity, first.ID, knowledgegap.AcceptInput{KnowledgeBaseID: base.ID, EntryID: entry.ID, QA: knowledgeaction.QAInput{
		Question: entry.Question, Answer: entry.Answer,
		SimilarQuestions: append(entry.SimilarQuestions, knowledgeaction.QASimilarQuestion{Content: "海外仓能发哪些国家"}),
	}}); err != nil {
		t.Fatal(err)
	}
	updated, err := knowledgeaction.NewGetQAEntryQuery(db).Execute(ctx, identity, base.ID, entry.ID)
	if err != nil || len(updated.SimilarQuestions) != 2 || updated.SimilarQuestions[0].ID != entry.SimilarQuestions[0].ID {
		t.Fatalf("并入后的问答 = %+v, %v", updated, err)
	}

	// AI 员工关闭的周期被访客评价未解决时登记复核条目，起草时按 AI 判断更新客户提问消息。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "你好")
	f.receive(t, &resolvedInput, "退货运费谁承担")
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("由我们承担", agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	if _, err := customerchataction.NewRateWebsiteServiceSessionAction(db, tasks).Execute(ctx, customerchataction.WebsiteServiceSessionRatingInput{
		ChannelID: channelID, ExternalID: resolvedInput.ExternalID, ConversationID: resolved.Conversation.ID, ServiceSessionID: resolveRun.ScopeID,
	}); err != nil {
		t.Fatal(err)
	}
	gaps = loadGaps(resolveRun.ScopeID)
	if len(gaps) != 1 || gaps[0].Source != string(domain.KnowledgeGapSourceRatedUnresolved) || gaps[0].QuestionMessageID == nil {
		t.Fatalf("评价未解决的待补知识 = %+v", gaps)
	}
	rated := gaps[0]
	greetingID := *rated.QuestionMessageID
	reviewCaller := &summaryCaller{text: `{"summary":"客户询问退货运费。","question":"退货运费由谁承担？","questionIndex":1,"similarQuestions":[],"answer":""}`}
	reviewer := servicesummary.NewWorker(db, tasks, &summaryDecider{answers: map[string]decision.Answer{
		"ai_incorrect": {Kind: decision.KindYesNo, Probability: 0.9},
	}}, reviewCaller)
	if err := reviewer.DraftKnowledgeGap(ctx, draftInput(rated)); err != nil {
		t.Fatal(err)
	}
	rated = loadGaps(resolveRun.ScopeID)[0]
	if rated.DraftStatus != string(domain.KnowledgeGapDraftStatusReady) || rated.DraftAnswer == nil || *rated.DraftAnswer != "" ||
		rated.QuestionMessageID == nil || *rated.QuestionMessageID == greetingID {
		t.Fatalf("起草后的复核条目 = %+v", rated)
	}
	if total, err := knowledgegap.PendingCount(ctx, db, identity.Organization.ID, knowledgegap.Scope{ChannelID: channelID}); err != nil || total != 1 {
		t.Fatalf("待处理条数 = %d, %v", total, err)
	}
	if err := dismiss.Execute(ctx, identity, rated.ID); err != nil {
		t.Fatal(err)
	}

	// 已处理的周期出现新的触发事件时再次登记：质检认为 AI 答复可能有误。
	if err := reviewer.Review(ctx, loadReviewInput(t, db, resolveRun.ScopeID)); err != nil {
		t.Fatal(err)
	}
	gaps = loadGaps(resolveRun.ScopeID)
	if len(gaps) != 2 || gaps[1].Source != string(domain.KnowledgeGapSourcePossiblyWrong) || gaps[1].Status != string(domain.KnowledgeGapStatusPending) ||
		gaps[1].TriggerMessageID == rated.TriggerMessageID {
		t.Fatalf("可能答错的待补知识 = %+v", gaps)
	}
	dismissed, err := list.Execute(ctx, identity, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusDismissed})
	if err != nil || dismissed.Total != 1 || dismissed.Gaps[0].ID != rated.ID || dismissed.Gaps[0].Question != "退货运费由谁承担？" || !dismissed.Gaps[0].HasDraft {
		t.Fatalf("已忽略清单 = %+v, %v", dismissed, err)
	}
	accepted, err := list.Execute(ctx, identity, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusAccepted})
	if err != nil || accepted.Total != 2 {
		t.Fatalf("已加入清单 = %+v, %v", accepted, err)
	}
}
