//go:build server

package integrationtest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentevaluation "github.com/runforyou-ai/luway/internal/actions/agentevaluation"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/decision"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// evaluationReplayer 按提问返回预设的回放结果，并记录收到的回放输入。
type evaluationReplayer struct {
	mu      sync.Mutex
	results map[string]agentruntime.RunResult
	errors  map[string]error
	inputs  []agentrunaction.ReplayInput
}

// Replay 返回提问对应的预设结果或错误。
func (r *evaluationReplayer) Replay(_ context.Context, input agentrunaction.ReplayInput) (agentruntime.RunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputs = append(r.inputs, input)
	question := input.Messages[len(input.Messages)-1].Content
	return r.results[question], r.errors[question]
}

// evaluationDecider 按 AI 回复返回预设的符合概率，并记录判定次数。
type evaluationDecider struct {
	mu            sync.Mutex
	probabilities map[string]float64
	calls         int
}

// Decide 返回回复对应的符合概率。
func (d *evaluationDecider) Decide(_ context.Context, _ decision.Credential, _ string, state any, _ map[string]decision.Question) (map[string]decision.Answer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	reply := state.(map[string]any)["reply"].(string)
	return map[string]decision.Answer{"correct": {Kind: decision.KindYesNo, Probability: d.probabilities[reply]}}, nil
}

