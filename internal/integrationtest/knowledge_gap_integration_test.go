//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentevaluationaction "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	"github.com/runforyou-ai/luway/internal/actions/knowledgegap"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeGaps 验证待补知识的登记、起草、清单、详情、加入知识库与忽略，以及成员都能查看全部条目、没有 AI 员工管理权限时只能处理本人负责的 AI 员工的待补知识、拥有 AI 员工管理权限的成员可以处理全部条目：知识不足转人工的周期在关闭时登记，重开后保留待处理条目并在再次关闭时重新起草，AI 员工关闭的周期被评价未解决或质检判断答错时登记复核条目，已处理的周期出现新的触发事件时再次登记。
func TestKnowledgeGaps(t *testing.T) {
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
	agent := f.newAgent(t, "待补知识客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	list := knowledgegap.NewListQuery(db)
	dismiss := knowledgegap.NewDismissAction(db)

	// 小结与判断模型在本测试内启用，结束后恢复为不使用。
	summaryProviderID := seedSummaryModels(t, db, identity)
	settings := customerservice.NewUpdateServiceSummarySettingsAction(db)
	_, err := settings.Execute(ctx, identity, domain.ServiceSummarySettings{
		DecisionModelID: new(aiModelID(t, db, summaryProviderID, "decision-model")),
		SummaryModelID:  new(aiModelID(t, db, summaryProviderID, "chat-model")),
		Locale:          domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := settings.Execute(context.Background(), identity, domain.ServiceSummarySettings{Locale: domain.LocaleChineseSimplified})
		assert.NoError(t, err)
	})

	// loadGaps 按登记顺序读取周期的全部待补知识。
	loadGaps := func(sessionID string) []servermodels.KnowledgeGap {
		t.Helper()
		gaps := make([]servermodels.KnowledgeGap, 0, 2)
		require.NoError(t, db.NewSelect().Model(&gaps).Where("kg.service_session_id = ?", sessionID).OrderExpr("kg.created_at, kg.id").Scan(ctx))
		return gaps
	}
	// draftInput 返回条目当前起草请求对应的任务输入。
	draftInput := func(gap servermodels.KnowledgeGap) knowledgegap.DraftInput {
		return knowledgegap.DraftInput{WorkspaceID: identity.Workspace.ID, KnowledgeGapID: gap.ID, RequestedAt: gap.DraftRequestedAt}
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
	_, err = claim.Execute(ctx, identity, conversationID)
	require.NoError(t, err)
	_, err = reply.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "海外仓订单一般 3 到 5 个工作日送达",
	})
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, identity, conversationID)
	require.NoError(t, err)
	gaps := loadGaps(sessionID)
	require.Len(t, gaps, 1, "登记的待补知识")
	require.Equal(t, string(domain.KnowledgeGapSourceKnowledgeGap), gaps[0].Source)
	require.Equal(t, string(domain.KnowledgeGapStatusPending), gaps[0].Status)
	require.Equal(t, string(domain.KnowledgeGapDraftStatusPending), gaps[0].DraftStatus)
	require.NotNil(t, gaps[0].QuestionMessageID)
	gap := gaps[0]
	drafts := servertest.QueuedInputs(t, tasks, knowledgegap.DraftActionName, func(input knowledgegap.DraftInput) bool { return input.KnowledgeGapID == gap.ID })
	require.Len(t, drafts, 1, "起草任务数")

	// 小结模型按真人答复起草问答，重复执行不再调用模型。
	caller := &summaryCaller{text: `{"question":"海外仓发货需要多久？","questionIndex":0,"similarQuestions":["海外仓几天到货"," ","海外仓发货需要多久？"],"answer":"海外仓订单一般 3 到 5 个工作日送达。"}`}
	worker := servicesummary.NewWorker(db, tasks, modelcall.New(db, modelcall.Upstreams{Decider: &summaryDecider{}, Chat: caller.chat}, nil))
	require.NoError(t, worker.DraftKnowledgeGap(ctx, draftInput(gap)))
	require.NoError(t, worker.DraftKnowledgeGap(ctx, draftInput(gap)))
	require.Equal(t, 1, caller.calls, "重复起草调用次数")

	// 默认知识库取 AI 员工绑定的问答知识库。
	base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "待补知识 FAQ "+uuid.NewV7().String(), domain.KnowledgeBaseCategoryQA))
	require.NoError(t, err)
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: agentaction.ExecutionInput{
		Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, KnowledgeBaseIDs: []string{base.ID}},
	}})
	require.NoError(t, err)
	pending, err := list.Execute(ctx, identity, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusPending})
	require.NoError(t, err)
	require.Equal(t, 1, pending.Total)
	require.Len(t, pending.Gaps, 1)
	require.Equal(t, "海外仓发货需要多久？", pending.Gaps[0].Question)
	require.True(t, pending.Gaps[0].HasDraft)
	require.Equal(t, conversationID, pending.Gaps[0].ConversationID)
	detail, err := knowledgegap.NewGetQuery(db).Execute(ctx, identity, gap.ID)
	require.NoError(t, err)
	senders := arr.Map(detail.Messages, func(message knowledgegap.Message) string { return message.Sender })
	require.Equal(t, "海外仓发货要几天", detail.Question)
	require.Equal(t, string(domain.KnowledgeGapDraftStatusReady), detail.DraftStatus)
	require.NotNil(t, detail.DraftQuestion)
	require.Equal(t, []string{"海外仓几天到货"}, detail.DraftSimilarQuestions)
	require.NotNil(t, detail.DraftAnswer)
	require.Equal(t, "海外仓订单一般 3 到 5 个工作日送达。", *detail.DraftAnswer)
	require.NotNil(t, detail.QuestionMessageID)

	// 成员都能看到全部条目，没有 AI 员工管理权限时只能处理本人负责的 AI 员工的待补知识。
	var memberRoleID string
	require.NoError(t, db.NewSelect().Model((*servermodels.Role)(nil)).ColumnExpr("id::text").
		Where("workspace_id = ? AND kind = ?", identity.Workspace.ID, domain.RoleKindMember).Scan(ctx, &memberRoleID))
	memberEmail := servertest.UniqueEmail("gap-member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, identity, memberSpec{DisplayName: "普通成员", Email: memberEmail, Password: "password123", RoleID: memberRoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, identity.Workspace.ID, memberEmail, "password123").Identity
	pending, err = list.Execute(ctx, member, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusPending})
	require.NoError(t, err)
	require.Equal(t, 1, pending.Total, "不负责时的清单")
	require.False(t, pending.Gaps[0].Handleable)
	memberDetail, err := knowledgegap.NewGetQuery(db).Execute(ctx, member, gap.ID)
	require.NoError(t, err, "不负责时的详情")
	require.False(t, memberDetail.Handleable)
	require.ErrorIs(t, dismiss.Execute(ctx, member, gap.ID), knowledgegap.ErrForbidden, "不负责时忽略")
	require.ErrorIs(t, knowledgegap.NewAcceptAction(db, knowledgeaction.NewSaveQAEntryAction(db, tasks), agentevaluationaction.KnowledgeGapCases{}).Execute(ctx, member, gap.ID, knowledgegap.AcceptInput{
		KnowledgeBaseID: base.ID, QA: knowledgeaction.QAInput{Question: "海外仓发货需要多久？", Answer: "3 到 5 个工作日。"},
	}), knowledgegap.ErrForbidden, "不负责时加入知识库")
	// 拥有 AI 员工管理权限的成员可以处理全部条目。
	curatorRole, err := roleaction.NewCreateRoleAction(db).Execute(ctx, identity, roleaction.Input{Name: "知识整理", Permissions: []domain.PermissionCode{domain.PermissionAIEmployeesManage}})
	require.NoError(t, err)
	curatorEmail := servertest.UniqueEmail("gap-curator")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, identity, memberSpec{DisplayName: "知识整理成员", Email: curatorEmail, Password: "password123", RoleID: curatorRole.ID})
	require.NoError(t, err)
	curator := servertest.LoginMember(t, db, identity.Workspace.ID, curatorEmail, "password123").Identity
	pending, err = list.Execute(ctx, curator, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusPending})
	require.NoError(t, err)
	require.Equal(t, 1, pending.Total, "知识整理成员的清单")
	require.True(t, pending.Gaps[0].Handleable)
	curatorDetail, err := knowledgegap.NewGetQuery(db).Execute(ctx, curator, gap.ID)
	require.NoError(t, err)
	require.True(t, curatorDetail.Handleable)
	var previousResponsible *string
	require.NoError(t, db.NewSelect().Table("agents").Column("responsible_user_id").Where("id = ?", agent.ID).Scan(ctx, &previousResponsible))
	setResponsible := func(userID *string) {
		t.Helper()
		_, err := db.NewUpdate().Table("agents").Set("responsible_user_id = ?", userID).Where("id = ?", agent.ID).Exec(ctx)
		require.NoError(t, err)
	}
	setResponsible(&member.User.ID)
	pending, err = list.Execute(ctx, member, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusPending})
	require.NoError(t, err)
	require.Equal(t, 1, pending.Total, "负责时的清单")
	require.True(t, pending.Gaps[0].Handleable)
	memberDetail, err = knowledgegap.NewGetQuery(db).Execute(ctx, member, gap.ID)
	require.NoError(t, err)
	require.True(t, memberDetail.Handleable)
	setResponsible(previousResponsible)
	require.Equal(t, *gap.QuestionMessageID, *detail.QuestionMessageID)
	require.NotNil(t, detail.DefaultKnowledgeBaseID)
	require.Equal(t, base.ID, *detail.DefaultKnowledgeBaseID)
	require.True(t, detail.Evaluable)
	require.GreaterOrEqual(t, len(senders), 3, "发送方 = %v", senders)
	require.Equal(t, "customer", senders[0], "发送方 = %v", senders)
	require.Equal(t, "staff", senders[len(senders)-1], "发送方 = %v", senders)

	// 加入知识库后创建问答并记为已加入，不能再次加入或忽略。
	accept := knowledgegap.NewAcceptAction(db, knowledgeaction.NewSaveQAEntryAction(db, tasks), agentevaluationaction.KnowledgeGapCases{})
	acceptInput := knowledgegap.AcceptInput{KnowledgeBaseID: base.ID, AddToEvaluation: true, QA: knowledgeaction.QAInput{
		Question: *detail.DraftQuestion, Answer: *detail.DraftAnswer, SimilarQuestions: []knowledgeaction.QASimilarQuestion{{Content: detail.DraftSimilarQuestions[0]}},
	}}
	require.NoError(t, accept.Execute(ctx, identity, gap.ID, acceptInput))
	// 同时加入评测时，以待补知识的提问为用例，期望答复，标准答案为保存的问答答案，并保存客户上下文。
	evaluationCase := &servermodels.AgentEvaluationCase{}
	require.NoError(t, db.NewSelect().Model(evaluationCase).Where("aec.agent_id = ? AND aec.service_session_id = ?", agent.ID, sessionID).Scan(ctx))
	require.Equal(t, struct {
		Source         string
		Question       string
		ExpectedAction string
		ExpectedAnswer string
	}{string(domain.AgentEvaluationCaseSourceKnowledgeGap), "海外仓发货要几天", string(domain.AgentRunOutcomeReply), "海外仓订单一般 3 到 5 个工作日送达。"}, struct {
		Source         string
		Question       string
		ExpectedAction string
		ExpectedAnswer string
	}{evaluationCase.Source, evaluationCase.Question, evaluationCase.ExpectedAction, evaluationCase.ExpectedAnswer}, "加入评测的用例")
	require.NotNil(t, evaluationCase.OccurredAt)
	require.Contains(t, string(evaluationCase.Context), "customerContext")
	gap = loadGaps(sessionID)[0]
	require.Equal(t, string(domain.KnowledgeGapStatusAccepted), gap.Status)
	require.NotNil(t, gap.KnowledgeBaseID)
	require.Equal(t, base.ID, *gap.KnowledgeBaseID)
	require.NotNil(t, gap.QAEntryID)
	require.NotNil(t, gap.HandledAt)
	entry, err := knowledgeaction.NewGetQAEntryQuery(db).Execute(ctx, identity, base.ID, *gap.QAEntryID)
	require.NoError(t, err)
	require.Equal(t, "海外仓发货需要多久？", entry.Question)
	require.Len(t, entry.SimilarQuestions, 1)
	require.ErrorIs(t, accept.Execute(ctx, identity, gap.ID, acceptInput), knowledgegap.ErrHandled, "再次加入")
	require.ErrorIs(t, dismiss.Execute(ctx, identity, gap.ID), knowledgegap.ErrHandled, "忽略已加入的条目")
	// 已处理的周期重开再关闭时，同一转人工事件不再登记。
	_, err = reopen.Execute(ctx, identity, conversationID)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, identity, conversationID)
	require.NoError(t, err)
	require.Len(t, loadGaps(sessionID), 1, "已处理周期再次关闭后的待补知识")

	// 重开保留待处理条目，再次关闭时重新起草，旧的起草任务不再写入。
	second, secondSessionID := handOff("海外仓能发哪些国家")
	_, err = claim.Execute(ctx, identity, second)
	require.NoError(t, err)
	_, err = closeSession.Execute(ctx, identity, second)
	require.NoError(t, err)
	first := loadGaps(secondSessionID)[0]
	_, err = reopen.Execute(ctx, identity, second)
	require.NoError(t, err)
	kept := loadGaps(secondSessionID)
	require.Len(t, kept, 1, "重开后的待补知识")
	require.Equal(t, first.ID, kept[0].ID)
	require.Equal(t, string(domain.KnowledgeGapStatusPending), kept[0].Status)
	_, err = closeSession.Execute(ctx, identity, second)
	require.NoError(t, err)
	redrafted := loadGaps(secondSessionID)
	require.Len(t, redrafted, 1, "再次关闭后的待补知识")
	require.Equal(t, first.ID, redrafted[0].ID)
	require.Equal(t, string(domain.KnowledgeGapDraftStatusPending), redrafted[0].DraftStatus)
	require.True(t, redrafted[0].DraftRequestedAt.After(first.DraftRequestedAt), "再次关闭后的待补知识 = %+v", redrafted)
	staleCaller := &summaryCaller{text: `{"question":"旧问题","answer":""}`}
	stale := servicesummary.NewWorker(db, tasks, modelcall.New(db, modelcall.Upstreams{Decider: &summaryDecider{}, Chat: staleCaller.chat}, nil))
	require.NoError(t, stale.DraftKnowledgeGap(ctx, draftInput(first)))
	require.Zero(t, staleCaller.calls, "旧起草任务调用次数")
	require.NoError(t, stale.FinalizeKnowledgeGapDraftFailure(ctx, draftInput(first), errors.New("旧任务失败")))
	require.Equal(t, string(domain.KnowledgeGapDraftStatusPending), loadGaps(secondSessionID)[0].DraftStatus, "旧任务失败后的草稿状态")
	require.NoError(t, stale.FinalizeKnowledgeGapDraftFailure(ctx, draftInput(redrafted[0]), errors.New("起草失败")))
	require.Equal(t, string(domain.KnowledgeGapDraftStatusFailed), loadGaps(secondSessionID)[0].DraftStatus, "起草失败后的草稿状态")

	// 并入已有问答时追加相似问题并保留原有内容编号。
	require.NoError(t, accept.Execute(ctx, identity, first.ID, knowledgegap.AcceptInput{KnowledgeBaseID: base.ID, EntryID: entry.ID, QA: knowledgeaction.QAInput{
		Question: entry.Question, Answer: entry.Answer,
		SimilarQuestions: append(entry.SimilarQuestions, knowledgeaction.QASimilarQuestion{Content: "海外仓能发哪些国家"}),
	}}))
	updated, err := knowledgeaction.NewGetQAEntryQuery(db).Execute(ctx, identity, base.ID, entry.ID)
	require.NoError(t, err)
	require.Len(t, updated.SimilarQuestions, 2)
	require.Equal(t, entry.SimilarQuestions[0].ID, updated.SimilarQuestions[0].ID)

	// AI 员工关闭的周期被访客评价未解决时登记复核条目，起草时按 AI 判断更新客户提问消息。
	resolvedInput := visitorInput(channelID, "")
	resolved := f.receive(t, &resolvedInput, "你好")
	f.receive(t, &resolvedInput, "退货运费谁承担")
	resolveRun := f.executeQueuedRun(t, resolved.Conversation.ID, resolutionRuntime("由我们承担", agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}, nil, nil))
	_, err = customerchataction.NewRateWebsiteServiceSessionAction(db, tasks).Execute(ctx, customerchataction.WebsiteServiceSessionRatingInput{
		ChannelID: channelID, ExternalID: resolvedInput.ExternalID, ConversationID: resolved.Conversation.ID, ServiceSessionID: resolveRun.ScopeID,
	})
	require.NoError(t, err)
	gaps = loadGaps(resolveRun.ScopeID)
	require.Len(t, gaps, 1, "评价未解决的待补知识")
	require.Equal(t, string(domain.KnowledgeGapSourceRatedUnresolved), gaps[0].Source)
	require.NotNil(t, gaps[0].QuestionMessageID)
	rated := gaps[0]
	greetingID := *rated.QuestionMessageID
	reviewCaller := &summaryCaller{text: `{"summary":"客户询问退货运费。","question":"退货运费由谁承担？","questionIndex":1,"similarQuestions":[],"answer":""}`}
	reviewer := servicesummary.NewWorker(db, tasks, modelcall.New(db, modelcall.Upstreams{Decider: &summaryDecider{answers: map[string]decision.Answer{
		"ai_incorrect": {Kind: decision.KindYesNo, Probability: 0.9},
	}}, Chat: reviewCaller.chat}, nil))
	require.NoError(t, reviewer.DraftKnowledgeGap(ctx, draftInput(rated)))
	rated = loadGaps(resolveRun.ScopeID)[0]
	require.Equal(t, string(domain.KnowledgeGapDraftStatusReady), rated.DraftStatus)
	require.NotNil(t, rated.DraftAnswer)
	require.Empty(t, *rated.DraftAnswer)
	require.NotNil(t, rated.QuestionMessageID)
	require.NotEqual(t, greetingID, *rated.QuestionMessageID)
	total, err := knowledgegap.PendingCount(ctx, db, identity.Workspace.ID, knowledgegap.Scope{ChannelID: channelID})
	require.NoError(t, err)
	require.Equal(t, 1, total, "待处理条数")
	require.NoError(t, dismiss.Execute(ctx, identity, rated.ID))

	// 已处理的周期出现新的触发事件时再次登记：质检认为 AI 答复可能有误。
	require.NoError(t, reviewer.Review(ctx, loadReviewInput(t, resolveRun.ScopeID, tasks, testEnqueuer)))
	gaps = loadGaps(resolveRun.ScopeID)
	require.Len(t, gaps, 2, "可能答错的待补知识")
	require.Equal(t, string(domain.KnowledgeGapSourcePossiblyWrong), gaps[1].Source)
	require.Equal(t, string(domain.KnowledgeGapStatusPending), gaps[1].Status)
	require.NotEqual(t, rated.TriggerMessageID, gaps[1].TriggerMessageID)
	dismissed, err := list.Execute(ctx, identity, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusDismissed})
	require.NoError(t, err)
	require.Equal(t, 1, dismissed.Total)
	require.Equal(t, rated.ID, dismissed.Gaps[0].ID)
	require.Equal(t, "退货运费由谁承担？", dismissed.Gaps[0].Question)
	require.True(t, dismissed.Gaps[0].HasDraft)
	accepted, err := list.Execute(ctx, identity, knowledgegap.ListInput{Scope: knowledgegap.Scope{ChannelID: channelID}, Status: domain.KnowledgeGapStatusAccepted})
	require.NoError(t, err)
	require.Equal(t, 2, accepted.Total, "已加入清单")
}
