//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
)

type executionScopeFixture struct {
	customerReadFixture
	agentIdentityID string
	tasks           *servertask.Runtime
	scheduler       *agentrunaction.Scheduler
	coordinator     *agentrunaction.ExecuteAction
	transfer        *servicesessionaction.TransferServiceSessionAction
	claim           *servicesessionaction.ClaimServiceSessionAction
	close           *servicesessionaction.CloseServiceSessionAction
}

// newExecutionScopeFixture 建立可转交给 AI 客服的网站客户会话。
func newExecutionScopeFixture(t *testing.T) executionScopeFixture {
	t.Helper()
	ctx := context.Background()
	f := newCustomerReadFixture(t)
	provider, err := aiprovideraction.NewCreateAIProviderAction(f.db).Execute(ctx, f.owner, aiprovideraction.Input{
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		Brand:          domain.AIProviderBrandOpenAI, Name: uuid.NewV7().String(), APIKey: "test-key", APIURL: "https://models.test/v1",
		Models: []aiprovideraction.Model{{
			Identifier: "chat-a", Name: "对话 A", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 8192, MaxOutputTokens: 4096,
		}},
	})
	if err != nil {
		t.Fatalf("创建模型服务失败：%v", err)
	}
	agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "执行范围助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: aiModelID(t, f.db, provider.ID, "chat-a"), SystemInstruction: "回答客户问题",
		}},
	})
	if err != nil {
		t.Fatalf("创建 AI 员工失败：%v", err)
	}
	tasks := newTestTasks(f.db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatalf("注册任务失败：%v", err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	coordinator := agentrunaction.NewExecuteAction(f.db, tasks, nil, testAttachmentReader(f.db), nil, nil)
	return executionScopeFixture{
		customerReadFixture: f, agentIdentityID: agent.IdentityID, tasks: tasks, scheduler: scheduler, coordinator: coordinator,
		transfer: servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, newTestTasks(f.db)),
		claim:    servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)),
		close:    servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)),
	}
}

// transferToAgent 把当前处理周期交给 AI 客服并返回周期编号。
func (f executionScopeFixture) transferToAgent(t *testing.T, ctx context.Context) string {
	t.Helper()
	if _, err := f.claim.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatalf("领取周期失败：%v", err)
	}
	session, err := f.transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.agentIdentityID,
	})
	if err != nil {
		t.Fatalf("转交给 AI 客服失败：%#v", err)
	}
	return session.ID
}

// activeRunCount 统计会话内尚未终结的 Agent 运行。
func (f executionScopeFixture) activeRunCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	count, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.conversation_id = ?", f.conversationID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// laneForSession 读取指定客服周期的输入队列。
func (f executionScopeFixture) laneForSession(t *testing.T, ctx context.Context, sessionID string) servermodels.AgentLane {
	t.Helper()
	lane := servermodels.AgentLane{}
	if err := f.db.NewSelect().Model(&lane).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeServiceSession, sessionID).
		Where("al.agent_identity_id = ?", f.agentIdentityID).
		Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return lane
}

// TestAgentExecutionScopeUniqueness 验证同一执行范围内只允许一个活动运行。
func TestAgentExecutionScopeUniqueness(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	sessionID := f.transferToAgent(t, ctx)
	if count := f.activeRunCount(t, ctx); count != 1 {
		t.Fatalf("转交后活动运行 = %d，期望 1", count)
	}
	existing := servermodels.AgentRun{}
	if err := f.db.NewSelect().Model(&existing).
		Where("agr.conversation_id = ?", f.conversationID).
		Where("agr.status = ?", domain.AgentRunStatusQueued).
		Scan(ctx); err != nil {
		t.Fatal(err)
	}
	duplicate := &servermodels.AgentRun{
		ID: uuid.NewV7().String(), OrganizationID: existing.OrganizationID, ConversationID: existing.ConversationID,
		AgentIdentityID: existing.AgentIdentityID, AgentRevisionID: existing.AgentRevisionID, LaneID: existing.LaneID,
		ScopeKind: string(domain.AgentExecutionScopeServiceSession), ScopeID: sessionID,
		Status: string(domain.AgentRunStatusQueued), InputStartSeq: 1,
	}
	_, err := f.db.NewInsert().Model(duplicate).
		Column("id", "organization_id", "conversation_id", "agent_identity_id", "agent_revision_id", "lane_id", "scope_kind", "scope_id", "status", "input_start_seq").
		Exec(ctx)
	if !pgerr.UniqueViolationOn(err, "agent_runs_active_scope_unique") {
		t.Fatalf("同范围第二个活动运行未被唯一索引拒绝：%v", err)
	}
}

