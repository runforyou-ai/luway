//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// executionScopeFixture 是客服周期内 Agent 执行范围的测试环境。
type executionScopeFixture struct {
	customerReadFixture
	agentIdentityID string
	tasks           *servertest.Tasks
	scheduler       *agentrunaction.Scheduler
	coordinator     *testAgentRun
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
	require.NoError(t, err, "创建模型服务失败")
	agent, err := agentaction.NewCreateAgentAction(f.db).Execute(ctx, f.owner, agentaction.CreateInput{
		ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "执行范围助手",
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: aiModelID(t, f.db, provider.ID, "chat-a"), SystemInstruction: "回答客户问题",
		}},
	})
	require.NoError(t, err, "创建 AI 员工失败")
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	coordinator := newTestAgentRun(f.db, tasks, nil, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	return executionScopeFixture{
		customerReadFixture: f, agentIdentityID: agent.IdentityID, tasks: tasks, scheduler: scheduler, coordinator: coordinator,
		transfer: servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, testEnqueuer),
		claim:    servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer),
		close:    servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer),
	}
}

// transferToAgent 把当前处理周期交给 AI 客服并返回周期编号。
func (f executionScopeFixture) transferToAgent(t *testing.T, ctx context.Context) string {
	t.Helper()
	_, err := f.claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err, "领取周期失败")
	session, err := f.transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.agentIdentityID,
	})
	require.NoError(t, err, "转交给 AI 客服失败")
	return session.ID
}

// activeRunCount 统计会话内尚未终结的 Agent 运行。
func (f executionScopeFixture) activeRunCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	count, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.conversation_id = ?", f.conversationID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Count(ctx)
	require.NoError(t, err)
	return int(count)
}

// laneForSession 读取指定客服周期的输入队列。
func (f executionScopeFixture) laneForSession(t *testing.T, ctx context.Context, sessionID string) servermodels.AgentLane {
	t.Helper()
	lane := servermodels.AgentLane{}
	require.NoError(t, f.db.NewSelect().Model(&lane).
		Where("al.scope_kind = ? AND al.scope_id = ?", domain.AgentExecutionScopeServiceSession, sessionID).
		Where("al.agent_identity_id = ?", f.agentIdentityID).
		Scan(ctx))
	return lane
}

// TestAgentExecutionScopeSessionBoundary 验证客服周期切换后输入队列按新周期重新编号。
func TestAgentExecutionScopeSessionBoundary(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	firstSession := f.transferToAgent(t, ctx)
	_, err := f.visitorMessage(ctx, "第一周期追问")
	require.NoError(t, err)
	firstLane := f.laneForSession(t, ctx, firstSession)
	require.Equal(t, int64(2), firstLane.DesiredSeq, "首个周期队列水位")
	require.Equal(t, 1, f.activeRunCount(t, ctx), "运行期间新增输入后活动运行")
	_, err = f.claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = f.close.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	settled := f.laneForSession(t, ctx, firstSession)
	require.Equal(t, settled.DesiredSeq, settled.ProcessedSeq, "关闭后首个周期队列未结算")
	reopened, err := f.visitorMessage(ctx, "新周期首问")
	require.NoError(t, err)
	require.NotEqual(t, firstSession, reopened.Conversation.ServiceSessionID, "期望新的客服周期")
	secondSession := f.transferToAgent(t, ctx)
	secondLane := f.laneForSession(t, ctx, secondSession)
	require.NotEqual(t, firstLane.ID, secondLane.ID, "期望独立队列")
	require.Equal(t, int64(1), secondLane.DesiredSeq)
	require.Equal(t, int64(0), secondLane.ProcessedSeq)
	var secondInputSeqs []int64
	require.NoError(t, f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).
		ColumnExpr("ai.input_seq").
		Where("ai.lane_id = ?", secondLane.ID).
		OrderExpr("ai.input_seq ASC").
		Scan(ctx, &secondInputSeqs))
	require.Equal(t, []int64{1}, secondInputSeqs, "新周期输入序号")
	reread := f.laneForSession(t, ctx, firstSession)
	require.Equal(t, firstLane.ID, reread.ID, "新周期建立后首个周期队列被改写")
	require.Equal(t, settled.DesiredSeq, reread.DesiredSeq, "新周期建立后首个周期队列被改写")
	require.Equal(t, settled.ProcessedSeq, reread.ProcessedSeq, "新周期建立后首个周期队列被改写")
	oldInputCount, err := f.db.NewSelect().Model((*servermodels.AgentInput)(nil)).
		Where("ai.lane_id = ?", firstLane.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, settled.DesiredSeq, oldInputCount, "旧周期输入数量")
}