// TestAgentEvaluation 验证评测用例的校验与版本、发起运行的前置条件、按处理方式与判断模型判定、评测异常不计入通过、单条重跑不改变运行计数，以及新失败只标记快照版本未变的用例。
func TestAgentEvaluation(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "评测客服",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: modelID, SystemInstruction: "回答退款问题",
		}},
	})
	require.NoError(t, err)
	createCase := agentevaluation.NewCreateCaseAction(db)
	updateCase := agentevaluation.NewUpdateCaseAction(db)
	startRun := agentevaluation.NewStartRunAction(db, tasks)
	rerun := agentevaluation.NewRerunCaseAction(db, tasks)
	overview := agentevaluation.NewOverviewQuery(db)
	detail := agentevaluation.NewCaseDetailQuery(db)
	replayer := &evaluationReplayer{results: map[string]agentruntime.RunResult{}, errors: map[string]error{}}
	decider := &evaluationDecider{probabilities: map[string]float64{}}
	worker := agentevaluation.NewWorker(db, replayer, modelcall.New(db, modelcall.Upstreams{Decider: decider}, nil))

	// 期望答复却没有标准答案、服务对象不属于 AI 员工时拒绝保存。
	_, err = createCase.Execute(ctx, identity, agent.ID, agentevaluation.CaseInput{
		Audience: domain.ServiceAudienceEmployee, Question: "如何退款", ExpectedAction: domain.AgentRunOutcomeReply,
	})
	fieldError, ok := errors.AsType[*common.FieldError](err)
	require.True(t, ok, "invalid case err = %v", err)
	require.Equal(t, agentevaluation.ValidationAudienceInvalid, fieldError.Fields["audience"])
	require.Equal(t, agentevaluation.ValidationExpectedAnswerRequired, fieldError.Fields["expectedAnswer"])

	// 未设置判断模型或没有用例时不能发起运行。
	_, err = startRun.Execute(ctx, identity, agent.ID)
	require.ErrorIs(t, err, agentevaluation.ErrDecisionModelUnavailable, "start without decision model")
	summaryProviderID := seedSummaryModels(t, db, identity)
	_, err = customerservice.NewUpdateServiceSummarySettingsAction(db).Execute(ctx, identity, domain.ServiceSummarySettings{
		DecisionModelID: new(aiModelID(t, db, summaryProviderID, "decision-model")),
		Locale:          domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)
	_, err = startRun.Execute(ctx, identity, agent.ID)
	require.ErrorIs(t, err, agentevaluation.ErrNoCases, "start without cases")

	// newCase 保存一条客户提问用例；期望转人工时标准答案被清空。
	newCase := func(question string, action domain.AgentRunOutcome, answer string) *agentevaluation.Case {
		t.Helper()
		created, err := createCase.Execute(ctx, identity, agent.ID, agentevaluation.CaseInput{
			Audience: domain.ServiceAudienceCustomer, Question: question, ExpectedAction: action, ExpectedAnswer: answer,
		})
		require.NoError(t, err)
		return created
	}
	edited := newCase("怎么退款", domain.AgentRunOutcomeReply, "签收后七天内可以申请退款")
	handoff := newCase("我要投诉", domain.AgentRunOutcomeHandoff, "不会保存")
	broken := newCase("发票怎么开", domain.AgentRunOutcomeReply, "下单时选择开具电子发票")
	stable := newCase("运费谁出", domain.AgentRunOutcomeResolve, "质量问题由商家承担运费")
	require.Empty(t, handoff.ExpectedAnswer)
	require.Equal(t, 1, edited.Version)
	replayer.results["怎么退款"] = agentruntime.RunResult{Content: "签收后七天内可以退款"}
	replayer.results["我要投诉"] = agentruntime.RunResult{Content: "请描述问题"}
	replayer.errors["发票怎么开"] = errors.New("model unavailable")
	replayer.results["运费谁出"] = agentruntime.RunResult{Content: "质量问题运费由我们承担", Decision: agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}}
	decider.probabilities["签收后七天内可以退款"] = 0.9
	decider.probabilities["质量问题运费由我们承担"] = 0.95

	// runAll 发起一次运行并同步执行它的全部首次尝试，返回运行编号。
	runAll := func() string {
		t.Helper()
		runID, err := startRun.Execute(ctx, identity, agent.ID)
		require.NoError(t, err)
		results := pendingEvaluationResults(t, db, runID)
		require.Len(t, results, 4, "pending results")
		_, err = startRun.Execute(ctx, identity, agent.ID)
		require.ErrorIs(t, err, agentevaluation.ErrRunInProgress, "start while running")
		for _, result := range results {
			require.NoError(t, worker.Evaluate(ctx, agentevaluation.EvaluateInput{WorkspaceID: identity.Workspace.ID, ResultID: result.ID}))
		}
		return runID
	}
	firstRunID := runAll()
	first, err := overview.Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	// 处理方式与期望不一致时不调用判断模型，回放失败记为异常且不计入通过或未通过。
	latest := first.Latest
	require.NotNil(t, latest)
	require.Equal(t, domain.AgentEvaluationRunStatusCompleted, latest.Status)
	require.Equal(t, 2, latest.Passed)
	require.Equal(t, 1, latest.Failed)
	require.Equal(t, 1, latest.Errors)
	require.Equal(t, 4, latest.Finished)
	require.Equal(t, 2, decider.calls)
	statuses := evaluationStatuses(first)
	require.Equal(t, domain.AgentEvaluationResultStatusPassed, statuses[edited.ID])
	require.Equal(t, domain.AgentEvaluationResultStatusFailed, statuses[handoff.ID])
	require.Equal(t, domain.AgentEvaluationResultStatusError, statuses[broken.ID])
	require.Equal(t, domain.AgentEvaluationResultStatusPassed, statuses[stable.ID])
	require.False(t, first.ConfigurationChanged)
	require.True(t, first.DecisionModelReady)
	require.Nil(t, first.Previous)
	// 回放使用运行发起时生效的配置版本与客户提问。
	input := replayer.inputs[0]
	require.Equal(t, agent.Execution.RevisionID, input.RevisionID)
	require.Equal(t, domain.ServiceAudienceCustomer, input.Audience)
	require.Len(t, input.Messages, 1)

	// 单条重跑新增尝试并展示在详情中，不改变原运行的计数。
	replayer.errors["发票怎么开"] = nil
	replayer.results["发票怎么开"] = agentruntime.RunResult{Content: "下单时可以选择电子发票"}
	decider.probabilities["下单时可以选择电子发票"] = 0.85
	require.NoError(t, rerun.Execute(ctx, identity, agent.ID, broken.ID))
	require.ErrorIs(t, rerun.Execute(ctx, identity, agent.ID, broken.ID), agentevaluation.ErrRunInProgress, "rerun while pending")
	for _, result := range pendingEvaluationResults(t, db, firstRunID) {
		require.NoError(t, worker.Evaluate(ctx, agentevaluation.EvaluateInput{WorkspaceID: identity.Workspace.ID, ResultID: result.ID}))
	}
	brokenDetail, err := detail.Execute(ctx, identity, agent.ID, broken.ID)
	require.NoError(t, err)
	require.Len(t, brokenDetail.Attempts, 2)
	require.Equal(t, domain.AgentEvaluationResultStatusError, brokenDetail.Attempts[0].Status)
	require.Equal(t, domain.AgentEvaluationResultStatusPassed, brokenDetail.Attempts[1].Status)
	require.Equal(t, "发票怎么开", brokenDetail.Attempts[1].Snapshot.Question)
	again, err := overview.Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	require.Equal(t, 2, again.Latest.Passed)
	require.Equal(t, 1, again.Latest.Errors)
	// 列表在首次结果之外给出最近一次重跑的结果。
	for _, row := range again.Cases {
		if row.ID == broken.ID {
			require.NotNil(t, row.RerunStatus, "broken row after rerun")
			require.Equal(t, domain.AgentEvaluationResultStatusPassed, *row.RerunStatus)
			require.Equal(t, domain.AgentEvaluationResultStatusError, *row.LatestStatus)
		}
	}

	// 修改用例递增版本；只有快照版本未变且由通过变为未通过的用例标记新失败。
	updated, err := updateCase.Execute(ctx, identity, agent.ID, edited.ID, agentevaluation.CaseInput{
		Audience: domain.ServiceAudienceCustomer, Question: "怎么退款", ExpectedAction: domain.AgentRunOutcomeReply, ExpectedAnswer: "签收后十五天内可以申请退款",
	})
	require.NoError(t, err)
	require.Equal(t, 2, updated.Version)
	// 修改后的用例标记为已修改，不能再重跑旧快照；已删除的用例同样不能重跑。
	modified, err := overview.Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	require.True(t, modifiedCase(modified, edited.ID), "overview after case edit = %+v", modified)
	require.False(t, modifiedCase(modified, stable.ID), "overview after case edit = %+v", modified)
	require.ErrorIs(t, rerun.Execute(ctx, identity, agent.ID, edited.ID), agentevaluation.ErrCaseChanged, "rerun modified case")
	removed := newCase("临时用例", domain.AgentRunOutcomeHandoff, "")
	require.NoError(t, agentevaluation.NewDeleteCaseAction(db).Execute(ctx, identity, agent.ID, removed.ID))
	require.ErrorIs(t, rerun.Execute(ctx, identity, agent.ID, removed.ID), agentevaluation.ErrCaseNotFound, "rerun deleted case")
	decider.probabilities["签收后七天内可以退款"] = 0.1
	decider.probabilities["质量问题运费由我们承担"] = 0.2
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: modelID, SystemInstruction: "回答退款与运费问题",
		}},
	})
	require.NoError(t, err)
	changed, err := overview.Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	require.True(t, changed.ConfigurationChanged, "overview after execution change")
	runAll()
	second, err := overview.Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	newFailures := make([]string, 0)
	for _, row := range second.Cases {
		if row.NewFailure {
			newFailures = append(newFailures, row.ID)
		}
	}
	require.Equal(t, []string{stable.ID}, newFailures)
	require.False(t, second.ConfigurationChanged)
	require.NotNil(t, second.Previous)
	require.Equal(t, 2, second.Previous.Passed)
}

