//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/actions/usernotification"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// scriptedDecisionModel 是按最后一条消息决定输出的对话模型：收到工具调用结果事件时复述事件，收到工具结果时复述结果，其余情况调用描述含 tool 的业务系统工具，calls 大于 1 时在一次输出中调用多次。
type scriptedDecisionModel struct {
	mu    sync.Mutex
	tool  string
	calls int
}

// use 设置下一次运行调用的业务系统工具描述。
func (m *scriptedDecisionModel) use(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tool = tool
}

// Generate 按最后一条消息返回工具调用或回答。
func (m *scriptedDecisionModel) Generate(_ context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 运行时可能在末尾追加系统提示，按最后一条非系统消息决定输出。
	last := input[len(input)-1]
	for index := len(input) - 1; index >= 0 && last.Role == schema.AgenticRoleTypeSystem; index-- {
		last = input[index]
	}
	for _, block := range last.ContentBlocks {
		switch {
		case block.Type == schema.ContentBlockTypeFunctionToolResult:
			var text strings.Builder
			for _, content := range block.FunctionToolResult.Content {
				if content.Type == schema.FunctionToolResultContentBlockTypeText {
					text.WriteString(content.Text.Text)
				}
			}
			return assistantText("结果：" + text.String()), nil
		case block.Type == schema.ContentBlockTypeUserInputText && strings.Contains(block.UserInputText.Text, `"kind":"tool_call_resolved"`):
			return assistantText("收到：" + block.UserInputText.Text), nil
		}
	}
	for _, info := range model.GetCommonOptions(&model.Options{}, opts...).Tools {
		if strings.Contains(info.Desc, m.tool) {
			message := assistantText("")
			for range max(1, m.calls) {
				message.ContentBlocks = append(message.ContentBlocks, schema.NewContentBlock(&schema.FunctionToolCall{
					CallID: "call-" + uuid.NewV7().String(), Name: info.Name, Arguments: `{"amount":30}`,
				}))
			}
			return message, nil
		}
	}
	return assistantText("没有可用工具"), nil
}

