//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// newTestBusinessSystem 插入 HTTP 接口位于 baseURL、带工具目录、请求头绑定与工具设置的业务系统，每个工具对应「POST /<工具名>」。
func newTestBusinessSystem(t *testing.T, db *bun.DB, identity *servermodels.Identity, baseURL string, headers map[string]domain.ContextValue, settings map[string]domain.BusinessToolSetting, tools ...domain.BusinessTool) *servermodels.BusinessSystem {
	t.Helper()
	for index := range tools {
		tools[index].HTTP = &domain.HTTPOperation{Method: "POST", Path: "/" + tools[index].Name, Parameters: []domain.HTTPParameter{}}
	}
	system := &servermodels.BusinessSystem{
		WorkspaceID: identity.Workspace.ID, Name: "业务系统-" + uuid.NewV7().String(), Transport: domain.BusinessSystemTransportHTTP,
		Connection:     domain.BusinessSystemConnection{HTTP: &domain.HTTPConnection{BaseURL: baseURL, Spec: "{}"}},
		Credential:     domain.BusinessSystemCredential{Kind: domain.BusinessSystemCredentialNone},
		HeaderBindings: headers, Tools: tools, ToolSettings: settings,
	}
	_, err := db.NewInsert().Model(system).Column("workspace_id", "name", "transport", "connection", "credential", "header_bindings", "tools", "tool_settings").Returning("id").Exec(context.Background())
	require.NoError(t, err)
	return system
}

// newHeaderRecorder 启动按请求路径记录最近一次请求头的 HTTP 接口。
func newHeaderRecorder(t *testing.T) (string, func(path string) http.Header) {
	t.Helper()
	var mu sync.Mutex
	recorded := make(map[string]http.Header)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		recorded[r.URL.Path] = r.Header.Clone()
		mu.Unlock()
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	return server.URL, func(path string) http.Header {
		mu.Lock()
		defer mu.Unlock()
		return recorded[path]
	}
}

// mountedTools 按业务系统名称汇总挂载的工具名称与级别。
func mountedTools(systems []agentcontract.BusinessSystem) map[string][]string {
	mounted := make(map[string][]string, len(systems))
	for _, system := range systems {
		for _, tool := range system.Tools {
			mounted[system.Name] = append(mounted[system.Name], tool.Name+":"+string(tool.Level))
		}
	}
	return mounted
}

// callMounted 经运行挂载的调用会话调用指定业务系统中的工具。
func callMounted(ctx context.Context, t *testing.T, systems []agentcontract.BusinessSystem, systemName, tool string) {
	t.Helper()
	index := slices.IndexFunc(systems, func(system agentcontract.BusinessSystem) bool { return system.Name == systemName })
	require.GreaterOrEqual(t, index, 0, "business system %s not mounted", systemName)
	_, err := systems[index].Caller.Call(ctx, tool, json.RawMessage(`{}`))
	require.NoError(t, err)
}