// TestAgentEvaluationReplay 验证回放使用指定配置版本解析服务场景的有效配置，只以给定消息为输入，且不写入会话、消息和运行记录。
func TestAgentEvaluationReplay(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee}, DisplayName: "回放客服",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: modelID, SystemInstruction: "第一版指令",
		}},
	})
	require.NoError(t, err)
	firstRevision := agent.Execution.RevisionID
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: modelID, SystemInstruction: "第二版指令",
		}},
	})
	require.NoError(t, err)
	runtime := &replayProbeRuntime{}
	execute := newTestAgentRun(db, testEnqueuer, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	messages := []agentcontract.Message{{ID: "question", Role: agentcontract.MessageRoleUser, Content: "怎么退款"}}
	counts := replayWriteCounts(t, db, identity.Workspace.ID)

	// 客户提问按客户服务场景解析，指令来自指定的旧配置版本，并带终止工具。
	result, err := execute.Replay(ctx, agentrunaction.ReplayInput{
		ReplayID: "0197d5f8-0000-7000-8000-000000000001", WorkspaceID: identity.Workspace.ID, AgentID: agent.ID, RevisionID: firstRevision,
		Audience: domain.ServiceAudienceCustomer, Messages: messages,
	})
	require.NoError(t, err)
	require.Equal(t, "回放回复", result.Content)
	assignment := runtime.request.Assignment
	require.Equal(t, agentruntime.SceneCustomer, assignment.Scene)
	require.Equal(t, agentruntime.GroundingStrict, assignment.Grounding)
	require.Contains(t, assignment.Tools, "handoff_to_human")
	require.Contains(t, assignment.Instruction, "第一版指令")
	require.Len(t, runtime.claimed.Messages, 1)
	require.Equal(t, "怎么退款", runtime.claimed.Messages[0].Content)
	// 员工提问按员工服务场景解析。
	_, err = execute.Replay(ctx, agentrunaction.ReplayInput{
		ReplayID: "0197d5f8-0000-7000-8000-000000000002", WorkspaceID: identity.Workspace.ID, AgentID: agent.ID, RevisionID: firstRevision,
		Audience: domain.ServiceAudienceEmployee, Messages: messages,
	})
	require.NoError(t, err)
	require.Equal(t, agentruntime.SceneEmployeeService, runtime.request.Assignment.Scene)
	require.Equal(t, counts, replayWriteCounts(t, db, identity.Workspace.ID), "replay wrote records")
	// 不属于该 AI 员工的配置版本不可回放。
	_, err = execute.Replay(ctx, agentrunaction.ReplayInput{
		ReplayID: "0197d5f8-0000-7000-8000-000000000003", WorkspaceID: identity.Workspace.ID, AgentID: agent.ID, RevisionID: "0197d5f8-0000-7000-8000-000000000009",
		Audience: domain.ServiceAudienceCustomer, Messages: messages,
	})
	require.ErrorIs(t, err, agentrunaction.ErrReplayConfigurationUnavailable, "unknown revision")
}