// TestAgentExecutionScopeHandoverKeepsSingleRun 验证转交、领取与关闭不产生重叠运行。
func TestAgentExecutionScopeHandoverKeepsSingleRun(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.transferToAgent(t, ctx)
	require.Equal(t, 1, f.activeRunCount(t, ctx), "转交后活动运行")
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
		require.NoError(t, step.operate(), "%s 失败", step.name)
		require.Equal(t, step.wantActive, f.activeRunCount(t, ctx), "%s 后活动运行", step.name)
	}
	// 真人领取取消原负责人运行，取消原因固定为负责人变化。
	cancelled := make([]string, 0)
	require.NoError(t, f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		ColumnExpr("COALESCE(agr.error_code, '')").
		Where("agr.conversation_id = ?", f.conversationID).
		Where("agr.status = ?", domain.AgentRunStatusCancelled).
		Scan(ctx, &cancelled))
	require.Len(t, cancelled, 2, "取消运行数量")
	for _, code := range cancelled {
		require.Equal(t, string(domain.AgentRunErrorCodeAssigneeChanged), code, "取消原因")
	}
}

// TestAgentExecutionScopeReturnsUnrepresentedRuns 验证负责人变化取消的运行与新负责人的运行同时返回，新消息到达后取消提示不再返回。
func TestAgentExecutionScopeReturnsUnrepresentedRuns(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.transferToAgent(t, ctx)
	// 真人领取取消 AI 运行且不写结果消息，再次转交后新运行开始。
	_, err := f.claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err, "领取周期失败")
	f.transferToAgent(t, ctx)
	query := conversationaction.NewListConversationMessagesQuery(f.db)
	history, err := query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	require.Len(t, history.AgentRuns, 2)
	require.Equal(t, domain.AgentRunStatusCancelled, history.AgentRuns[0].Status)
	require.NotNil(t, history.AgentRuns[0].ErrorCode)
	require.Equal(t, string(domain.AgentRunErrorCodeAssigneeChanged), *history.AgentRuns[0].ErrorCode)
	require.Equal(t, domain.AgentRunStatusQueued, history.AgentRuns[1].Status)
	// 访客在同一线程追加消息，取消提示被新消息取代。
	_, err = f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef",
		ConversationID: &f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客户追加消息",
	})
	require.NoError(t, err)
	history, err = query.Execute(ctx, f.owner, conversationaction.ConversationMessageHistoryInput{ConversationID: f.conversationID})
	require.NoError(t, err)
	require.Len(t, history.AgentRuns, 1, "新消息后运行集合")
	require.Equal(t, domain.AgentRunStatusQueued, history.AgentRuns[0].Status)
}

// TestAgentExecutionScopeKeepsSuppressedProcess 验证运行在吸收后续输入时失去资格后，中断前的过程内容仍然保留并可读取。
func TestAgentExecutionScopeKeepsSuppressedProcess(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	sessionID := f.transferToAgent(t, ctx)
	run := &servermodels.AgentRun{}
	require.NoError(t, f.db.NewSelect().Model(run).
		Where("agr.scope_id = ? AND agr.status = ?", sessionID, domain.AgentRunStatusQueued).Scan(ctx))
	claimed, release := make(chan struct{}), make(chan struct{})
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := feed.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		close(claimed)
		<-release
		partial := agentruntime.RunResult{
			Usage: agentcontract.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
			Blocks: runBlocks([]agentcontract.Block{{
				ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
				Kind: domain.AgentRunBlockThinking, Payload: agentcontract.BlockPayload{Text: "失去资格前的思考"},
			}}),
		}
		// 运行已被取消，吸收后续输入时被抑制；运行 context 未取消，与更换机器人等入口一致。
		_, err := feed.Claim(ctx, 2)
		assert.Error(t, err, "吸收后续输入没有被抑制")
		return partial, err
	}}
	executor := newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	finished := make(chan error, 1)
	go func() { finished <- executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}) }()
	<-claimed
	require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
		_, err := f.coordinator.CancelForServiceSession(ctx, tx, run.WorkspaceID, sessionID, f.agentIdentityID, domain.AgentRunErrorCodeAccountChanged)
		return err
	}))
	close(release)
	require.NoError(t, <-finished, "抑制的运行返回错误")
	process, err := processquery.NewGetRunProcessQuery(f.db).Execute(ctx, f.owner, run.ID)
	require.NoError(t, err)
	require.Len(t, process.Blocks, 1)
	require.Equal(t, "失去资格前的思考", process.Blocks[0].Payload.Text)
	require.Equal(t, 10, process.Usage.TotalTokens)
}