// TestServiceBusinessQueries 验证各场景按授权、工具事实与可信身份挂载业务系统工具，填入绑定值并提示客户登录，以及侧栏业务查询记录。
func TestServiceBusinessQueries(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	yes, no := true, false
	// 公开业务系统的查询为 L0、可撤销写操作为 L2，未标注的退款为 L3，超出授权的最高级别 L2 而不挂载。
	baseURL, recordedHeaders := newHeaderRecorder(t)
	publicSystem := newTestBusinessSystem(t, db, identity, baseURL, map[string]domain.ContextValue{"X-Conversation": domain.ContextValueConversationID}, nil,
		domain.BusinessTool{Name: "search_products", ReadOnlyHint: &yes},
		domain.BusinessTool{Name: "update_profile", ReadOnlyHint: &no, DestructiveHint: &no},
		domain.BusinessTool{Name: "refund"},
	)
	// 客户业务系统按参数绑定客户编号、按请求头绑定客户邮箱。
	customerSystem := newTestBusinessSystem(t, db, identity, baseURL, map[string]domain.ContextValue{"X-Customer-Email": domain.ContextValueCustomerEmail},
		map[string]domain.BusinessToolSetting{"get_order": {ParameterBindings: map[string]domain.ContextValue{"customerId": domain.ContextValueCustomerUserID}}},
		domain.BusinessTool{Name: "get_order", ReadOnlyHint: &yes, InputSchema: []byte(`{"type":"object","properties":{"customerId":{"type":"string"},"orderId":{"type":"string"}}}`)},
	)
	// 成员业务系统按参数绑定所服务成员的邮箱。
	memberSystem := newTestBusinessSystem(t, db, identity, baseURL, nil,
		map[string]domain.BusinessToolSetting{"my_tasks": {ParameterBindings: map[string]domain.ContextValue{"email": domain.ContextValueMemberEmail}}},
		domain.BusinessTool{Name: "my_tasks", ReadOnlyHint: &yes, InputSchema: []byte(`{"type":"object","properties":{"email":{"type":"string"}}}`)},
	)
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID, SystemInstruction: "查询订单并答复"}}
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "订单客服", Execution: execution})
	require.NoError(t, err)
	_, err = agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: execution, BusinessSystems: []domain.BusinessSystemGrant{
			{BusinessSystemID: publicSystem.ID, ToolGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL2}},
			{BusinessSystemID: customerSystem.ID, ToolGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL1}},
			{BusinessSystemID: memberSystem.ID, ToolGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL1}},
		},
	})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	scheduler := agentrunaction.NewScheduler(tasks)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "业务查询", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: created.IdentityID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, testEnqueuer, servertest.DisabledMail{})
	t.Run("内部对话", func(t *testing.T) {
		conversationID := uuid.NewV7().String()
		_, err := directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "查一下订单",
		})
		require.NoError(t, err)
		// AI 单聊提供成员身份与会话编号，不提供客户身份。
		runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			want := map[string][]string{
				publicSystem.Name: {"search_products:l0", "update_profile:l2"},
				memberSystem.Name: {"my_tasks:l1"},
			}
			require.Equal(t, want, mountedTools(request.BusinessSystems), "internal mounted")
			callMounted(ctx, t, request.BusinessSystems, publicSystem.Name, "search_products")
			require.Equal(t, conversationID, recordedHeaders("/search_products").Get("X-Conversation"), "public header")
			for _, system := range request.BusinessSystems {
				if system.Name == memberSystem.Name {
					require.Equal(t, identity.Account.Email, system.Tools[0].Bound["email"], "member binding")
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
			return agentruntime.RunResult{Content: "好的", EndSeq: claimed.EndSeq}, nil
		}}
		run := &servermodels.AgentRun{}
		require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
		require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	})
	userID := "user-" + uuid.NewV7().String()
	for _, scenario := range []struct {
		name     string
		input    customerchataction.WebsiteCustomerTextMessageInput
		verified bool
	}{
		{name: "已登录客户", verified: true, input: customerchataction.WebsiteCustomerTextMessageInput{
			ExternalID: "web-user:" + userID, Customer: &customerchataction.SignedCustomer{UserID: userID, Name: "Ada", Email: "ada@example.com"},
		}},
		{name: "匿名访客", input: customerchataction.WebsiteCustomerTextMessageInput{ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := scenario.input
			input.ChannelID, input.ClientMessageID, input.Body = channel.ID, uuid.NewV7().String(), "我的订单到哪了"
			received, err := receive.Execute(ctx, input)
			require.NoError(t, err)
			conversationID := received.Conversation.ID
			result, failure := `{"orders":[]}`, "订单系统超时"
			startedAt := time.Now().Add(-time.Minute)
			runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				want := map[string][]string{publicSystem.Name: {"search_products:l0", "update_profile:l2"}}
				if scenario.verified {
					want[customerSystem.Name] = []string{"get_order:l1"}
				}
				require.Equal(t, want, mountedTools(request.BusinessSystems), "customer mounted")
				loginRule := strings.Contains(request.Assignment.Instruction, "客户尚未通过企业的身份验证")
				require.NotEqual(t, scenario.verified, loginRule, "login rule")
				if scenario.verified {
					index := slices.IndexFunc(request.BusinessSystems, func(system agentcontract.BusinessSystem) bool { return system.Name == customerSystem.Name })
					customer := request.BusinessSystems[index]
					callMounted(ctx, t, request.BusinessSystems, customerSystem.Name, "get_order")
					require.Equal(t, "ada@example.com", recordedHeaders("/get_order").Get("X-Customer-Email"), "customer bindings header")
					require.Equal(t, userID, customer.Tools[0].Bound["customerId"], "customer bindings bound")
				}
				triggers, err := pendingTriggers(ctx, feed, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				blocks := []agentcontract.Block{
					{ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(), Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &agentcontract.ToolCall{
						ID: uuid.NewV7().String(), ModelCallID: uuid.NewV7().String(), Source: domain.AgentToolSourceBusinessSystem,
						CallID: "c1", Name: "search_products", Arguments: `{"q":"1001"}`, Error: &failure, Status: domain.AgentToolCallFailed, StartedAt: &startedAt,
						BusinessSystemID: publicSystem.ID, BusinessSystem: publicSystem.Name, Level: domain.OperationLevelL0,
					}}},
					{ID: uuid.NewV7().String(), Position: 2, ModelCallID: uuid.NewV7().String(), Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &agentcontract.ToolCall{
						ID: uuid.NewV7().String(), ModelCallID: uuid.NewV7().String(), Source: domain.AgentToolSourceBuiltin, Replayable: true,
						CallID: "c2", Name: agentruntime.KnowledgeToolName, Arguments: `{}`, Result: &result, Status: domain.AgentToolCallSucceeded, StartedAt: &startedAt, Evidence: true,
					}}},
					{ID: uuid.NewV7().String(), Position: 3, ModelCallID: uuid.NewV7().String(), Kind: domain.AgentRunBlockToolCall, Payload: agentcontract.BlockPayload{ToolCall: &agentcontract.ToolCall{
						ID: uuid.NewV7().String(), ModelCallID: uuid.NewV7().String(), Source: domain.AgentToolSourceBusinessSystem, Replayable: true,
						CallID: "c3", Name: "get_order", Arguments: `{"orderId":"1001"}`, Result: &result, Status: domain.AgentToolCallSucceeded, StartedAt: &startedAt, Evidence: true,
						BusinessSystemID: customerSystem.ID, BusinessSystem: customerSystem.Name, Level: domain.OperationLevelL1,
					}}},
				}
				return agentruntime.RunResult{Content: "没有查到订单", EndSeq: claimed.EndSeq, Blocks: runBlocks(blocks)}, nil
			}}
			run := &servermodels.AgentRun{}
			require.NoError(t, db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
			require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
			queries, err := conversationaction.NewListBusinessQueriesQuery(db).Execute(ctx, identity, conversationID)
			require.NoError(t, err)
			require.Len(t, queries, 2)
			first := queries[0].ToolCall
			require.Equal(t, "get_order", first.Name, "first query")
			require.True(t, first.Evidence, "first query")
			require.NotNil(t, first.Result, "first query")
			require.Equal(t, result, *first.Result, "first query")
			require.Equal(t, customerSystem.Name, first.BusinessSystem, "first query")
			require.Equal(t, domain.OperationLevelL1, first.Level, "first query")
			second := queries[1].ToolCall
			require.Equal(t, "search_products", second.Name, "second query")
			require.False(t, second.Evidence, "second query")
			require.NotNil(t, second.Error, "second query")
			require.Equal(t, failure, *second.Error, "second query")
			require.Equal(t, publicSystem.ID, second.BusinessSystemID, "second query")
			foreignOwner, _ := newQAFixture(t, db)
			_, err = conversationaction.NewListBusinessQueriesQuery(db).Execute(ctx, foreignOwner, conversationID)
			require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "foreign queries")
		})
	}
}