// Stream 以单个分片返回模型输出。
func (m *scriptedDecisionModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// decisionFixture 是工具调用确认与审批集成测试共用的工作区、AI 员工、业务系统与调用入口。
type decisionFixture struct {
	t         *testing.T
	ctx       context.Context
	db        *bun.DB
	owner     *servermodels.Identity
	agent     *agentaction.Agent
	model     *scriptedDecisionModel
	runner    *testAgentRun
	decisions *tooldecisionaction.Action
	send      *directchataction.SendAgentTextMessageAction
	requests  *[]string
	mu        *sync.Mutex
}

// TestAgentToolDecisions 验证需要审批的业务系统工具提交给处理成员而不执行，批准后按原参数执行，结果、拒绝与过期作为事件唤醒 AI 员工新一轮运行；
// 个人 AI 员工对话中需要发起人确认的调用暂停运行，确认后在同一轮内执行，拒绝与过期以原因交给模型；
// 待处理列表、会话时间线卡片、待核对的核对，以及需要审批的授权要求 AI 员工指定负责人。
func TestAgentToolDecisions(t *testing.T) {
	t.Parallel()
	db, owner, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	var mu sync.Mutex
	requests := make([]string, 0)
	var onRequest func()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Path+" "+string(body))
		callback := onRequest
		mu.Unlock()
		if callback != nil {
			callback()
		}
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	reversible := false
	system := &servermodels.BusinessSystem{
		WorkspaceID: owner.Workspace.ID, Name: "订单系统", Transport: domain.BusinessSystemTransportHTTP,
		Connection: domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{BaseURL: server.URL, Spec: "{}"}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone},
		Tools: []domain.BusinessTool{
			{Name: "refund", Description: "退款", InputSchema: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}}}`),
				HTTP: &domain.HTTPOperation{Method: "POST", Path: "/refunds", Parameters: []domain.HTTPParameter{{Name: "amount", In: domain.HTTPParameterInBody, Key: "amount"}}}},
			{Name: "update_note", Description: "修改备注", DestructiveHint: &reversible, InputSchema: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}}}`),
				HTTP: &domain.HTTPOperation{Method: "PATCH", Path: "/notes", Parameters: []domain.HTTPParameter{{Name: "amount", In: domain.HTTPParameterInBody, Key: "amount"}}}},
		},
	}
	_, err := db.NewInsert().Model(system).Column("workspace_id", "name", "transport", "connection", "credential", "tools").Returning("id").Exec(ctx)
	require.NoError(t, err)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "处理订单"}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{DisplayName: "订单助手", Execution: execution})
	require.NoError(t, err)
	grants := []domain.BusinessSystemGrant{{BusinessSystemID: system.ID, ToolGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL3, ConfirmL2: true}}}
	update := agentaction.NewUpdateExecutionAction(db)
	t.Run("需要审批的授权要求负责人", func(t *testing.T) {
		_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
		var fields *common.FieldError
		require.ErrorAs(t, err, &fields)
		require.Equal(t, agentaction.ValidationResponsibleRequired, fields.Fields["businessSystems"])
	})
	_, err = db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("responsible_user_id = ?", owner.User.ID).Where("id = ?", agent.ID).Exec(ctx)
	require.NoError(t, err)
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
	colleague := newChatLockUser(t, db, owner)

	t.Run("审批通过后执行并唤醒", func(t *testing.T) {
		call := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		require.Equal(t, string(domain.ToolInterventionApproval), *call.Intervention)
		require.Equal(t, fixture.subject(owner), *call.AssigneeSubjectID)
		require.Greater(t, time.Until(*call.ExpiresAt), 23*time.Hour)
		require.Empty(t, fixture.sentRequests(), "approval tool executed before decision")
		fixture.assertLatestReply(conversationID, agentcontract.SubmittedResult(domain.ToolInterventionApproval))
		pending, err := fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool {
			return item.ID == call.ID && item.CanDecide && item.ConversationReadable && item.ConversationID == conversationID && item.AgentName == "订单助手"
		}), "pending = %+v", pending)
		// 时间线卡片带上调用的当前内容与当前成员可执行的处理。
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, owner, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(history.Messages, func(message conversationaction.ConversationMessage) bool {
			event := message.SystemEvent
			return event != nil && event.Type == domain.ConversationSystemEventAgentToolCallPending && event.ToolCall != nil && event.ToolCall.ID == call.ID && event.ToolCall.CanDecide
		}), "timeline lacks pending card")
		require.ErrorIs(t, fixture.decisions.Decide(ctx, colleague, call.ID, true), tooldecisionaction.ErrToolCallUnavailable)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		require.ErrorIs(t, fixture.decisions.Decide(ctx, owner, call.ID, false), tooldecisionaction.ErrToolCallDecided)
		input := agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, input))
		require.Equal(t, []string{`POST /refunds {"amount":30}`}, fixture.sentRequests())
		executed := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallSucceeded), executed.Status)
		require.Equal(t, `{"ok":true}`, *executed.Result)
		require.Equal(t, fixture.subject(owner), *executed.DecidedBySubjectID)
		// 再次投递的执行任务不重复调用业务系统。
		require.NoError(t, fixture.decisions.ExecuteApproved(ctx, input))
		require.Len(t, fixture.sentRequests(), 1)
		fixture.runQueued(conversationID)
		fixture.assertLatestReply(conversationID, `"status":"succeeded"`)
		fixture.assertLatestReply(conversationID, `{\"ok\":true}`)
	})

	t.Run("发起人确认后在运行内执行", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		run := fixture.sendAndSuspend(conversationID, "改备注")
		call := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		require.Nil(t, call.Handover, "paused call handed over")
		require.Equal(t, string(domain.ToolInterventionConfirmation), *call.Intervention)
		require.Equal(t, fixture.subject(owner), *call.AssigneeSubjectID)
		require.Len(t, fixture.sentRequests(), 1, "confirmation tool executed before decision")
		// 发起人在应用中对话时不经渠道提醒，待处理列表与时间线卡片可以处理。
		require.Empty(t, tasks.Keyed(tooldecisionaction.ToolDecisionReminderActionName, "agent-tool-call-reminder:"+call.ID))
		pending, err := fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool { return item.ID == call.ID && item.CanDecide }), "pending = %+v", pending)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		require.ErrorIs(t, fixture.decisions.Decide(ctx, owner, call.ID, false), tooldecisionaction.ErrToolCallDecided)
		decided := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), decided.Status, "decision executed outside the run")
		require.NotNil(t, decided.DecidedAt)
		pending, err = fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.False(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool { return item.ID == call.ID }), "decided call still pending")
		// 运行执行之前时间线卡片按批准显示为待执行。
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, owner, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(history.Messages, func(message conversationaction.ConversationMessage) bool {
			event := message.SystemEvent
			return event != nil && event.ToolCall != nil && event.ToolCall.ID == call.ID && event.ToolCall.Status == domain.AgentToolCallQueued && !event.ToolCall.CanDecide
		}), "decided card not shown as queued")
		// 恢复的运行在同一轮内执行调用并以结果回答，不经结果事件唤醒新一轮。
		assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusQueued)
		fixture.runQueued(conversationID)
		require.Equal(t, `PATCH /notes {"amount":30}`, fixture.sentRequests()[1])
		executed := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallSucceeded), executed.Status)
		require.Equal(t, fixture.subject(owner), *executed.DecidedBySubjectID)
		fixture.assertLatestReply(conversationID, `结果：{"ok":true}`)
	})

	t.Run("发起人拒绝确认", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		fixture.sendAndSuspend(conversationID, "再改备注")
		call := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, false))
		fixture.runQueued(conversationID)
		rejected := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallRejected), rejected.Status)
		require.NotNil(t, rejected.DecidedAt)
		fixture.assertLatestReply(conversationID, "发起人拒绝执行这项操作")
		require.Len(t, fixture.sentRequests(), 2, "rejected tool executed")
	})

	t.Run("确认过期后按拒绝继续", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		fixture.sendAndSuspend(conversationID, "第三次改备注")
		call := fixture.latestCall(conversationID)
		_, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).Set("expires_at = now() - interval '1 second'").Where("id = ?", call.ID).Exec(ctx)
		require.NoError(t, err)
		require.NoError(t, fixture.decisions.Expire(ctx, agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}))
		fixture.runQueued(conversationID)
		expired := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallRejected), expired.Status)
		require.Nil(t, expired.DecidedBySubjectID)
		fixture.assertLatestReply(conversationID, "确认已过期")
		require.Len(t, fixture.sentRequests(), 2, "expired tool executed")
	})

	t.Run("撤销授权后确认按拒绝继续", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		fixture.sendAndSuspend(conversationID, "撤销前改备注")
		call := fixture.latestCall(conversationID)
		_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := update.Execute(context.Background(), owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
			require.NoError(t, err)
		})
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		fixture.runQueued(conversationID)
		rejected := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallRejected), rejected.Status)
		fixture.assertLatestReply(conversationID, "授权已变化")
		require.Len(t, fixture.sentRequests(), 2, "revoked tool executed")
		_, err = update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
		require.NoError(t, err)
	})

	t.Run("两个确认的调用之间撤销授权时第二个不执行", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		chat.mu.Lock()
		chat.calls = 2
		chat.mu.Unlock()
		defer func() { chat.mu.Lock(); chat.calls = 0; chat.mu.Unlock() }()
		run := fixture.sendAndSuspend(conversationID, "两次改备注")
		var calls []servermodels.AgentToolCall
		require.NoError(t, db.NewSelect().Model(&calls).Where("atc.agent_run_id = ?", run.ID).Where("atc.business_system_id IS NOT NULL").OrderExpr("atc.id").Scan(ctx))
		require.Len(t, calls, 2)
		for _, call := range calls {
			require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		}
		before := len(fixture.sentRequests())
		// 第一个请求到达业务系统时撤销授权，第二个调用执行前复核到授权已变化。
		changed := make(chan error, 1)
		var once sync.Once
		mu.Lock()
		onRequest = func() {
			once.Do(func() {
				_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
				changed <- err
			})
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			onRequest = nil
			mu.Unlock()
			_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
			require.NoError(t, err)
		}()
		fixture.runQueued(conversationID)
		require.NoError(t, <-changed)
		require.NoError(t, db.NewSelect().Model(&calls).Where("atc.agent_run_id = ?", run.ID).Where("atc.business_system_id IS NOT NULL").OrderExpr("atc.id").Scan(ctx))
		var succeeded, failed int
		for _, call := range calls {
			if call.Status == string(domain.AgentToolCallSucceeded) {
				succeeded++
			}
			if call.Status == string(domain.AgentToolCallFailed) {
				failed++
				require.Contains(t, *call.Error, "授权已变化")
			}
		}
		require.Equal(t, 1, succeeded)
		require.Equal(t, 1, failed)
		require.Len(t, fixture.sentRequests(), before+1)
	})

	t.Run("确认后工具改为需要审批时不执行", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		fixture.sendAndSuspend(conversationID, "改为审批前改备注")
		call := fixture.latestCall(conversationID)
		before := len(fixture.sentRequests())
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		reversible := false
		settings := map[string]domain.BusinessToolSetting{"update_note": {Reversible: &reversible}}
		_, err := db.NewUpdate().Model((*servermodels.BusinessSystem)(nil)).Set("tool_settings = ?", settings).Where("id = ?", system.ID).Exec(ctx)
		require.NoError(t, err)
		defer func() {
			_, err := db.NewUpdate().Model((*servermodels.BusinessSystem)(nil)).Set("tool_settings = '{}'::jsonb").Where("id = ?", system.ID).Exec(ctx)
			require.NoError(t, err)
		}()
		fixture.runQueued(conversationID)
		actual := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallFailed), actual.Status)
		require.Contains(t, *actual.Error, "授权已变化")
		require.Len(t, fixture.sentRequests(), before)
		fixture.assertLatestReply(conversationID, "授权已变化")
	})

	t.Run("确认后撤销授权再恢复时不执行", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		fixture.sendAndSuspend(conversationID, "确认后撤销授权")
		call := fixture.latestCall(conversationID)
		before := len(fixture.sentRequests())
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		// 确认之后、运行恢复之前撤销业务系统授权，运行内执行前按当前配置复核，调用记为失败并把原因交给模型。
		_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution})
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := update.Execute(context.Background(), owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
			require.NoError(t, err)
		})
		fixture.runQueued(conversationID)
		failed := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallFailed), failed.Status)
		require.Contains(t, *failed.Error, "授权已变化")
		require.Len(t, fixture.sentRequests(), before, "revoked tool executed after confirmation")
		_, err = update.Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{ExecutionInput: execution, BusinessSystems: grants})
		require.NoError(t, err)
	})

	t.Run("记下决定后延迟的待确认通知不下发", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		fixture.sendAndSuspend(conversationID, "拒绝后通知")
		call := fixture.latestCall(conversationID)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, false))
		feed := startRealtimeFeed(t, owner.Workspace.ID)
		require.NoError(t, usernotification.NewDeliverer(db, tasks).DeliverToolDecision(ctx, notificationtask.ToolDecisionInput{
			WorkspaceID: owner.Workspace.ID, UserID: owner.User.ID, ToolCallID: call.ID,
		}))
		require.Empty(t, feed.userNotices(t), "decided call notified again")
		fixture.runQueued(conversationID)
	})

	t.Run("确认后执行中停止回复记为待核对", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		run := fixture.sendAndSuspend(conversationID, "停止前改备注")
		call := fixture.latestCall(conversationID)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, true))
		// 恢复的运行已开始执行这次调用时成员停止回复。
		_, err := db.NewUpdate().Model((*servermodels.AgentRun)(nil)).Set("status = ?", domain.AgentRunStatusRunning).Where("id = ?", run.ID).Exec(ctx)
		require.NoError(t, err)
		_, err = db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).Set("status = ?", domain.AgentToolCallRunning).Where("id = ?", call.ID).Exec(ctx)
		require.NoError(t, err)
		status, err := fixture.runner.StopAgentReply(ctx, owner, conversationID, run.ID)
		require.NoError(t, err)
		require.Equal(t, domain.AgentRunStatusCancelled, status)
		require.Equal(t, string(domain.AgentToolCallNeedsReview), fixture.latestCall(conversationID).Status)
		require.NoError(t, fixture.decisions.Review(ctx, owner, call.ID))
	})

	t.Run("超过截止时间后过期", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］退款")
		fixture.sendAndRun(conversationID, "再退一次")
		call := fixture.latestCall(conversationID)
		input := agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}
		// 截止前的过期任务不改变调用。
		require.NoError(t, fixture.decisions.Expire(ctx, input))
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), fixture.latestCall(conversationID).Status)
		_, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).Set("expires_at = now() - interval '1 second'").Where("id = ?", call.ID).Exec(ctx)
		require.NoError(t, err)
		require.ErrorIs(t, fixture.decisions.Decide(ctx, owner, call.ID, true), tooldecisionaction.ErrToolCallDecided)
		require.NoError(t, fixture.decisions.Expire(ctx, input))
		expired := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallExpired), expired.Status)
		require.Nil(t, expired.DecidedBySubjectID)
		fixture.runQueued(conversationID)
		fixture.assertLatestReply(conversationID, `"status":"expired"`)
	})

	t.Run("负责人核对结果未知的调用", func(t *testing.T) {
		call := fixture.latestCall(conversationID)
		_, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).Set("status = ?", domain.AgentToolCallNeedsReview).Where("id = ?", call.ID).Exec(ctx)
		require.NoError(t, err)
		pending, err := fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool {
			return item.ID == call.ID && item.CanReview && !item.CanDecide
		}), "pending = %+v", pending)
		require.ErrorIs(t, fixture.decisions.Review(ctx, colleague, call.ID), tooldecisionaction.ErrToolCallUnavailable)
		require.NoError(t, fixture.decisions.Review(ctx, owner, call.ID))
		require.Equal(t, string(domain.AgentToolCallReviewed), fixture.latestCall(conversationID).Status)
		pending, err = fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.Empty(t, pending)
	})

	t.Run("负责人审批其他成员私聊中的操作", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］退款")
		private, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, colleague, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "帮我退款",
		})
		require.NoError(t, err)
		fixture.runQueued(private.Conversation.ID)
		call := fixture.latestCall(private.Conversation.ID)
		pending, err := fixture.decisions.List(ctx, owner)
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool {
			return item.ID == call.ID && item.CanDecide && !item.ConversationReadable && item.Arguments == `{"amount":30}`
		}), "pending = %+v", pending)
		require.NoError(t, fixture.decisions.Decide(ctx, owner, call.ID, false))
	})

	t.Run("负责人变更后取消待原负责人审批的操作", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］退款")
		fixture.sendAndRun(conversationID, "再退一笔")
		call := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		update := agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db))
		_, err := update.Execute(ctx, owner, agent.ID, agentaction.UpdateInput{DisplayName: "订单助手", WorkStatus: domain.WorkStatusWorking, ResponsibleUserID: colleague.User.ID})
		require.NoError(t, err)
		cancelled := fixture.latestCall(conversationID)
		require.Equal(t, string(domain.AgentToolCallCancelled), cancelled.Status)
		require.ErrorIs(t, fixture.decisions.Decide(ctx, owner, call.ID, true), tooldecisionaction.ErrToolCallDecided)
		require.NoError(t, fixture.decisions.Resolve(ctx, agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}))
		fixture.runQueued(conversationID)
		fixture.assertLatestReply(conversationID, `"status":"cancelled"`)
		_, err = update.Execute(ctx, owner, agent.ID, agentaction.UpdateInput{DisplayName: "订单助手", WorkStatus: domain.WorkStatusWorking, ResponsibleUserID: owner.User.ID})
		require.NoError(t, err)
	})

	t.Run("发起成员停用后取消待其确认的操作", func(t *testing.T) {
		chat.use("［业务系统 · 订单系统］修改备注")
		member := newChatLockUser(t, db, owner)
		private, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, member, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "改备注",
		})
		require.NoError(t, err)
		var run servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", private.Conversation.ID, domain.AgentRunStatusQueued).Scan(ctx))
		require.NoError(t, fixture.runner.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
		assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusWaiting)
		call := fixture.latestCall(private.Conversation.ID)
		require.Equal(t, fixture.subject(member), *call.AssigneeSubjectID)
		_, err = testUserStatusAction(db).Execute(ctx, owner, member.User.ID, domain.IdentityStatusInactive)
		require.NoError(t, err)
		require.Equal(t, string(domain.AgentToolCallCancelled), fixture.latestCall(private.Conversation.ID).Status)
		// 取消后的结果处理恢复暂停的运行，模型在同一轮看到取消说明。
		require.NoError(t, fixture.decisions.Resolve(ctx, agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: call.ID}))
		assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusQueued)
		fixture.runQueued(private.Conversation.ID)
		fixture.assertLatestReply(private.Conversation.ID, "操作已取消")
	})

	t.Run("授权需要审批时不能取消负责人", func(t *testing.T) {
		_, err := agentaction.NewUpdateAgentAction(db, testEnqueuer, nil).Execute(ctx, owner, agent.ID, agentaction.UpdateInput{
			DisplayName: "订单助手", WorkStatus: domain.WorkStatusWorking,
		})
		var fields *common.FieldError
		require.ErrorAs(t, err, &fields)
		require.Equal(t, agentaction.ValidationResponsibleRequired, fields.Fields["responsibleUserId"])
	})
}