// replayProbeRuntime 认领回放输入并记录运行请求，返回固定回复。
type replayProbeRuntime struct {
	request agentruntime.RunRequest
	claimed einorun.Claim
}

// Run 认领唯一输入并确认没有后续输入。
func (r *replayProbeRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
	r.request = request
	triggers, err := pendingTriggers(ctx, feed, 0)
	if err != nil || len(triggers) != 1 {
		return agentruntime.RunResult{}, errors.New("replay feed must provide one input")
	}
	if r.claimed, err = feed.Claim(ctx, triggers[0].Seq); err != nil {
		return agentruntime.RunResult{}, err
	}
	if next, err := pendingTriggers(ctx, feed, r.claimed.EndSeq); err != nil || len(next) != 0 {
		return agentruntime.RunResult{}, errors.New("replay feed must not provide further inputs")
	}
	return agentruntime.RunResult{Content: "回放回复", EndSeq: r.claimed.EndSeq}, nil
}

// replayCounts 是工作区内会话、消息与运行记录的数量。
type replayCounts struct {
	Conversations int `bun:"conversations"`
	Messages      int `bun:"messages"`
	Runs          int `bun:"runs"`
}

// replayWriteCounts 读取工作区内会话、消息与运行记录的数量。
func replayWriteCounts(t *testing.T, db *bun.DB, workspaceID string) replayCounts {
	t.Helper()
	counts := replayCounts{}
	require.NoError(t, db.NewSelect().
		ColumnExpr("(SELECT count(*) FROM conversations WHERE workspace_id = ?) AS conversations", workspaceID).
		ColumnExpr("(SELECT count(*) FROM messages WHERE workspace_id = ?) AS messages", workspaceID).
		ColumnExpr("(SELECT count(*) FROM agent_runs WHERE workspace_id = ?) AS runs", workspaceID).
		Scan(context.Background(), &counts))
	return counts
}

