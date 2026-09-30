//go:build server

package integrationtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	agentaction "github.com/runforyou-ai/cervi/internal/actions/agent"
	agentevaluation "github.com/runforyou-ai/cervi/internal/actions/agentevaluation"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	"github.com/runforyou-ai/cervi/internal/actions/customerservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/runforyou-ai/cervi/internal/integration/decision"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
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
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentevaluation.EvaluateActionName, func(context.Context, agentevaluation.EvaluateInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "评测客服",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "回答退款问题",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	createCase := agentevaluation.NewCreateCaseAction(db)
	updateCase := agentevaluation.NewUpdateCaseAction(db)
	startRun := agentevaluation.NewStartRunAction(db, tasks)
	rerun := agentevaluation.NewRerunCaseAction(db, tasks)
	overview := agentevaluation.NewOverviewQuery(db)
	detail := agentevaluation.NewCaseDetailQuery(db)
	replayer := &evaluationReplayer{results: map[string]agentruntime.RunResult{}, errors: map[string]error{}}
	decider := &evaluationDecider{probabilities: map[string]float64{}}
	worker := agentevaluation.NewWorker(db, replayer, decider)

	// 提问为空、期望答复却没有标准答案、服务对象不属于 AI 员工时拒绝保存。
	_, err = createCase.Execute(ctx, identity, agent.ID, agentevaluation.CaseInput{
		Audience: domain.ServiceAudienceEmployee, Question: " ", ExpectedAction: domain.AgentRunOutcomeReply,
	})
	if fieldError, ok := errors.AsType[*common.FieldError](err); !ok ||
		fieldError.Fields["audience"] != agentevaluation.ValidationAudienceInvalid ||
		fieldError.Fields["question"] != agentevaluation.ValidationQuestionRequired ||
		fieldError.Fields["expectedAnswer"] != agentevaluation.ValidationExpectedAnswerRequired {
		t.Fatalf("invalid case err = %v", err)
	}

	// 未设置判断模型或没有用例时不能发起运行。
	if _, err := startRun.Execute(ctx, identity, agent.ID); !errors.Is(err, agentevaluation.ErrDecisionModelUnavailable) {
		t.Fatalf("start without decision model err = %v", err)
	}
	summaryProviderID := seedSummaryModels(t, db, identity)
	if _, err := customerservice.NewUpdateServiceSummarySettingsAction(db).Execute(ctx, identity, domain.ServiceSummarySettings{
		Decision: &domain.AIModelReference{ProviderID: summaryProviderID, ModelIdentifier: "decision-model"},
		Locale:   domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := startRun.Execute(ctx, identity, agent.ID); !errors.Is(err, agentevaluation.ErrNoCases) {
		t.Fatalf("start without cases err = %v", err)
	}

	// newCase 保存一条客户提问用例；期望转人工时标准答案被清空。
	newCase := func(question string, action domain.AgentRunOutcome, answer string) *agentevaluation.Case {
		t.Helper()
		created, err := createCase.Execute(ctx, identity, agent.ID, agentevaluation.CaseInput{
			Audience: domain.ServiceAudienceCustomer, Question: question, ExpectedAction: action, ExpectedAnswer: answer,
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	edited := newCase("怎么退款", domain.AgentRunOutcomeReply, "签收后七天内可以申请退款")
	handoff := newCase("我要投诉", domain.AgentRunOutcomeHandoff, "不会保存")
	broken := newCase("发票怎么开", domain.AgentRunOutcomeReply, "下单时选择开具电子发票")
	stable := newCase("运费谁出", domain.AgentRunOutcomeResolve, "质量问题由商家承担运费")
	if handoff.ExpectedAnswer != "" || edited.Version != 1 {
		t.Fatalf("created cases = %+v %+v", handoff, edited)
	}
	replayer.results["怎么退款"] = agentruntime.RunResult{Content: "签收后七天内可以退款"}
	replayer.results["我要投诉"] = agentruntime.RunResult{Content: "请描述问题"}
	replayer.errors["发票怎么开"] = errors.New("model unavailable")
	replayer.results["运费谁出"] = agentruntime.RunResult{Content: "质量问题运费由我们承担", Decision: agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeResolve}}
	decider.probabilities["签收后七天内可以退款"] = 0.9
	decider.probabilities["质量问题运费由我们承担"] = 0.95

	// runAll 发起一次运行并同步执行它的全部首次尝试，返回运行编号。
	runAll := func() string {
		t.Helper()
		runID, err := startRun.Execute(ctx, identity, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		results := pendingEvaluationResults(t, db, runID)
		if len(results) != 4 {
			t.Fatalf("pending results = %d", len(results))
		}
		if _, err := startRun.Execute(ctx, identity, agent.ID); !errors.Is(err, agentevaluation.ErrRunInProgress) {
			t.Fatalf("start while running err = %v", err)
		}
		for _, result := range results {
			if err := worker.Evaluate(ctx, agentevaluation.EvaluateInput{OrganizationID: identity.Organization.ID, ResultID: result.ID}); err != nil {
				t.Fatal(err)
			}
		}
		return runID
	}
	firstRunID := runAll()
	first, err := overview.Execute(ctx, identity, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 处理方式与期望不一致时不调用判断模型，回放失败记为异常且不计入通过或未通过。
	if latest := first.Latest; latest == nil || latest.Status != domain.AgentEvaluationRunStatusCompleted ||
		latest.Passed != 2 || latest.Failed != 1 || latest.Errors != 1 || latest.Finished != 4 || decider.calls != 2 {
		t.Fatalf("first run = %+v decider calls = %d", first.Latest, decider.calls)
	}
	statuses := evaluationStatuses(first)
	if statuses[edited.ID] != domain.AgentEvaluationResultStatusPassed || statuses[handoff.ID] != domain.AgentEvaluationResultStatusFailed ||
		statuses[broken.ID] != domain.AgentEvaluationResultStatusError || statuses[stable.ID] != domain.AgentEvaluationResultStatusPassed ||
		first.ConfigurationChanged || !first.DecisionModelReady || first.Previous != nil {
		t.Fatalf("first overview = %+v statuses = %v", first, statuses)
	}
	// 回放使用运行发起时生效的配置版本与客户提问。
	input := replayer.inputs[0]
	if input.RevisionID != agent.Execution.RevisionID || input.Audience != domain.ServiceAudienceCustomer || len(input.Messages) != 1 {
		t.Fatalf("replay input = %+v", input)
	}

	// 单条重跑新增尝试并展示在详情中，不改变原运行的计数。
	replayer.errors["发票怎么开"] = nil
	replayer.results["发票怎么开"] = agentruntime.RunResult{Content: "下单时可以选择电子发票"}
	decider.probabilities["下单时可以选择电子发票"] = 0.85
	if err := rerun.Execute(ctx, identity, agent.ID, broken.ID); err != nil {
		t.Fatal(err)
	}
	if err := rerun.Execute(ctx, identity, agent.ID, broken.ID); !errors.Is(err, agentevaluation.ErrRunInProgress) {
		t.Fatalf("rerun while pending err = %v", err)
	}
	for _, result := range pendingEvaluationResults(t, db, firstRunID) {
		if err := worker.Evaluate(ctx, agentevaluation.EvaluateInput{OrganizationID: identity.Organization.ID, ResultID: result.ID}); err != nil {
			t.Fatal(err)
		}
	}
	brokenDetail, err := detail.Execute(ctx, identity, agent.ID, broken.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(brokenDetail.Attempts) != 2 || brokenDetail.Attempts[0].Status != domain.AgentEvaluationResultStatusError ||
		brokenDetail.Attempts[1].Status != domain.AgentEvaluationResultStatusPassed || brokenDetail.Attempts[1].Snapshot.Question != "发票怎么开" {
		t.Fatalf("broken detail = %+v", brokenDetail.Attempts)
	}
	again, err := overview.Execute(ctx, identity, agent.ID)
	if err != nil || again.Latest.Passed != 2 || again.Latest.Errors != 1 {
		t.Fatalf("overview after rerun = %+v err = %v", again.Latest, err)
	}
	// 列表在首次结果之外给出最近一次重跑的结果。
	for _, row := range again.Cases {
		if row.ID == broken.ID && (row.RerunStatus == nil || *row.RerunStatus != domain.AgentEvaluationResultStatusPassed || *row.LatestStatus != domain.AgentEvaluationResultStatusError) {
			t.Fatalf("broken row after rerun = %+v", row)
		}
	}

	// 修改用例递增版本；只有快照版本未变且由通过变为未通过的用例标记新失败。
	updated, err := updateCase.Execute(ctx, identity, agent.ID, edited.ID, agentevaluation.CaseInput{
		Audience: domain.ServiceAudienceCustomer, Question: "怎么退款", ExpectedAction: domain.AgentRunOutcomeReply, ExpectedAnswer: "签收后十五天内可以申请退款",
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("updated case = %+v err = %v", updated, err)
	}
	// 修改后的用例标记为已修改，不能再重跑旧快照；已删除的用例同样不能重跑。
	if modified, err := overview.Execute(ctx, identity, agent.ID); err != nil || !modifiedCase(modified, edited.ID) || modifiedCase(modified, stable.ID) {
		t.Fatalf("overview after case edit = %+v err = %v", modified, err)
	}
	if err := rerun.Execute(ctx, identity, agent.ID, edited.ID); !errors.Is(err, agentevaluation.ErrCaseChanged) {
		t.Fatalf("rerun modified case err = %v", err)
	}
	removed := newCase("临时用例", domain.AgentRunOutcomeHandoff, "")
	if err := agentevaluation.NewDeleteCaseAction(db).Execute(ctx, identity, agent.ID, removed.ID); err != nil {
		t.Fatal(err)
	}
	if err := rerun.Execute(ctx, identity, agent.ID, removed.ID); !errors.Is(err, agentevaluation.ErrCaseNotFound) {
		t.Fatalf("rerun deleted case err = %v", err)
	}
	decider.probabilities["签收后七天内可以退款"] = 0.1
	decider.probabilities["质量问题运费由我们承担"] = 0.2
	if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "回答退款与运费问题",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if changed, err := overview.Execute(ctx, identity, agent.ID); err != nil || !changed.ConfigurationChanged {
		t.Fatalf("overview after execution change = %+v err = %v", changed, err)
	}
	runAll()
	second, err := overview.Execute(ctx, identity, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	newFailures := make([]string, 0)
	for _, row := range second.Cases {
		if row.NewFailure {
			newFailures = append(newFailures, row.ID)
		}
	}
	if !slices.Equal(newFailures, []string{stable.ID}) || second.ConfigurationChanged || second.Previous == nil || second.Previous.Passed != 2 {
		t.Fatalf("second overview new failures = %v overview = %+v", newFailures, second)
	}
}

// TestAgentEvaluationReplay 验证回放使用指定配置版本解析服务场景的有效配置，只以给定消息为输入，且不写入会话、消息和运行记录。
func TestAgentEvaluationReplay(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee}, DisplayName: "回放客服",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "第一版指令",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstRevision := agent.Execution.RevisionID
	if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, agent.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "第二版指令",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &replayProbeRuntime{}
	execute := agentrunaction.NewExecuteAction(db, newTestTasks(db), runtime, testAttachmentReader(db), nil, nil)
	messages := []agentruntime.Message{{ID: "question", Role: agentruntime.MessageRoleUser, Content: "怎么退款"}}
	counts := replayWriteCounts(t, db, identity.Organization.ID)

	// 客户提问按客户服务场景解析，指令来自指定的旧配置版本，并带终止工具。
	result, err := execute.Replay(ctx, agentrunaction.ReplayInput{
		ReplayID: "0197d5f8-0000-7000-8000-000000000001", OrganizationID: identity.Organization.ID, AgentID: agent.ID, RevisionID: firstRevision,
		Audience: domain.ServiceAudienceCustomer, Messages: messages,
	})
	if err != nil || result.Content != "回放回复" {
		t.Fatalf("replay result = %+v err = %v", result, err)
	}
	assignment := runtime.request.Assignment
	if assignment.Scene != agentruntime.SceneCustomer || assignment.Grounding != agentruntime.GroundingStrict ||
		!slices.Contains(assignment.Tools, "handoff_to_human") || !strings.Contains(assignment.Instruction, "第一版指令") ||
		len(runtime.claimed.Messages) != 1 || runtime.claimed.Messages[0].Content != "怎么退款" {
		t.Fatalf("replay assignment = %+v claimed = %+v", assignment, runtime.claimed)
	}
	// 员工提问按员工服务场景解析。
	if _, err := execute.Replay(ctx, agentrunaction.ReplayInput{
		ReplayID: "0197d5f8-0000-7000-8000-000000000002", OrganizationID: identity.Organization.ID, AgentID: agent.ID, RevisionID: firstRevision,
		Audience: domain.ServiceAudienceEmployee, Messages: messages,
	}); err != nil || runtime.request.Assignment.Scene != agentruntime.SceneEmployeeService {
		t.Fatalf("employee replay scene = %s err = %v", runtime.request.Assignment.Scene, err)
	}
	if after := replayWriteCounts(t, db, identity.Organization.ID); after != counts {
		t.Fatalf("replay wrote records: before = %+v after = %+v", counts, after)
	}
	// 不属于该 AI 员工的配置版本不可回放。
	if _, err := execute.Replay(ctx, agentrunaction.ReplayInput{
		ReplayID: "0197d5f8-0000-7000-8000-000000000003", OrganizationID: identity.Organization.ID, AgentID: agent.ID, RevisionID: "0197d5f8-0000-7000-8000-000000000009",
		Audience: domain.ServiceAudienceCustomer, Messages: messages,
	}); !errors.Is(err, agentrunaction.ErrReplayConfigurationUnavailable) {
		t.Fatalf("unknown revision err = %v", err)
	}
}

// replayProbeRuntime 认领回放输入并记录运行请求，返回固定回复。
type replayProbeRuntime struct {
	request agentruntime.RunRequest
	claimed agentruntime.ClaimedInput
}

// Run 认领唯一输入并确认没有后续输入。
func (r *replayProbeRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
	r.request = request
	triggers, err := feed.Peek(ctx, 0)
	if err != nil || len(triggers) != 1 {
		return agentruntime.RunResult{}, errors.New("replay feed must provide one input")
	}
	if r.claimed, err = feed.Claim(ctx, triggers[0].Seq); err != nil {
		return agentruntime.RunResult{}, err
	}
	if next, err := feed.Peek(ctx, r.claimed.EndSeq); err != nil || len(next) != 0 {
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
func replayWriteCounts(t *testing.T, db *bun.DB, organizationID string) replayCounts {
	t.Helper()
	counts := replayCounts{}
	if err := db.NewSelect().
		ColumnExpr("(SELECT count(*) FROM conversations WHERE organization_id = ?) AS conversations", organizationID).
		ColumnExpr("(SELECT count(*) FROM messages WHERE organization_id = ?) AS messages", organizationID).
		ColumnExpr("(SELECT count(*) FROM agent_runs WHERE organization_id = ?) AS runs", organizationID).
		Scan(context.Background(), &counts); err != nil {
		t.Fatal(err)
	}
	return counts
}

// pendingEvaluationResults 读取运行中尚未结束的尝试。
func pendingEvaluationResults(t *testing.T, db *bun.DB, runID string) []servermodels.AgentEvaluationResult {
	t.Helper()
	results := make([]servermodels.AgentEvaluationResult, 0)
	if err := db.NewSelect().Model(&results).
		Where("aers.run_id = ? AND aers.status = ?", runID, domain.AgentEvaluationResultStatusPending).
		OrderExpr("aers.created_at, aers.id").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	return &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		triggers, err := feed.Peek(ctx, 0)
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
	tasks := newTestTasks(db)
	for name, register := range map[string]func() error{
		agentrunaction.RunActionName: func() error {
			return tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil })
		},
		agentevaluation.EvaluateActionName: func() error {
			return tasks.Registry().RegisterJSON(agentevaluation.EvaluateActionName, func(context.Context, agentevaluation.EvaluateInput) error { return nil })
		},
	} {
		if err := register(); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Organization.ID)
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
		if err := db.NewSelect().Table("messages").Column("id").Where("conversation_id = ? AND body = ?", conversationID, body).Scan(ctx, &id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	add := agentevaluation.NewAddServiceSessionCaseAction(db)

	// 未被质检标记为应转人工未转的周期不能加入。
	if _, err := add.Execute(ctx, identity, session.ID, messageID("我要找人工")); !errors.Is(err, agentevaluation.ErrServiceSessionNotFound) {
		t.Fatalf("add unflagged session err = %v", err)
	}
	if _, err := db.NewInsert().Model(&servermodels.ServiceSessionReview{
		OrganizationID: identity.Organization.ID, ServiceSessionID: session.ID, ClosedAt: session.CreatedAt, AIMissedHandoff: new(true),
	}).Column("organization_id", "service_session_id", "closed_at", "ai_missed_handoff").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 提问只能是提问人发送的文字消息。
	if _, err := add.Execute(ctx, identity, session.ID, messageID("已为你查询到订单")); !errors.Is(err, agentevaluation.ErrQuestionNotFound) {
		t.Fatalf("add agent message err = %v", err)
	}
	created, err := add.Execute(ctx, identity, session.ID, messageID("我要找人工"))
	if err != nil {
		t.Fatal(err)
	}
	senders := make([]string, 0, len(created.Context.Messages))
	for _, message := range created.Context.Messages {
		senders = append(senders, message.Sender+":"+message.Body)
	}
	if created.Source != domain.AgentEvaluationCaseSourceServiceSession || created.ExpectedAction != domain.AgentRunOutcomeHandoff || created.ExpectedAnswer != "" ||
		created.Question != "我要找人工" || created.AgentID != agent.ID || created.OccurredAt == nil || created.ServiceSessionID == nil ||
		created.Context.CustomerContext == "" || created.Context.Customer != nil || !created.Context.Channel ||
		!slices.Equal(senders, []string{"customer:我要退货", "ai:请提供订单号", "customer:订单号 123", "ai:已为你查询到订单"}) {
		t.Fatalf("captured case = %+v senders = %v", created, senders)
	}

	// 同一提问再次加入时返回已有用例。
	again, err := add.Execute(ctx, identity, session.ID, messageID("我要找人工"))
	if err != nil || again.ID != created.ID {
		t.Fatalf("add same question again = %+v err = %v", again, err)
	}

	// 同一提问以不同的期望处理方式加入时拒绝，已有用例保持不变。
	questionID := messageID("我要找人工")
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return agentevaluation.KnowledgeGapCases{}.AddFromKnowledgeGap(ctx, tx, identity,
			&servermodels.KnowledgeGap{ServiceSessionID: session.ID, QuestionMessageID: &questionID}, "请联系人工客服")
	}); !errors.Is(err, agentevaluation.ErrQuestionAlreadyEvaluated) {
		t.Fatalf("add same question with different expectation err = %v", err)
	}

	// 回放以客户上下文开头，前文按发送方区分角色，客户历史检索只看提问之前关闭的周期。
	summaryProviderID := seedSummaryModels(t, db, identity)
	if _, err := customerservice.NewUpdateServiceSummarySettingsAction(db).Execute(ctx, identity, domain.ServiceSummarySettings{
		Decision: &domain.AIModelReference{ProviderID: summaryProviderID, ModelIdentifier: "decision-model"}, Locale: domain.LocaleChineseSimplified,
	}); err != nil {
		t.Fatal(err)
	}
	runID, err := agentevaluation.NewStartRunAction(db, tasks).Execute(ctx, identity, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	replayer := &evaluationReplayer{results: map[string]agentruntime.RunResult{
		"我要找人工": {Decision: agentruntime.TerminalDecision{Kind: domain.AgentRunOutcomeHandoff, Reason: domain.AgentHandoffReasonCustomerRequested}},
	}, errors: map[string]error{}}
	worker := agentevaluation.NewWorker(db, replayer, &evaluationDecider{probabilities: map[string]float64{}})
	for _, result := range pendingEvaluationResults(t, db, runID) {
		if err := worker.Evaluate(ctx, agentevaluation.EvaluateInput{OrganizationID: identity.Organization.ID, ResultID: result.ID}); err != nil {
			t.Fatal(err)
		}
	}
	replay := replayer.inputs[0]
	roles := make([]agentruntime.MessageRole, 0, len(replay.Messages))
	for _, message := range replay.Messages {
		roles = append(roles, message.Role)
	}
	if replay.Messages[0].Content != created.Context.CustomerContext || replay.Messages[len(replay.Messages)-1].Content != "我要找人工" ||
		!slices.Equal(roles, []agentruntime.MessageRole{
			agentruntime.MessageRoleUser, agentruntime.MessageRoleUser, agentruntime.MessageRoleAssistant,
			agentruntime.MessageRoleUser, agentruntime.MessageRoleAssistant, agentruntime.MessageRoleUser,
		}) ||
		replay.History == nil || replay.History.ServiceSessionID != session.ID || !replay.History.ClosedBefore.Equal(*created.OccurredAt) || replay.Customer != nil {
		t.Fatalf("replay input = %+v roles = %v", replay, roles)
	}
	if overview, err := agentevaluation.NewOverviewQuery(db).Execute(ctx, identity, agent.ID); err != nil || overview.Latest.Passed != 1 {
		t.Fatalf("overview = %+v err = %v", overview, err)
	}
}