// TestAgentExecutionScopeSessionBoundary 验证客服周期切换后输入队列按新周期重新编号。
func TestAgentExecutionScopeSessionBoundary(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	firstSession := f.transferToAgent(t, ctx)
	if _, err := f.visitorMessage(ctx, "第一周期追问"); err != nil {
		t.Fatal(err)
	}
	firstLane := f.laneForSession(t, ctx, firstSession)
	if firstLane.DesiredSeq != 2 {
		t.Fatalf("首个周期队列水位 = %+v，期望 desired_seq 为 2", firstLane)
	}
	if count := f.activeRunCount(t, ctx); count != 1 {
		t.Fatalf("运行期间新增输入后活动运行 = %d，期望仍为 1", count)
	}
	if _, err := f.claim.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.close.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	settled := f.laneForSession(t, ctx, firstSession)
	if settled.ProcessedSeq != settled.DesiredSeq {
		t.Fatalf("关闭后首个周期队列未结算：%+v", settled)
	}
	reopened, err := f.visitorMessage(ctx, "新周期首问")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Conversation.ServiceSessionID == firstSession {
		t.Fatalf("期望新的客服周期，实际仍为 %s", firstSession)
	}
	secondSession := f.transferToAgent(t, ctx)
	secondLane := f.laneForSession(t, ctx, secondSession)
	if secondLane.ID == firstLane.ID || secondLane.DesiredSeq != 1 || secondLane.ProcessedSeq != 0 {
		t.Fatalf("新周期队列 = %+v，期望独立队列且序号从 1 开始", secondLane)
	}
	var secondInputSeqs []int64
	if err := f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).
		ColumnExpr("ai.input_seq").
		Where("ai.lane_id = ?", secondLane.ID).
		OrderExpr("ai.input_seq ASC").
		Scan(ctx, &secondInputSeqs); err != nil {
		t.Fatal(err)
	}
	if len(secondInputSeqs) != 1 || secondInputSeqs[0] != 1 {
		t.Fatalf("新周期输入序号 = %v，期望仅有序号 1", secondInputSeqs)
	}
	reread := f.laneForSession(t, ctx, firstSession)
	if reread.ID != firstLane.ID || reread.DesiredSeq != settled.DesiredSeq || reread.ProcessedSeq != settled.ProcessedSeq {
		t.Fatalf("新周期建立后首个周期队列被改写：%+v", reread)
	}
	oldInputCount, err := f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).
		Where("ai.lane_id = ?", firstLane.ID).Count(ctx)
	if err != nil || int64(oldInputCount) != settled.DesiredSeq {
		t.Fatalf("旧周期输入数量 = %d，期望 %d，error = %v", oldInputCount, settled.DesiredSeq, err)
	}
}

// TestAgentExecutionScopeHandoverKeepsSingleRun 验证转交、领取与关闭不产生重叠运行。
func TestAgentExecutionScopeHandoverKeepsSingleRun(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.transferToAgent(t, ctx)
	if count := f.activeRunCount(t, ctx); count != 1 {
		t.Fatalf("转交后活动运行 = %d，期望 1", count)
	}
	for _, step := range []struct {
		name       string
		operate    func() error
		wantActive int
	}{
		{"领取", func() error { _, err := f.claim.Execute(ctx, f.owner, f.conversationID); return err }, 0},
		{"再次转交", func() error { f.transferToAgent(t, ctx); return nil }, 1},
		{"收回", func() error { _, err := f.claim.Execute(ctx, f.owner, f.conversationID); return err }, 0},
		{"关闭", func() error { _, err := f.close.Execute(ctx, f.owner, f.conversationID); return err }, 0},
	} {
		if err := step.operate(); err != nil {
			t.Fatalf("%s 失败：%v", step.name, err)
		}
		if count := f.activeRunCount(t, ctx); count != step.wantActive {
			t.Fatalf("%s 后活动运行 = %d，期望 %d", step.name, count, step.wantActive)
		}
	}
	// 真人领取取消原负责人运行，取消原因固定为负责人变化。
	cancelled := make([]string, 0)
	if err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		ColumnExpr("COALESCE(agr.error_code, '')").
		Where("agr.conversation_id = ?", f.conversationID).
		Where("agr.status = ?", domain.AgentRunStatusCancelled).
		Scan(ctx, &cancelled); err != nil {
		t.Fatal(err)
	}
	if len(cancelled) != 2 {
		t.Fatalf("取消运行数量 = %d，期望 2", len(cancelled))
	}
	for _, code := range cancelled {
		if code != string(domain.AgentRunErrorCodeAssigneeChanged) {
			t.Fatalf("取消原因 = %q，期望 %q", code, domain.AgentRunErrorCodeAssigneeChanged)
		}
	}
}