// pendingEvaluationResults 读取运行中尚未结束的尝试。
func pendingEvaluationResults(t *testing.T, db *bun.DB, runID string) []servermodels.AgentEvaluationResult {
	t.Helper()
	results := make([]servermodels.AgentEvaluationResult, 0)
	require.NoError(t, db.NewSelect().Model(&results).
		Where("aers.run_id = ? AND aers.status = ?", runID, domain.AgentEvaluationResultStatusPending).
		OrderExpr("aers.created_at, aers.id").Scan(context.Background()))
	return results
}

// modifiedCase 判断评测页中的用例是否标记为最近一次运行后已修改。
func modifiedCase(overview *agentevaluation.Overview, caseID string) bool {
	for _, row := range overview.Cases {
		if row.ID == caseID {
			return row.Modified
		}
	}
	return false
}

// evaluationStatuses 返回评测页各用例在最近一次运行中的结果。
func evaluationStatuses(overview *agentevaluation.Overview) map[string]domain.AgentEvaluationResultStatus {
	statuses := make(map[string]domain.AgentEvaluationResultStatus, len(overview.Cases))
	for _, row := range overview.Cases {
		if row.LatestStatus != nil {
			statuses[row.ID] = *row.LatestStatus
		}
	}
	return statuses
}

// replyRuntime 认领全部输入后以固定正文答复。
func replyRuntime(content string) *testAgentRuntime {
	return &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Content: content, EndSeq: claimed.EndSeq}, nil
	}}
}

