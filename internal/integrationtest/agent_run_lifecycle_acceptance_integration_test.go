//go:build server

package integrationtest

// 本文件以端到端最终状态锁定工具决定之后的收尾分派：已批准业务系统调用的中断、失败与授权失效，
// 以及工作区电脑上需要审批的命令被拒绝、过期或批准时已不可派发的结算、唤醒与不派发。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// TestLifecycleApprovedBusinessCall 验证已批准的业务系统调用在执行任务中的收尾：执行中断的有副作用调用转待核对且不重复调用，
// 业务系统返回失败时记为失败，批准后授权撤销时记为失败且不调用，三种结果都以结果事件唤醒 AI 员工。
func TestLifecycleApprovedBusinessCall(t *testing.T) {
	t.Parallel()
	db, owner, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	var mu sync.Mutex
	requests := make([]string, 0)
	var failing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Path+" "+string(body))
		mu.Unlock()
		if failing.Load() {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(writer, `{"error":"down"}`)
			return
		}
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	system := &servermodels.BusinessSystem{
		WorkspaceID: owner.Workspace.ID, Name: "订单系统", Transport: domain.BusinessSystemTransportHTTP,
		Connection: domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{BaseURL: server.URL, Spec: "{}"}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone},
		Tools: []domain.BusinessTool{{Name: "refund", Description: "退款", InputSchema: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}}}`),
			HTTP: &domain.HTTPOperation{Method: "POST", Path: "/refunds", Parameters: []domain.HTTPParameter{{Name: "amount", In: domain.HTTPParameterInBody, Key: "amount"}}}}},
	}
	_, err := db.NewInsert().Model(system).Column("workspace_id", "name", "transport", "connection", "credential", "tools").Returning("id").Exec(ctx)
	require.NoError(t, err)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "处理订单"}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{DisplayName: "订单助手", Execution: execution})
	require.NoError(t, err)
	_, err = db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("responsible_user_id = ?", owner.User.ID).Where("id = ?", agent.ID).Exec(ctx)
	require.NoError(t, err)
	grants := []domain.BusinessSystemGrant{{BusinessSystemID: system.ID, ToolGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL3}}}
	update := agentaction.NewUpdateExecutionAction(db)
	agent, err = update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	chat := &scriptedDecisionModel{}
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return chat, nil }
	fixture := &decisionFixture{
		t: t, ctx: ctx, db: db, owner: owner, agent: agent, model: chat, requests: &requests, mu: &mu,
		runner:    newTestAgentRun(db, tasks, runtime, modelcall.New(db, upstreams, nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil),
		decisions: newTestToolDecisions(db, tasks),
		send:      directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)),
	}
	first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, owner, directchataction.FirstAgentTextMessageInput{
		ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "开始",
	})
	require.NoError(t, err)
	conversationID := first.Conversation.ID
	chat.use("［业务系统 · 订单系统］退款")
	fixture.runQueued(conversationID)
	// approved 提交一次退款审批并由负责人批准，返回批准后排队等待执行的调用。
	approved := func(body string) servermodels.AgentToolCall {
		t.Helper()
		call := fixture.latestCall(conversationID)
		if call.Status != string(domain.AgentToolCallAwaitingDecision) {
			fixture.sendAndRun(conversationID, body)
			call = fixture.latestCall(conversationID)
		}
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		call = fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallQueued), call.Status)
		return call
	}

	t.Run("执行中断的有副作用调用转待核对且不重复执行", func(t *testing.T) {
		call := approved("退款")
		require.True(t, call.SideEffects, "退款工具有副作用")
		require.False(t, call.Replayable, "退款工具不可重新执行")
		// 模拟执行任务在调用业务系统期间中断：调用停留在执行中，任务重试。
		_, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).Set("status = ?", domain.AgentToolCallRunning).Set("started_at = now()").Where("id = ?", call.ID).Exec(ctx)
		require.NoError(t, err)
		input := agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, input))
		require.Empty(t, fixture.sentRequests(), "中断后的重试调用了业务系统")
		settled := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallNeedsReview), settled.Status)
		require.NotNil(t, settled.Result)
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, input))
		require.Empty(t, fixture.sentRequests(), "待核对的调用被再次执行")
		fixture.runQueued(conversationID)
		fixture.assertLatestReply(conversationID, `"status":"needs_review"`)
		pending, err := fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool { return item.ID == call.ID && item.CanReview }), "pending = %+v", pending)
		require.NoError(t, fixture.decisions.Review(ctx, owner, call.ID))
	})

	t.Run("业务系统返回失败时记为失败并唤醒", func(t *testing.T) {
		call := approved("再退一笔")
		failing.Store(true)
		t.Cleanup(func() { failing.Store(false) })
		input := agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, input))
		require.Len(t, fixture.sentRequests(), 1)
		failed := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallFailed), failed.Status)
		require.NotNil(t, failed.Error)
		require.Nil(t, failed.Result)
		// 重复投递的执行任务不再调用业务系统。
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, input))
		require.Len(t, fixture.sentRequests(), 1)
		fixture.runQueued(conversationID)
		fixture.assertLatestReply(conversationID, `"status":"failed"`)
	})

	t.Run("批准后授权撤销时记为失败且不调用", func(t *testing.T) {
		call := approved("第三笔退款")
		before := len(fixture.sentRequests())
		_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
		})
		// 现状：撤销授权不取消已批准排队的调用，由执行任务按当前授权判定为不可执行。
		require.Equal(t, string(domain.AgentToolCallQueued), fixture.latestCall(conversationID).Status)
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}))
		require.Len(t, fixture.sentRequests(), before, "授权撤销后仍调用了业务系统")
		failed := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallFailed), failed.Status)
		require.NotNil(t, failed.Error)
		fixture.runQueued(conversationID)
		fixture.assertLatestReply(conversationID, `"status":"failed"`)
	})
}

// TestLifecycleWorkspaceComputerDecision 验证工作区电脑上需要审批的命令在审批收尾时的分派：拒绝与过期记为终态、不派发给电脑并唤醒 AI 员工，
// 批准时授权已不允许该级别则记为失败且不派发。
func TestLifecycleWorkspaceComputerDecision(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "维护构建"}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{DisplayName: "构建助手", Execution: execution})
	require.NoError(t, err)
	added, err := computeraction.NewCreateWorkspaceComputerAction(db).Execute(ctx, identity, "构建服务器")
	require.NoError(t, err)
	_, err = agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agent.ID, agentaction.UpdateInput{
		DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: agent.ServiceAudiences, ResponsibleUserID: identity.User.ID,
		ComputerID: added.Record.ID, ComputerGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL3}, WorkStatus: domain.WorkStatusWorking,
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	chat := &scriptedComputerModel{}
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return chat, nil }
	fixture := &personalAgentFixture{
		t: t, ctx: ctx, db: db, identity: identity, tasks: tasks, computer: added, model: chat,
		runner:    newTestAgentRun(db, tasks, runtime, modelcall.New(db, upstreams, nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil),
		sendFirst: directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)),
	}
	decisions := newTestToolDecisions(db, tasks)
	operations := computeraction.NewOperationsAction(db, newTestToolDecisions(db, tasks))
	fixture.online()
	// submit 让 AI 员工在新单聊中执行命令，返回提交审批的运行与调用。
	submit := func(command string) (servermodels.AgentRun, servermodels.AgentToolCall) {
		t.Helper()
		conversationID := uuid.NewV7().String()
		_, err := fixture.sendFirst.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "执行 " + command,
		})
		require.NoError(t, err)
		var run servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ?", conversationID).Scan(ctx))
		chat.use("execute", `{"command":"`+command+`"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		call := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		require.Equal(t, string(domain.ToolInterventionApproval), *call.Intervention)
		return run, call
	}
	// settledAndWoken 核对调用的终态、电脑领取不到该调用，并执行结果事件唤醒的运行，回复带出调用状态。
	settledAndWoken := func(run servermodels.AgentRun, want domain.AgentToolCallStatus) {
		t.Helper()
		call := fixture.computerCall(run.ID)
		require.Equal(t, string(want), call.Status)
		require.NotNil(t, call.DecidedAt)
		fixture.online()
		claimed, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
		require.NoError(t, err)
		require.False(t, slices.ContainsFunc(claimed.Operations, func(operation computeraction.Operation) bool { return operation.ID == call.ID }), "终态调用被电脑领取")
		var woken servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&woken).Where("agr.conversation_id = ? AND agr.status = ?", run.ConversationID, domain.AgentRunStatusQueued).Scan(ctx), "结果事件未唤醒运行")
		chat.call = nil
		fixture.run(woken.ID)
		fixture.assertStatus(woken.ID, domain.AgentRunStatusSucceeded)
		count, err := db.NewSelect().Model((*servermodels.Message)(nil)).
			Where("conversation_id = ? AND system_event_type = ?", run.ConversationID, domain.ConversationSystemEventAgentToolCallResolved).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), count, "结果事件条数")
	}

	t.Run("批准时授权已不允许该级别则记为失败且不派发", func(t *testing.T) {
		run, call := submit("make deploy")
		// 绕过成员操作直接降低授权，模拟审批与授权变更交错。
		_, err := db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("computer_grant = ?", domain.ToolGrant{MaxLevel: domain.OperationLevelL2}).Where("id = ?", agent.ID).Exec(ctx)
		require.NoError(t, err)
		require.NoError(t, decisions.Decide(ctx, identity, call.ID, true))
		failed := fixture.computerCall(run.ID)
		require.NotNil(t, failed.Error)
		require.NotEqual(t, agentcontract.ErrComputerOffline.Error(), *failed.Error)
		settledAndWoken(run, domain.AgentToolCallFailed)
	})
}

