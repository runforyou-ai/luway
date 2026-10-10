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
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestCustomerToolConfirmations 验证网页客服中需要确认的操作只在客户已核验时挂载并由客户本人确认：调用记录分开保存模型参数与绑定参数，
// 访客时间线带确认卡片，成员只能查看；客户确认后按合成的参数执行并唤醒 AI 员工，会话转给成员时取消待确认的操作。
func TestCustomerToolConfirmations(t *testing.T) {
	t.Parallel()
	db, owner, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	var mu sync.Mutex
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Path+" "+string(body))
		mu.Unlock()
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	t.Cleanup(server.Close)
	no := false
	addressSchema := json.RawMessage(`{"type":"object","properties":{"customerId":{"type":"string"},"address":{"type":"string","title":"新地址"}}}`)
	system := &servermodels.BusinessSystem{
		WorkspaceID: owner.Workspace.ID, Name: "会员系统", Transport: domain.BusinessSystemTransportHTTP,
		Connection: domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{BaseURL: server.URL, Spec: "{}"}},
		Credential: domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone},
		Tools: []domain.BusinessTool{{
			Name: "update_address", Description: "修改收货地址", ReadOnlyHint: &no, DestructiveHint: &no, InputSchema: addressSchema,
			HTTP: &domain.HTTPOperation{Method: "POST", Path: "/addresses", Parameters: []domain.HTTPParameter{
				{Name: "customerId", In: domain.HTTPParameterInBody, Key: "customerId"}, {Name: "address", In: domain.HTTPParameterInBody, Key: "address"},
			}},
		}},
		ToolSettings: map[string]domain.BusinessToolSetting{"update_address": {ParameterBindings: map[string]domain.ContextValue{"customerId": domain.ContextValueCustomerUserID}}},
	}
	_, err := db.NewInsert().Model(system).Column("workspace_id", "name", "transport", "connection", "credential", "tools", "tool_settings").Returning("id").Exec(ctx)
	require.NoError(t, err)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "处理会员资料"}}
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "会员客服", Execution: execution})
	require.NoError(t, err)
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, owner, agent.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: execution, BusinessSystems: []domain.BusinessSystemGrant{{BusinessSystemID: system.ID, MaxLevel: domain.OperationLevelL2, ConfirmL2: true}},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, owner, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "会员中心", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{})
	decisions := newTestToolDecisions(db, tasks)
	decide := customerchataction.NewDecideWebsiteToolCallAction(db, decisions)
	history := customerchataction.NewListWebsiteMessagesQuery(db)
	userID := "user-" + uuid.NewV7().String()
	customer := customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-user:" + userID, Customer: &customerchataction.SignedCustomer{UserID: userID, Name: "Ada", Email: "ada@example.com"},
	}

	// submit 以客户身份发送消息并执行它触发的运行：运行提交挂载的需要确认的工具，返回提交的调用编号；工具未挂载时返回空编号。
	submit := func(t *testing.T, input customerchataction.WebsiteCustomerTextMessageInput, conversationID *string) (string, string) {
		t.Helper()
		input.ClientMessageID, input.Body, input.ConversationID = uuid.NewV7().String(), "把收货地址改成北京", conversationID
		received, err := receive.Execute(ctx, input)
		require.NoError(t, err)
		callID := ""
		runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			blocks := make([]agentcontract.Block, 0, 1)
			for _, mounted := range request.BusinessSystems {
				for _, tool := range mounted.Tools {
					if tool.Intervention != domain.ToolInterventionConfirmation {
						continue
					}
					callID = uuid.NewV7().String()
					modelCallID := uuid.NewV7().String()
					call := agentcontract.ToolCall{
						ID: callID, ModelCallID: modelCallID, CallID: "call-" + callID, Name: tool.Name, Source: domain.AgentToolSourceBusinessSystem,
						Arguments: `{"address":"北京"}`, BoundArguments: tool.Bound, BusinessSystemID: mounted.ID, BusinessSystem: mounted.Name, Level: tool.Level,
						Intervention: tool.Intervention, Status: domain.AgentToolCallAwaitingDecision, SideEffects: true,
					}
					if err := request.Journal.SaveToolCall(ctx, runCall(call)); err != nil {
						return agentruntime.RunResult{}, err
					}
					blocks = append(blocks, agentcontract.Block{
						ID: uuid.NewV7().String(), Position: 1, ModelCallID: modelCallID, Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &call},
					})
				}
			}
			triggers, err := pendingTriggers(ctx, feed, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			return agentruntime.RunResult{Content: "已提交，请确认", EndSeq: claimed.EndSeq, Blocks: runBlocks(blocks)}, nil
		}}
		runQueuedWith(t, ctx, db, tasks, runtime, received.Conversation.ID)
		return received.Conversation.ID, callID
	}
	// visitorCalls 读取访客时间线中的操作确认事件与操作列表。
	visitorCalls := func(t *testing.T, input customerchataction.WebsiteCustomerTextMessageInput, conversationID string) ([]string, []customerchataction.VisitorToolCall) {
		t.Helper()
		page, err := history.Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channel.ID, ExternalID: input.ExternalID, ConversationID: conversationID})
		require.NoError(t, err)
		events := make([]string, 0)
		for _, message := range page.Messages {
			if message.Event != nil && message.Event.Type == customerchataction.VisitorEventToolConfirmation {
				events = append(events, message.Event.ToolCallID)
			}
		}
		return events, page.ToolCalls
	}

	t.Run("匿名访客不挂载需要确认的工具", func(t *testing.T) {
		anonymous := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")}
		_, callID := submit(t, anonymous, nil)
		require.Empty(t, callID)
	})

	conversationID, callID := submit(t, customer, nil)
	t.Run("客户确认后执行并唤醒", func(t *testing.T) {
		require.NotEmpty(t, callID)
		call := &servermodels.AgentToolCall{}
		require.NoError(t, db.NewSelect().Model(call).Where("atc.id = ?", callID).Scan(ctx))
		require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
		require.Equal(t, `{"address":"北京"}`, call.Arguments)
		require.Equal(t, map[string]string{"customerId": userID}, call.BoundArguments)
		service, err := loadServiceConversation(ctx, db, conversationID)
		require.NoError(t, err)
		require.Equal(t, service.RequesterSubjectID, *call.AssigneeSubjectID)

		// 访客时间线带确认事件与可确认的操作，参数只含模型给出的参数并带标题。
		events, calls := visitorCalls(t, customer, conversationID)
		require.Equal(t, []string{callID}, events)
		require.Len(t, calls, 1)
		require.True(t, calls[0].Decidable)
		require.Equal(t, `{"address":"北京"}`, calls[0].Arguments)
		require.Equal(t, map[string]string{"address": "新地址"}, calls[0].ArgumentTitles)

		// 成员只能查看客户的确认，不进入成员的待处理列表。
		pending, err := decisions.List(ctx, owner)
		require.NoError(t, err)
		require.False(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool { return item.ID == callID }))
		timeline, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, owner, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
		require.NoError(t, err)
		require.True(t, slices.ContainsFunc(timeline.Messages, func(message conversationaction.ConversationMessage) bool {
			event := message.SystemEvent
			return event != nil && event.ToolCall != nil && event.ToolCall.ID == callID && !event.ToolCall.CanDecide &&
				event.ToolCall.AssigneeName != nil && *event.ToolCall.AssigneeName == "Ada"
		}), "member timeline lacks read-only card")
		require.ErrorIs(t, decisions.Decide(ctx, owner, callID, true), tooldecisionaction.ErrToolCallUnavailable)

		// 其他访客无法裁决这位客户的操作。
		other := customerchataction.WebsiteToolCallDecisionInput{ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ConversationID: conversationID, ToolCallID: callID, Approve: true}
		require.ErrorIs(t, decide.Execute(ctx, other), conversationaction.ErrConversationNotFound)

		approve := customerchataction.WebsiteToolCallDecisionInput{ChannelID: channel.ID, ExternalID: customer.ExternalID, ConversationID: conversationID, ToolCallID: callID, Approve: true}
		require.NoError(t, decide.Execute(ctx, approve))
		require.ErrorIs(t, decide.Execute(ctx, approve), tooldecisionaction.ErrToolCallDecided)
		require.NoError(t, decisions.ExecuteApproved(ctx, agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: callID}))
		mu.Lock()
		require.Equal(t, []string{`POST /addresses {"address":"北京","customerId":"` + userID + `"}`}, requests)
		mu.Unlock()
		executed := &servermodels.AgentToolCall{}
		require.NoError(t, db.NewSelect().Model(executed).Where("atc.id = ?", callID).Scan(ctx))
		require.Equal(t, string(domain.AgentToolCallSucceeded), executed.Status)
		require.Equal(t, service.RequesterSubjectID, *executed.DecidedBySubjectID)
		_, calls = visitorCalls(t, customer, conversationID)
		require.Equal(t, string(domain.AgentToolCallSucceeded), string(calls[0].Status))
		require.False(t, calls[0].Decidable)

		// 结果事件唤醒 AI 员工新一轮运行，事件进入模型上下文。
		resolved := false
		runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			triggers, err := pendingTriggers(ctx, feed, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			resolved = slices.ContainsFunc(claimed.Messages, func(message einorun.Message) bool {
				return strings.Contains(message.Content, `"kind":"tool_call_resolved"`) && strings.Contains(message.Content, `"status":"succeeded"`)
			})
			return agentruntime.RunResult{Content: "地址已修改", EndSeq: claimed.EndSeq}, nil
		}}
		runQueuedWith(t, ctx, db, tasks, runtime, conversationID)
		require.True(t, resolved, "resolved event not delivered to model")
	})

	t.Run("会话转给成员后取消待客户确认的操作", func(t *testing.T) {
		_, callID := submit(t, customer, &conversationID)
		require.NotEmpty(t, callID)
		_, err := servicesessionaction.NewClaimServiceSessionAction(db, testServiceSessionReturner(db), testEnqueuer).Execute(ctx, owner, conversationID)
		require.NoError(t, err)
		cancelled := &servermodels.AgentToolCall{}
		require.NoError(t, db.NewSelect().Model(cancelled).Where("atc.id = ?", callID).Scan(ctx))
		require.Equal(t, string(domain.AgentToolCallCancelled), cancelled.Status)
		reject := customerchataction.WebsiteToolCallDecisionInput{ChannelID: channel.ID, ExternalID: customer.ExternalID, ConversationID: conversationID, ToolCallID: callID}
		require.ErrorIs(t, decide.Execute(ctx, reject), tooldecisionaction.ErrToolCallDecided)
		require.NoError(t, decisions.Resolve(ctx, agentprocess.ToolCallInput{WorkspaceID: owner.Workspace.ID, ToolCallID: callID}))
		_, calls := visitorCalls(t, customer, conversationID)
		index := slices.IndexFunc(calls, func(call customerchataction.VisitorToolCall) bool { return call.ID == callID })
		require.GreaterOrEqual(t, index, 0)
		require.Equal(t, domain.AgentToolCallCancelled, calls[index].Status)
	})
}

// runQueuedWith 用指定运行时执行会话中排队的运行。
func runQueuedWith(t *testing.T, ctx context.Context, db *bun.DB, tasks *servertest.Tasks, runtime agentruntime.Runtime, conversationID string) {
	t.Helper()
	run := &servermodels.AgentRun{}
	require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
	require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	assertAgentRunStatus(t, ctx, db, run.ID, domain.AgentRunStatusSucceeded)
}

// loadServiceConversation 读取客服会话。
func loadServiceConversation(ctx context.Context, db *bun.DB, conversationID string) (*servermodels.ServiceConversation, error) {
	service := &servermodels.ServiceConversation{}
	return service, db.NewSelect().Model(service).Where("svc.conversation_id = ?", conversationID).Scan(ctx)
}