// TestAgentEvaluationCapturedCases 验证应转人工未转的问题会话按所选客户消息截取前文与客户上下文加入评测，只接受被质检标记的周期与提问人的文字消息，回放按快照组装上下文并限定客户历史检索。
func TestAgentEvaluationCapturedCases(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	agent := f.newAgent(t, "评测来源客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})

	// 访客连续提问，AI 员工每次都直接答复，最后一次应当转人工却没有转。
	input := visitorInput(channelID, "")
	var conversationID string
	for _, exchange := range [][2]string{{"我要退货", "请提供订单号"}, {"订单号 123", "已为你查询到订单"}, {"我要找人工", "请描述你的问题"}} {
		conversationID = f.receive(t, &input, exchange[0]).Conversation.ID
		f.executeQueuedRun(t, conversationID, replyRuntime(exchange[1]))
	}
	session := loadSummarySession(t, db, conversationID)
	messageID := func(body string) string {
		t.Helper()
		var id string
		require.NoError(t, db.NewSelect().Table("messages").Column("id").Where("conversation_id = ? AND body = ?", conversationID, body).Scan(ctx, &id))
		return id
	}
	add := agentevaluation.NewAddServiceSessionCaseAction(db)

	// 未被质检标记为应转人工未转的周期不能加入。
	_, err := add.Execute(ctx, identity, session.ID, messageID("我要找人工"))
	require.ErrorIs(t, err, agentevaluation.ErrServiceSessionNotFound, "add unflagged session")
	_, err = db.NewInsert().Model(&servermodels.ServiceSessionReview{
		WorkspaceID: identity.Workspace.ID, ServiceSessionID: session.ID, ClosedAt: session.CreatedAt, AIMissedHandoff: new(true),
	}).Column("workspace_id", "service_session_id", "closed_at", "ai_missed_handoff").Exec(ctx)
	require.NoError(t, err)
	// 提问只能是提问人发送的文字消息。
	_, err = add.Execute(ctx, identity, session.ID, messageID("已为你查询到订单"))
	require.ErrorIs(t, err, agentevaluation.ErrQuestionNotFound, "add agent message")
	created, err := add.Execute(ctx, identity, session.ID, messageID("我要找人工"))
	require.NoError(t, err)
	senders := arr.Map(created.Context.Messages, func(message agentevaluation.ContextMessage) string { return message.Sender + ":" + message.Body })
	require.Equal(t, domain.AgentEvaluationCaseSourceServiceSession, created.Source)
	require.Equal(t, domain.AgentRunOutcomeHandoff, created.ExpectedAction)
	require.Empty(t, created.ExpectedAnswer)
	require.Equal(t, "我要找人工", created.Question)
	require.Equal(t, agent.ID, created.AgentID)
	require.NotNil(t, created.OccurredAt)
	require.NotNil(t, created.ServiceSessionID)
	require.NotEmpty(t, created.Context.CustomerContext)
	require.Nil(t, created.Context.Customer)
	require.True(t, created.Context.CustomerHistory)
	require.Equal(t, []string{"customer:我要退货", "ai:请提供订单号", "customer:订单号 123", "ai:已为你查询到订单"}, senders)

	// 同一提问再次加入时返回已有用例。
	again, err := add.Execute(ctx, identity, session.ID, messageID("我要找人工"))
	require.NoError(t, err)
	require.Equal(t, created.ID, again.ID)

	// 同一提问以不同的期望处理方式加入时拒绝，已有用例保持不变。
	questionID := messageID("我要找人工")
	require.ErrorIs(t, db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return agentevaluation.KnowledgeGapCases{}.AddFromKnowledgeGap(ctx, tx, identity,
			&servermodels.KnowledgeGap{ServiceSessionID: session.ID, QuestionMessageID: &questionID}, "请联系人工客服")
	}), agentevaluation.ErrQuestionAlreadyEvaluated, "add same question with different expectation err")

	// 回放以客户上下文开头，前文按发送方区分角色，客户历史检索只看提问之前关闭的周期。
	summaryProviderID := seedSummaryModels(t, db, identity)
	_, err = customerservice.NewUpdateServiceSummarySettingsAction(db).Execute(ctx, identity, domain.ServiceSummarySettings{
		DecisionModelID: new(aiModelID(t, db, summaryProviderID, "decision-model")), Locale: domain.LocaleChineseSimplified,
	})
	require.NoError(t, err)
	runID, err := agentevaluation.NewStartRunAction(db, tasks).Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	replayer := &evaluationReplayer{results: map[string]agentruntime.RunResult{
		"我要找人工": {Decision: agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeHandoff, Reason: domain.AgentHandoffReasonCustomerRequested}},
	}, errors: map[string]error{}}
	worker := agentevaluation.NewWorker(db, replayer, modelcall.New(db, modelcall.Upstreams{Decider: &evaluationDecider{probabilities: map[string]float64{}}}, nil))
	for _, result := range pendingEvaluationResults(t, db, runID) {
		require.NoError(t, worker.Evaluate(ctx, agentevaluation.EvaluateInput{WorkspaceID: identity.Workspace.ID, ResultID: result.ID}))
	}
	replay := replayer.inputs[0]
	roles := arr.Map(replay.Messages, func(message agentcontract.Message) agentcontract.MessageRole { return message.Role })
	require.Equal(t, created.Context.CustomerContext, replay.Messages[0].Content)
	require.Equal(t, "我要找人工", replay.Messages[len(replay.Messages)-1].Content)
	require.Equal(t, []agentcontract.MessageRole{
		agentcontract.MessageRoleUser, agentcontract.MessageRoleUser, agentcontract.MessageRoleAssistant,
		agentcontract.MessageRoleUser, agentcontract.MessageRoleAssistant, agentcontract.MessageRoleUser,
	}, roles)
	require.NotNil(t, replay.History)
	require.Equal(t, session.ID, replay.History.ServiceSessionID)
	require.True(t, replay.History.ClosedBefore.Equal(*created.OccurredAt), "closed before = %v, occurred at = %v", replay.History.ClosedBefore, *created.OccurredAt)
	require.Nil(t, replay.Customer)
	evaluationOverview, err := agentevaluation.NewOverviewQuery(db).Execute(ctx, identity, agent.ID)
	require.NoError(t, err)
	require.Equal(t, 1, evaluationOverview.Latest.Passed)
}
