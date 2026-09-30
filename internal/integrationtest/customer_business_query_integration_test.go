//go:build server

package integrationtest

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	mcpaction "github.com/runforyou-ai/luway/internal/actions/mcpserver"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// newBusinessMCPService 插入带工具目录与用途标记的 MCP 服务。
func newBusinessMCPService(t *testing.T, db *bun.DB, identity *servermodels.Identity, customerScoped bool, purposes map[string]domain.MCPToolPurpose, tools ...string) *servermodels.MCPServer {
	t.Helper()
	catalog := make([]domain.MCPTool, 0, len(tools))
	for _, name := range tools {
		catalog = append(catalog, domain.MCPTool{Name: name})
	}
	service := &servermodels.MCPServer{
		OrganizationID: identity.Organization.ID, Name: "业务系统-" + uuid.NewV7().String(),
		URL: "http://127.0.0.1:1/mcp", ServerType: domain.MCPServerTypeStreamableHTTP,
		Tools: catalog, ToolPurposes: purposes, CustomerScoped: customerScoped,
	}
	if _, err := db.NewInsert().Model(service).Column("organization_id", "name", "url", "server_type", "tools", "tool_purposes", "customer_scoped").Returning("id").Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return service
}

// TestServiceBusinessQueries 验证工具用途标记、各场景的业务工具挂载与客户请求头、未登录提示，以及侧栏业务查询记录。
func TestServiceBusinessQueries(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	t.Run("工具用途标记", func(t *testing.T) {
		id := newBusinessMCPService(t, db, identity, false, nil, "get_order", "cancel_order").ID
		mark := mcpaction.NewUpdateToolPurposeAction(db)
		record, err := mark.Execute(ctx, identity, id, mcpaction.ToolPurposeInput{ToolName: "get_order", Purpose: domain.MCPToolPurposeQuery})
		if err != nil || record.ToolPurposes["get_order"] != domain.MCPToolPurposeQuery {
			t.Fatalf("mark=%+v err=%v", record, err)
		}
		if _, err := mark.Execute(ctx, identity, id, mcpaction.ToolPurposeInput{ToolName: "cancel_order", Purpose: domain.MCPToolPurposeAction}); err != nil {
			t.Fatal(err)
		}
		record, err = mark.Execute(ctx, identity, id, mcpaction.ToolPurposeInput{ToolName: "cancel_order"})
		if err != nil || len(record.ToolPurposes) != 1 {
			t.Fatalf("clear=%+v err=%v", record, err)
		}
		if _, err := mark.Execute(ctx, identity, id, mcpaction.ToolPurposeInput{ToolName: "missing", Purpose: domain.MCPToolPurposeQuery}); !errors.Is(err, mcpaction.ErrToolNotFound) {
			t.Fatalf("missing tool: %v", err)
		}
		var fields *common.FieldError
		if _, err := mark.Execute(ctx, identity, id, mcpaction.ToolPurposeInput{ToolName: "get_order", Purpose: "write"}); !errors.As(err, &fields) {
			t.Fatalf("invalid purpose: %v", err)
		}
		foreignOwner, _ := newQAFixture(t, db)
		if _, err := mark.Execute(ctx, foreignOwner, id, mcpaction.ToolPurposeInput{ToolName: "get_order", Purpose: domain.MCPToolPurposeQuery}); !errors.Is(err, mcpaction.ErrNotFound) {
			t.Fatalf("foreign mark: %v", err)
		}
	})

	// 公开服务名称排在前面且有同名查询工具，按客户查询的服务仍须先挂载。
	publicService := newBusinessMCPService(t, db, identity, false,
		map[string]domain.MCPToolPurpose{"search_products": domain.MCPToolPurposeQuery, "get_order": domain.MCPToolPurposeQuery}, "search_products", "get_order", "unmarked_tool")
	customerService := newBusinessMCPService(t, db, identity, true,
		map[string]domain.MCPToolPurpose{"get_order": domain.MCPToolPurposeQuery, "cancel_order": domain.MCPToolPurposeAction}, "get_order", "cancel_order")
	actionService := newBusinessMCPService(t, db, identity, false,
		map[string]domain.MCPToolPurpose{"refund": domain.MCPToolPurposeAction}, "refund")
	execution := agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID, SystemInstruction: "查询订单并答复"}}
	created, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "订单客服", Execution: execution})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, created.ID, agentaction.UpdateExecutionInput{
		ExecutionInput: execution, MCPServerIDs: []string{customerService.ID, publicService.ID, actionService.ID},
	}); err != nil {
		t.Fatal(err)
	}
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler := agentrunaction.NewScheduler(tasks)
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "业务查询", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: created.IdentityID}, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	receive := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil)
	t.Run("内部对话", func(t *testing.T) {
		conversationID := uuid.NewV7().String()
		if _, err := directchataction.NewSendFirstAgentTextMessageAction(db, scheduler).Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: created.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "查一下订单",
		}); err != nil {
			t.Fatal(err)
		}
		// 内部对话不挂载按客户查询的服务，其他服务挂载全部工具。
		runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			names := make([]string, 0, len(request.MCPConnections))
			for _, server := range request.MCPConnections {
				if server.Tools != nil || len(server.Config.Headers) != 0 {
					t.Fatalf("internal server=%+v", server)
				}
				names = append(names, server.Name)
			}
			slices.Sort(names)
			if want := []string{actionService.Name, publicService.Name}; !slices.Equal(names, slices.Sorted(slices.Values(want))) {
				t.Fatalf("internal servers=%v", names)
			}
			triggers, err := feed.Peek(ctx, 0)
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
		if err := db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if err := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
	})
	userID := "user-" + uuid.NewV7().String()
	for _, scenario := range []struct {
		name     string
		input    customerchataction.WebsiteCustomerTextMessageInput
		verified bool
	}{
		{name: "已登录客户", verified: true, input: customerchataction.WebsiteCustomerTextMessageInput{
			ExternalID: "web-user:" + userID, Customer: &customerchataction.WebsiteCustomer{UserID: userID, Name: "Ada", Email: "ada@example.com"},
		}},
		{name: "匿名访客", input: customerchataction.WebsiteCustomerTextMessageInput{ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := scenario.input
			input.ChannelID, input.ClientMessageID, input.Body = channel.ID, uuid.NewV7().String(), "我的订单到哪了"
			received, err := receive.Execute(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			conversationID := received.Conversation.ID
			result, failure := `{"orders":[]}`, "订单系统超时"
			startedAt := time.Now().Add(-time.Minute)
			runtime := &testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				mounted := make(map[string]agentruntime.MCPServer)
				for _, server := range request.MCPConnections {
					mounted[server.Name] = server
				}
				if _, ok := mounted[actionService.Name]; ok {
					t.Fatal("service without query tools mounted")
				}
				// 按客户查询的服务挂载时，普通服务的同名工具不再挂载。
				publicTools := []string{"get_order", "search_products"}
				if scenario.verified {
					publicTools = []string{"search_products"}
				}
				if public := mounted[publicService.Name]; !slices.Equal(public.Tools, publicTools) || len(public.Config.Headers) != 0 {
					t.Fatalf("public service=%+v", public)
				}
				customer, ok := mounted[customerService.Name]
				loginRule := strings.Contains(request.Assignment.Instruction, "客户尚未在企业网站登录")
				if scenario.verified {
					want := map[string]string{mcpintegration.CustomerIDHeader: userID, mcpintegration.CustomerEmailHeader: "ada@example.com"}
					if !ok || request.MCPConnections[0].Name != customerService.Name || !slices.Equal(customer.Tools, []string{"get_order"}) || !maps.Equal(customer.Config.Headers, want) || loginRule {
						t.Fatalf("verified customer service=%+v mounted=%t loginRule=%t", customer, ok, loginRule)
					}
				} else if ok || !loginRule {
					t.Fatalf("anonymous customer service mounted=%t loginRule=%t", ok, loginRule)
				}
				triggers, err := feed.Peek(ctx, 0)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
				if err != nil {
					return agentruntime.RunResult{}, err
				}
				blocks := []agentruntime.Block{
					{ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(), Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &agentruntime.ToolCall{
						CallID: "c1", Name: "search_products", Arguments: `{"q":"1001"}`, Error: &failure, Status: domain.AgentToolCallFailed, StartedAt: &startedAt, MCPServer: publicService.Name,
					}}},
					{ID: uuid.NewV7().String(), Position: 2, ModelCallID: uuid.NewV7().String(), Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &agentruntime.ToolCall{
						CallID: "c2", Name: agentruntime.KnowledgeToolName, Arguments: `{}`, Result: &result, Status: domain.AgentToolCallSucceeded, StartedAt: &startedAt, Evidence: true,
					}}},
					{ID: uuid.NewV7().String(), Position: 3, ModelCallID: uuid.NewV7().String(), Kind: domain.AgentRunBlockToolCall, Payload: agentruntime.BlockPayload{ToolCall: &agentruntime.ToolCall{
						CallID: "c3", Name: "get_order", Arguments: `{"orderId":"1001"}`, Result: &result, Status: domain.AgentToolCallSucceeded, StartedAt: &startedAt, MCPServer: customerService.Name, Evidence: true,
					}}},
				}
				return agentruntime.RunResult{Content: "没有查到订单", EndSeq: claimed.EndSeq, Blocks: blocks}, nil
			}}
			run := &servermodels.AgentRun{}
			if err := db.NewSelect().Model(run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if err := agentrunaction.NewExecuteAction(db, tasks, runtime, testAttachmentReader(db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
				t.Fatal(err)
			}
			queries, err := conversationaction.NewListBusinessQueriesQuery(db).Execute(ctx, identity, conversationID)
			if err != nil || len(queries) != 2 {
				t.Fatalf("queries=%+v err=%v", queries, err)
			}
			if first := queries[0].ToolCall; first.Name != "get_order" || !first.Evidence || first.Result == nil || *first.Result != result || first.MCPServer != customerService.Name {
				t.Fatalf("first query=%+v", first)
			}
			if second := queries[1].ToolCall; second.Name != "search_products" || second.Evidence || second.Error == nil || *second.Error != failure {
				t.Fatalf("second query=%+v", second)
			}
			foreignOwner, _ := newQAFixture(t, db)
			if _, err := conversationaction.NewListBusinessQueriesQuery(db).Execute(ctx, foreignOwner, conversationID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
				t.Fatalf("foreign queries: %v", err)
			}
		})
	}
}