// TestAgentExecutionScopeReturnsUnrepresentedRuns 验证负责人变化取消的运行与新负责人的运行同时返回，新消息到达后取消提示不再返回。
func TestAgentExecutionScopeReturnsUnrepresentedRuns(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.transferToAgent(t, ctx)
	// 真人领取取消 AI 运行且不写结果消息，再次转交后新运行开始。
	if _, err := f.claim.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatalf("领取周期失败：%v", err)
	}
	f.transferToAgent(t, ctx)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	history, err := query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.AgentRuns) != 2 || history.AgentRuns[0].Status != domain.AgentRunStatusCancelled ||
		history.AgentRuns[0].ErrorCode == nil || *history.AgentRuns[0].ErrorCode != string(domain.AgentRunErrorCodeAssigneeChanged) ||
		history.AgentRuns[1].Status != domain.AgentRunStatusQueued {
		t.Fatalf("运行集合 = %#v", history.AgentRuns)
	}
	// 访客在同一线程追加消息，取消提示被新消息取代。
	if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef",
		ConversationID: &f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客户追加消息",
	}); err != nil {
		t.Fatal(err)
	}
	history, err = query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	if err != nil || len(history.AgentRuns) != 1 || history.AgentRuns[0].Status != domain.AgentRunStatusQueued {
		t.Fatalf("新消息后运行集合 = %#v，错误 = %v", history.AgentRuns, err)
	}
}

// TestAgentExecutionScopeKeepsSuppressedProcess 验证运行在吸收后续输入时失去资格后，中断前的过程内容仍然保留并可读取。
func TestAgentExecutionScopeKeepsSuppressedProcess(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	sessionID := f.transferToAgent(t, ctx)
	run := &servermodels.AgentRun{}
	if err := f.db.NewSelect().Model(run).
		Where("agr.scope_id = ? AND agr.status = ?", sessionID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, release := make(chan struct{}), make(chan struct{})
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		close(claimed)
		<-release
		partial := agentruntime.RunResult{
			Usage: agentruntime.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
			Blocks: []agentruntime.Block{{
				ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
				Kind: domain.AgentRunBlockThinking, Payload: agentruntime.BlockPayload{Text: "失去资格前的思考"},
			}},
		}
		// 运行已被取消，吸收后续输入时被抑制；运行 context 未取消，与更换机器人等入口一致。
		_, err := feed.Claim(ctx, 2)
		if err == nil {
			t.Error("吸收后续输入没有被抑制")
		}
		return partial, err
	}}
	executor := agentrunaction.NewExecuteAction(f.db, f.tasks, runtime, testAttachmentReader(f.db), nil, nil)
	finished := make(chan error, 1)
	go func() { finished <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
	<-claimed
	if _, err := f.coordinator.CancelForServiceSession(ctx, f.db, run.OrganizationID, sessionID, f.agentIdentityID, domain.AgentRunErrorCodeBotChanged); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatalf("抑制的运行返回错误：%v", err)
	}
	process, err := conversationaction.NewGetAgentRunProcessQuery(f.db).Execute(ctx, f.owner, run.ID)
	if err != nil || len(process.Blocks) != 1 || process.Blocks[0].Payload.Text != "失去资格前的思考" || process.Usage.TotalTokens != 10 {
		t.Fatalf("被抑制运行的过程 = %#v，错误 = %v", process, err)
	}
}