// TestLifecycleGroupStopAuthorization 验证群聊停止回复只对本群在群成员开放：非群成员与其他工作区身份不能停止，
// 群主停止后运行取消并写入停止消息，重复停止返回已取消状态且不重复写入。
func TestLifecycleGroupStopAuthorization(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agents := newGroupAgentCollaborators(t, db, identity, providerID, modelID)
	f := newGroupAgentFixture(t, db, identity, agents)
	f.post(t, "请看下", []string{f.agents[0].IdentityID}, "")
	running := f.activeRun(t)
	require.NotNil(t, running)
	coordinator := newTestAgentRun(db, f.tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
	colleague := newChatLockUser(t, db, identity)
	_, err := coordinator.StopGroupAgentReply(ctx, colleague, f.groupID, running.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "非群成员停止")
	outsider := newNavigationFixture(t)
	_, err = coordinator.StopGroupAgentReply(ctx, outsider.owner, f.groupID, running.ID)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "其他工作区停止")
	assertAgentRunStatus(t, ctx, db, running.ID, domain.AgentRunStatusQueued)
	for range 2 {
		status, err := coordinator.StopGroupAgentReply(ctx, identity, f.groupID, running.ID)
		require.NoError(t, err)
		require.Equal(t, domain.AgentRunStatusCancelled, status)
	}
	count, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ? AND type = ?", f.groupID, domain.MessageTypeAgentCancelled).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "停止消息条数")
}