// subject 返回成员的聊天主体编号。
func (f *decisionFixture) subject(member *servermodels.Identity) string {
	f.t.Helper()
	var subjectID string
	require.NoError(f.t, f.db.NewSelect().TableExpr("chat_subjects AS cs").Column("cs.id").
		Where("cs.workspace_id = ? AND cs.kind = ? AND cs.source_id = ?", member.Workspace.ID, domain.ChatSubjectKindWorkspaceIdentity, member.WorkspaceIdentity.ID).
		Scan(f.ctx, &subjectID))
	return subjectID
}

// runQueued 执行会话中排队的运行。
func (f *decisionFixture) runQueued(conversationID string) {
	f.t.Helper()
	var run servermodels.AgentRun
	require.NoError(f.t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(f.ctx))
	require.NoError(f.t, f.runner.Execute(f.ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(f.t, f.ctx, f.db, run.ID, domain.AgentRunStatusSucceeded)
}

// sendAndRun 由负责人在会话中发送消息并执行它触发的运行。
func (f *decisionFixture) sendAndRun(conversationID, body string) {
	f.t.Helper()
	_, err := f.send.Execute(f.ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body})
	require.NoError(f.t, err)
	f.runQueued(conversationID)
}

// sendAndSuspend 由负责人在会话中发送消息并执行它触发的运行，运行暂停等待确认后返回该运行。
func (f *decisionFixture) sendAndSuspend(conversationID, body string) servermodels.AgentRun {
	f.t.Helper()
	_, err := f.send.Execute(f.ctx, f.owner, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body})
	require.NoError(f.t, err)
	var run servermodels.AgentRun
	require.NoError(f.t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(f.ctx))
	require.NoError(f.t, f.runner.Execute(f.ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(f.t, f.ctx, f.db, run.ID, domain.AgentRunStatusWaiting)
	return run
}

// latestCall 读取会话中最近一次业务系统工具调用。
func (f *decisionFixture) latestCall(conversationID string) servermodels.AgentToolCall {
	f.t.Helper()
	var call servermodels.AgentToolCall
	require.NoError(f.t, f.db.NewSelect().Model(&call).
		Join("JOIN agent_runs AS agr ON agr.id = atc.agent_run_id").
		Where("agr.conversation_id = ? AND atc.business_system_id IS NOT NULL", conversationID).
		OrderExpr("atc.created_at DESC, atc.id DESC").Limit(1).Scan(f.ctx))
	return call
}

// sentRequests 返回业务系统收到的请求。
func (f *decisionFixture) sentRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(*f.requests)
}

// assertLatestReply 核对会话中 AI 员工最近一条回复包含指定文本。
func (f *decisionFixture) assertLatestReply(conversationID, text string) {
	f.t.Helper()
	var body string
	require.NoError(f.t, f.db.NewSelect().TableExpr("agent_runs AS agr").ColumnExpr("msg.body").
		Join("JOIN messages AS msg ON msg.id = agr.response_message_id").
		Where("agr.conversation_id = ?", conversationID).
		OrderExpr("agr.created_at DESC, agr.id DESC").Limit(1).Scan(f.ctx, &body))
	require.Contains(f.t, body, text)
}

// queryHook 在查询完成时执行测试同步点。
type queryHook struct {
	after func(context.Context, *bun.QueryEvent)
}

// BeforeQuery 保持查询上下文。
func (h queryHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context { return ctx }

// AfterQuery 执行测试同步点。
func (h queryHook) AfterQuery(ctx context.Context, e *bun.QueryEvent) { h.after(ctx, e) }

// TestDecidedComputerCallCheckLockOrder 验证暂停确认后的电脑调用复核与撤销电脑并发时都不因死锁失败：撤销确认时所用的电脑时二者按相同锁顺序串行；
// 确认后 AI 员工改用另一台电脑时，复核不锁定新电脑并按授权已变化拒绝，撤销新电脑照常完成。
func TestDecidedComputerCallCheckLockOrder(t *testing.T) {
	for _, item := range []struct {
		name   string
		rebind bool
		// lockQuery 是复核事务中让撤销开始并排队的加锁语句。
		lockQuery string
	}{
		{name: "撤销确认时所用的电脑", lockQuery: "FOR SHARE"},
		{name: "确认后改用另一台电脑再撤销它", rebind: true, lockQuery: "agents AS a"},
	} {
		t.Run(item.name, func(t *testing.T) {
			db, owner, _, modelID := newAIWorkspace(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			tasks := servertest.NewTasks()
			added, err := computeraction.NewCreateWorkspaceComputerAction(db).Execute(ctx, owner, "构建服务器")
			require.NoError(t, err)
			execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}}
			agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{DisplayName: "文件助手", Execution: execution})
			require.NoError(t, err)
			bind := func(computerID string) {
				t.Helper()
				_, err := agentaction.NewUpdateAgentAction(db, testEnqueuer, nil).Execute(ctx, owner, agent.ID, agentaction.UpdateInput{
					DisplayName: agent.DisplayName, WorkStatus: domain.WorkStatusWorking, ComputerID: computerID,
					ComputerGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL2, ConfirmL2: true},
				})
				require.NoError(t, err)
			}
			bind(added.Record.ID)
			runtime, err := agentruntime.New()
			require.NoError(t, err)
			chat := &scriptedComputerModel{}
			chat.use("write_file", `{"file_path":"notes.txt","content":"draft"}`)
			upstreams := fakeUpstreams(nil, nil)
			upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return chat, nil }
			fixture := &personalAgentFixture{t: t, ctx: ctx, db: db, identity: owner, computer: added, model: chat,
				runner: newTestAgentRun(db, tasks, runtime, modelcall.New(db, upstreams, nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil),
			}
			fixture.online()
			first, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)).Execute(ctx, owner, directchataction.FirstAgentTextMessageInput{
				ConversationID: uuid.NewV7().String(), AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "写文件",
			})
			require.NoError(t, err)
			var run servermodels.AgentRun
			require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ?", first.Conversation.ID).Scan(ctx))
			fixture.run(run.ID)
			fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
			call := fixture.computerCall(run.ID)
			decisions := newTestToolDecisions(db, tasks)
			require.NoError(t, decisions.Decide(ctx, owner, call.ID, true))
			revoked := added.Record.ID
			if item.rebind {
				second, err := computeraction.NewCreateWorkspaceComputerAction(db).Execute(ctx, owner, "第二台服务器")
				require.NoError(t, err)
				bind(second.Record.ID)
				revoked = second.Record.ID
			}
			// 复核执行到 lockQuery 后开始撤销电脑，等撤销在锁上排队后复核继续；锁顺序与撤销相反时二者在此交错下死锁。
			locked := make(chan struct{})
			var once sync.Once
			checkDB := db.WithQueryHook(queryHook{after: func(ctx context.Context, e *bun.QueryEvent) {
				if e.Err != nil || !strings.Contains(e.Query, "FOR SHARE") || !strings.Contains(e.Query, item.lockQuery) {
					return
				}
				once.Do(func() {
					close(locked)
					require.Eventually(t, func() bool {
						var waiting int
						require.NoError(t, db.NewRaw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(context.Background(), &waiting))
						return waiting > 0
					}, 5*time.Second, 20*time.Millisecond, "computer revoke did not wait on the lock")
				})
			}})
			revokeDone := make(chan error, 1)
			go func() {
				select {
				case <-locked:
				case <-ctx.Done():
					revokeDone <- ctx.Err()
					return
				}
				revokeDone <- computeraction.NewRevokeComputerAction(db, decisions).Execute(ctx, owner, revoked)
			}()
			failure, checkErr := tooldecisionaction.CheckDecidedCall(ctx, checkDB, agentrunaction.NewRunScopes(tasks), owner.Workspace.ID, call.ID)
			revokeErr := <-revokeDone
			require.NoError(t, checkErr, "decided computer call check deadlocked")
			require.NoError(t, revokeErr, "computer revoke deadlocked")
			if item.rebind {
				require.Contains(t, failure, "授权已变化")
			}
		})
	}
}
