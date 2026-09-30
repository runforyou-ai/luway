//go:build server

package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	knowledgeaction "github.com/runforyou-ai/luway/internal/actions/knowledgebase"
	mcpserveraction "github.com/runforyou-ai/luway/internal/actions/mcpserver"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/luway/internal/integration/knowledgeretrieval"
	mcpintegration "github.com/runforyou-ai/luway/internal/integration/mcp"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// deviceRunFixture 是设备执行集成测试共用的助理、设备与调用入口。
type deviceRunFixture struct {
	t         *testing.T
	ctx       context.Context
	db        *bun.DB
	identity  *servermodels.Identity
	assistant *agentaction.Assistant
	tasks     *servertask.Runtime
	executor  *agentrunaction.ExecuteAction
	sendFirst *directchataction.SendFirstAgentTextMessageAction
	send      *directchataction.SendAgentTextMessageAction
	device    agentrunaction.RunDevice
}

// testDeviceAgentRuns 验证助理的运行派发到其绑定电脑，以及领取、并行、停止、租约过期、换电脑、暂停与失去执行条件的收敛；AI 员工的运行始终在服务端执行。
func testDeviceAgentRuns(t *testing.T, db *bun.DB, identity *servermodels.Identity, employee *agentaction.Agent, tasks *servertask.Runtime) {
	ctx := context.Background()
	installID := uuid.NewV7().String()
	registered, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, identity, deviceaction.RegisterInput{InstallID: installID, Name: "测试电脑", Platform: domain.DevicePlatformMacOS})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := agentaction.NewCreateAssistantAction(db).Execute(ctx, identity, registered.ID, agentaction.AssistantInput{
		DisplayName: "小码", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ProviderID: employee.Execution.Managed.ProviderID, ModelIdentifier: employee.Execution.Managed.ModelIdentifier,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &deviceRunFixture{
		t: t, ctx: ctx, db: db, identity: identity, assistant: assistant, tasks: tasks,
		executor:  agentrunaction.NewExecuteAction(db, tasks, nil, testAttachmentReader(db), nil, nil),
		sendFirst: directchataction.NewSendFirstAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)),
		send:      directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)),
		device:    agentrunaction.RunDevice{OrganizationID: identity.Organization.ID, UserID: identity.User.ID, DeviceID: registered.ID},
	}
	t.Run("助理归属主人", func(t *testing.T) {
		if assistant.OwnerUserID != identity.User.ID || assistant.DeviceID != registered.ID || assistant.Presence(time.Now()) != domain.AssistantPresenceOffline {
			t.Fatalf("assistant=%+v", assistant)
		}
		other := newChatLockUser(t, db, identity)
		// 其他成员不能在别人的电脑上创建助理，也不能与别人的助理单聊。
		if _, err := agentaction.NewCreateAssistantAction(db).Execute(ctx, other, registered.ID, agentaction.AssistantInput{
			DisplayName: "冒用", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: employee.Execution.Managed.ProviderID, ModelIdentifier: employee.Execution.Managed.ModelIdentifier}},
		}); !errors.Is(err, agentaction.ErrAssistantDeviceNotFound) {
			t.Fatalf("create on foreign device=%v", err)
		}
		if _, err := fixture.sendFirst.Execute(ctx, other, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: assistant.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "借用",
		}); !errors.Is(err, conversationaction.ErrAgentTargetNotFound) {
			t.Fatalf("foreign assistant chat=%v", err)
		}
		// AI 员工管理入口不能修改助理。
		if _, err := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(ctx, identity, assistant.ID, domain.IdentityStatusInactive); err == nil {
			t.Fatal("agent status action changed assistant")
		}
		if _, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, assistant.ID); !errors.Is(err, agentaction.ErrNotFound) {
			t.Fatalf("agent query read assistant=%v", err)
		}
	})

	t.Run("AI 员工在服务端执行", func(t *testing.T) {
		_, run := createAgentLockChat(t, ctx, db, identity, employee.IdentityID, tasks)
		if run.ExecutionDeviceID != nil {
			t.Fatalf("employee run dispatched to device=%+v", run)
		}
		if _, err := fixture.executor.StopAgentReply(ctx, identity, run.ConversationID, run.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("设备认证", func(t *testing.T) {
		other := newChatLockUser(t, db, identity)
		if _, err := deviceaction.NewAuthenticateDeviceAction(db).Execute(ctx, other, registered.ID); !errors.Is(err, deviceaction.ErrNotFound) {
			t.Fatalf("other member device auth=%v", err)
		}
	})

	t.Run("草稿首发即派发到绑定电脑", func(t *testing.T) {
		conversationID := uuid.NewV7().String()
		if _, err := fixture.sendFirst.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: assistant.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "首条就在本机执行",
		}); err != nil {
			t.Fatal(err)
		}
		var run servermodels.AgentRun
		if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ?", conversationID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if run.ExecutionDeviceID == nil || *run.ExecutionDeviceID != registered.ID {
			t.Fatalf("first run=%+v", run)
		}
		if count, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("tr.idempotency_key = ?", "agent:"+run.ID).Count(ctx); err != nil || count != 0 {
			t.Fatalf("first device run enqueued server task=%d %v", count, err)
		}
		work, err := fixture.executor.DeviceWork(ctx, fixture.device)
		if err != nil || !deviceWorkContains(work, run.ID) {
			t.Fatalf("device work=%+v %v", work, err)
		}
		fixture.claimAndComplete(run.ID, "首条的回复")
	})

	t.Run("运行期知识检索与附件读取", func(t *testing.T) {
		base, err := knowledgeaction.NewCreateKnowledgeBaseAction(db).Execute(ctx, identity, newKnowledgeBaseInput(t, db, identity, "设备资料", domain.KnowledgeBaseCategoryStandard))
		if err != nil {
			t.Fatal(err)
		}
		bindKnowledge := func(ids []string) {
			t.Helper()
			if _, err := agentaction.NewUpdateAssistantAction(db).Execute(ctx, identity, assistant.ID, agentaction.AssistantInput{
				DisplayName: assistant.DisplayName, Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
					ProviderID: employee.Execution.Managed.ProviderID, ModelIdentifier: employee.Execution.Managed.ModelIdentifier, KnowledgeBaseIDs: ids,
				}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		bindKnowledge([]string{base.ID})
		defer bindKnowledge(nil)
		executor := agentrunaction.NewExecuteAction(db, tasks, nil, testAttachmentReader(db), testDeviceKnowledge{}, nil)
		conversationID := fixture.assistantChat()
		sent, err := directchataction.NewSendAttachmentMessageAction(db, agentrunaction.NewScheduler(tasks)).Execute(ctx, identity, directchataction.AttachmentMessageInput{
			ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "看看截图",
			FileID: uploadedAttachment(t, db, identity, "screen.png", "image/png"),
		})
		if err != nil {
			t.Fatal(err)
		}
		var run servermodels.AgentRun
		if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		// 领取前不能读取运行期资料。
		if _, err := executor.SearchDeviceRunKnowledge(ctx, fixture.device, run.ID, knowledgeretrieval.Request{Queries: []string{"退款"}}); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("search before claim=%v", err)
		}
		claim, err := executor.ClaimDeviceRun(ctx, fixture.device, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var assignment agentruntime.Assignment
		if err := json.Unmarshal(claim.Assignment, &assignment); err != nil || !slices.Contains(assignment.Tools, agentruntime.KnowledgeToolName) {
			t.Fatalf("assignment=%+v %v", assignment, err)
		}
		result, err := executor.SearchDeviceRunKnowledge(ctx, fixture.device, run.ID, knowledgeretrieval.Request{Queries: []string{"退款"}})
		if err != nil || len(result.Records) != 1 || result.Records[0].KnowledgeBaseID != base.ID || result.Records[0].Content != "设备资料：退款" {
			t.Fatalf("search=%+v %v", result, err)
		}
		content, err := executor.ReadDeviceRunAttachment(ctx, fixture.device, run.ID, sent.Message.ID)
		if err != nil || string(content) != "content:screen.png" {
			t.Fatalf("attachment=%q %v", content, err)
		}
		// 其他会话的附件与其他设备都读不到。
		if _, err := executor.ReadDeviceRunAttachment(ctx, fixture.device, run.ID, uuid.NewV7().String()); !errors.Is(err, agentrunaction.ErrAttachmentUnavailable) {
			t.Fatalf("foreign attachment=%v", err)
		}
		otherDevice := fixture.device
		otherDevice.DeviceID = uuid.NewV7().String()
		if _, err := executor.ReadDeviceRunAttachment(ctx, otherDevice, run.ID, sent.Message.ID); !errors.Is(err, agentrunaction.ErrDeviceRunNotFound) {
			t.Fatalf("other device attachment=%v", err)
		}
		// 由领取运行的执行器收尾，释放它持有的输入状态发布。
		knowledgeFixture := *fixture
		knowledgeFixture.executor = executor
		knowledgeFixture.complete(run.ID, "已查阅资料")
	})

	t.Run("企业 MCP 服务经服务端代理", func(t *testing.T) {
		var authorization atomic.Value
		server := sdk.NewServer(&sdk.Implementation{Name: "orders", Version: "1"}, nil)
		server.AddTool(&sdk.Tool{Name: "get_order", Description: "查询订单", InputSchema: map[string]any{"type": "object"}},
			func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "订单参数 " + string(request.Params.Arguments)}}}, nil
			})
		handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
		// 统计建立与关闭的 MCP 会话数。
		var sessionsOpened, sessionsClosed atomic.Int32
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorization.Store(r.Header.Get("Authorization"))
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if r.Method == http.MethodPost && bytes.Contains(body, []byte(`"method":"initialize"`)) {
				sessionsOpened.Add(1)
			}
			if r.Method == http.MethodDelete {
				sessionsClosed.Add(1)
			}
			handler.ServeHTTP(w, r)
		}))
		defer endpoint.Close()
		insertService := func(customerScoped bool) *servermodels.MCPServer {
			t.Helper()
			service := &servermodels.MCPServer{
				OrganizationID: identity.Organization.ID, Name: "订单系统 " + uuid.NewV7().String(), URL: endpoint.URL,
				ServerType: domain.MCPServerTypeStreamableHTTP, AuthorizationToken: "device-proxy-token", CustomerScoped: customerScoped,
			}
			if _, err := db.NewInsert().Model(service).Column("organization_id", "name", "url", "server_type", "authorization_token", "customer_scoped").Returning("id").Exec(ctx); err != nil {
				t.Fatal(err)
			}
			return service
		}
		orders, customerOrders := insertService(false), insertService(true)
		offline := &servermodels.MCPServer{
			OrganizationID: identity.Organization.ID, Name: "离线系统 " + uuid.NewV7().String(), URL: "http://127.0.0.1:1/mcp", ServerType: domain.MCPServerTypeStreamableHTTP,
		}
		if _, err := db.NewInsert().Model(offline).Column("organization_id", "name", "url", "server_type").Returning("id").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		bindMCP := func(ids []string) error {
			t.Helper()
			_, err := agentaction.NewUpdateAssistantAction(db).Execute(ctx, identity, assistant.ID, agentaction.AssistantInput{
				DisplayName: assistant.DisplayName, MCPServerIDs: ids, Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
					ProviderID: employee.Execution.Managed.ProviderID, ModelIdentifier: employee.Execution.Managed.ModelIdentifier,
				}},
			})
			return err
		}
		// 助理不接待客户，不能绑定按客户查询的服务。
		var fieldErr *common.FieldError
		if err := bindMCP([]string{customerOrders.ID}); !errors.As(err, &fieldErr) || fieldErr.Fields["mcpServerIds"] != agentaction.ValidationMCPServerInvalid {
			t.Fatalf("bind customer scoped service=%v", err)
		}
		if err := bindMCP([]string{orders.ID, offline.ID}); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := bindMCP(nil); err != nil {
				t.Fatal(err)
			}
		}()
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "查一下订单")
		if _, err := fixture.executor.ListDeviceRunMCPTools(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("list before claim=%v", err)
		}
		claim, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var assignment agentruntime.Assignment
		if err := json.Unmarshal(claim.Assignment, &assignment); err != nil || !slices.Equal(assignment.MCPServers, slices.Sorted(slices.Values([]string{orders.Name, offline.Name}))) {
			t.Fatalf("assignment mcp servers=%v %v", assignment.MCPServers, err)
		}
		servers, err := fixture.executor.ListDeviceRunMCPTools(ctx, fixture.device, run.ID)
		if err != nil || len(servers) != 1 || servers[0].ID != orders.ID || servers[0].Name != orders.Name ||
			len(servers[0].Tools) != 1 || servers[0].Tools[0].Name != "get_order" {
			t.Fatalf("list=%+v %v", servers, err)
		}
		result, err := fixture.executor.CallDeviceRunMCPTool(ctx, fixture.device, run.ID, orders.ID, "get_order", json.RawMessage(`{"id":"A1"}`))
		if err != nil || result.Error != "" || result.Result != `订单参数 {"id":"A1"}` || authorization.Load() != "Bearer device-proxy-token" {
			t.Fatalf("call=%+v %v authorization=%v", result, err, authorization.Load())
		}
		// 读取目录与多次调用复用同一次运行内建立的连接。
		if again, err := fixture.executor.CallDeviceRunMCPTool(ctx, fixture.device, run.ID, orders.ID, "get_order", json.RawMessage(`{"id":"A2"}`)); err != nil || again.Result != `订单参数 {"id":"A2"}` {
			t.Fatalf("second call=%+v %v", again, err)
		}
		if opened := sessionsOpened.Load(); opened != 1 {
			t.Fatalf("mcp sessions opened=%d", opened)
		}
		// 不可用服务的失败只说明服务名称与失败类型，不含服务地址。
		failed, err := fixture.executor.CallDeviceRunMCPTool(ctx, fixture.device, run.ID, offline.ID, "get_order", json.RawMessage(`{}`))
		if err != nil || !strings.Contains(failed.Error, offline.Name) || strings.Contains(failed.Error, "127.0.0.1") {
			t.Fatalf("offline call=%+v %v", failed, err)
		}
		// 未绑定的服务与其他设备都调用不到。
		if _, err := fixture.executor.CallDeviceRunMCPTool(ctx, fixture.device, run.ID, customerOrders.ID, "get_order", json.RawMessage(`{}`)); !errors.Is(err, agentrunaction.ErrDeviceRunMCPToolNotFound) {
			t.Fatalf("call unbound service=%v", err)
		}
		otherDevice := fixture.device
		otherDevice.DeviceID = uuid.NewV7().String()
		if _, err := fixture.executor.CallDeviceRunMCPTool(ctx, otherDevice, run.ID, orders.ID, "get_order", json.RawMessage(`{}`)); !errors.Is(err, agentrunaction.ErrDeviceRunNotFound) {
			t.Fatalf("other device call=%v", err)
		}
		fixture.complete(run.ID, "订单已查到")
		// 运行结束时关闭已建立的连接。
		if closed := sessionsClosed.Load(); closed != 1 {
			t.Fatalf("mcp sessions closed=%d", closed)
		}
		if _, err := fixture.executor.CallDeviceRunMCPTool(ctx, fixture.device, run.ID, orders.ID, "get_order", json.RawMessage(`{}`)); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("call after completion=%v", err)
		}
		// 服务改为按客户查询后从助理的配置中移除，AI 员工保留。
		bindEmployee := func(ids []string) {
			t.Helper()
			if _, err := agentaction.NewUpdateExecutionAction(db).Execute(ctx, identity, employee.ID, agentaction.UpdateExecutionInput{
				ExecutionInput: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
					ProviderID: employee.Execution.Managed.ProviderID, ModelIdentifier: employee.Execution.Managed.ModelIdentifier,
					SystemInstruction: employee.Execution.Managed.SystemInstruction, KnowledgeBaseIDs: employee.Execution.Managed.KnowledgeBaseIDs,
				}},
				MCPServerIDs: ids,
			}); err != nil {
				t.Fatal(err)
			}
		}
		bindEmployee([]string{orders.ID})
		defer bindEmployee(employee.Execution.MCPServerIDs)
		discover := mcpDiscoverFunc(func(context.Context, mcpintegration.Config) ([]domain.MCPTool, error) { return []domain.MCPTool{}, nil })
		probe := mcpserveraction.NewTestConnectionAction(discover)
		if _, err := mcpserveraction.NewUpdateMCPServerAction(db, probe).Execute(ctx, identity, orders.ID, mcpserveraction.Input{
			Name: orders.Name, URL: orders.URL, ServerType: orders.ServerType, AuthorizationToken: orders.AuthorizationToken, CustomerScoped: true,
		}); err != nil {
			t.Fatal(err)
		}
		_, assistantExecution, err := agentaction.NewGetAssistantQuery(db).Execute(ctx, identity, assistant.ID)
		if err != nil || !slices.Equal(assistantExecution.MCPServerIDs, []string{offline.ID}) {
			t.Fatalf("assistant mcp servers=%v %v", assistantExecution.MCPServerIDs, err)
		}
		reloaded, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, employee.ID)
		if err != nil || !slices.Contains(reloaded.Execution.MCPServerIDs, orders.ID) {
			t.Fatalf("employee mcp servers=%v %v", reloaded.Execution.MCPServerIDs, err)
		}
	})

	t.Run("设备运行下发全部本机工具", func(t *testing.T) {
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "看看文件")
		claim, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var assignment agentruntime.Assignment
		if err := json.Unmarshal(claim.Assignment, &assignment); err != nil {
			t.Fatal(err)
		}
		for _, name := range agentruntime.LocalTools() {
			if !slices.Contains(assignment.Tools, name) {
				t.Fatalf("device run tools=%v", assignment.Tools)
			}
		}
		// 网页在本机读取；企业未启用联网搜索时不下发搜索工具，服务端也拒绝代为搜索。
		if !slices.Contains(assignment.Tools, agentruntime.WebFetchToolName) || slices.Contains(assignment.Tools, agentruntime.WebSearchToolName) {
			t.Fatalf("device run web tools=%v", assignment.Tools)
		}
		if _, err := fixture.executor.SearchDeviceRunWeb(ctx, fixture.device, run.ID, websearch.Request{Query: "退款"}); !errors.Is(err, agentrunaction.ErrWebSearchDisabled) {
			t.Fatalf("web search without settings=%v", err)
		}
		fixture.complete(run.ID, "已查看")
	})

	t.Run("助理记忆", func(t *testing.T) {
		testAssistantMemory(t, fixture)
	})

	t.Run("派发领取与并行", func(t *testing.T) {
		before := fixture.workSeq()
		conversationID := fixture.assistantChat()
		run := fixture.sendAndLoadRun(conversationID, "在本机执行")
		if run.ExecutionDeviceID == nil || *run.ExecutionDeviceID != registered.ID {
			t.Fatalf("device run=%+v", run)
		}
		if count, err := db.NewSelect().Model((*servermodels.TaskRun)(nil)).Where("tr.idempotency_key = ?", "agent:"+run.ID).Count(ctx); err != nil || count != 0 {
			t.Fatalf("device run enqueued server task=%d %v", count, err)
		}
		if fixture.workSeq() <= before {
			t.Fatal("device work sequence did not advance")
		}
		work, err := fixture.executor.DeviceWork(ctx, fixture.device)
		if err != nil || !deviceWorkContains(work, run.ID) {
			t.Fatalf("device work=%+v %v", work, err)
		}
		if _, err := fixture.executor.ClaimDeviceRun(ctx, agentrunaction.RunDevice{OrganizationID: identity.Organization.ID, UserID: identity.User.ID, DeviceID: uuid.NewV7().String()}, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunNotFound) {
			t.Fatalf("foreign device claim=%v", err)
		}
		claim, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID)
		if err != nil || len(claim.Assignment) == 0 {
			t.Fatalf("claim=%+v %v", claim, err)
		}
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunUnavailable) {
			t.Fatalf("repeated claim=%v", err)
		}
		// 持有租约的设备取得配置版本锁定的模型服务，其他设备取不到。
		upstream, err := fixture.executor.ResolveDeviceModelUpstream(ctx, fixture.device, run.ID)
		if err != nil || upstream.Brand == "" || upstream.BaseURL == "" || upstream.Identifier == "" {
			t.Fatalf("model upstream=%+v %v", upstream, err)
		}
		if _, err := fixture.executor.ResolveDeviceModelUpstream(ctx, agentrunaction.RunDevice{OrganizationID: identity.Organization.ID, UserID: identity.User.ID, DeviceID: uuid.NewV7().String()}, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunNotFound) {
			t.Fatalf("foreign device model upstream=%v", err)
		}
		// 其他会话的运行不必等待，可以同时领取。
		waiting := fixture.sendAndLoadRun(fixture.assistantChat(), "并行")
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, waiting.ID); err != nil {
			t.Fatalf("parallel claim=%v", err)
		}
		fixture.complete(run.ID, "本机回复")
		if _, err := fixture.executor.ResolveDeviceModelUpstream(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("model upstream after completion=%v", err)
		}
		var message servermodels.Message
		if err := db.NewSelect().Model(&message).Where("msg.idempotency_key = ?", "agent:"+run.ID).Scan(ctx); err != nil || message.Body != "本机回复" {
			t.Fatalf("device reply=%+v %v", message, err)
		}
		// 已完成的运行再次上报保持幂等。
		if err := fixture.executor.CompleteDeviceRun(ctx, fixture.device, run.ID, agentruntime.RunResult{Content: "重复", EndSeq: 1}); err != nil {
			t.Fatal(err)
		}
		// 停止运行后续租得知结束，工作水位随之推进。
		stoppedSeq := fixture.workSeq()
		if status, err := fixture.executor.StopAgentReply(ctx, identity, waiting.ConversationID, waiting.ID); err != nil || status != domain.AgentRunStatusCancelled {
			t.Fatalf("stop=%s %v", status, err)
		}
		if fixture.workSeq() <= stoppedSeq {
			t.Fatal("stop did not advance device work sequence")
		}
		if lease, err := fixture.executor.RenewDeviceRunLease(ctx, fixture.device, waiting.ID); err != nil || !lease.Ended {
			t.Fatalf("lease after stop=%+v %v", lease, err)
		}
		// 停止后设备回传的过程内容仍被保留。
		if err := fixture.executor.FailDeviceRun(ctx, fixture.device, waiting.ID, domain.AgentRunErrorCodeDeviceRunFailed, "stopped", agentruntime.RunResult{Blocks: fixture.partialBlocks()}); err != nil {
			t.Fatal(err)
		}
		fixture.assertBlocks(waiting.ID, 1)
	})

	t.Run("设备运行输入状态", func(t *testing.T) {
		conversationID := fixture.assistantChat()
		run := fixture.sendAndLoadRun(conversationID, "输入状态")
		feed := startRealtimeFeed(t, identity.Organization.ID)
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID); err != nil {
			t.Fatal(err)
		}
		// 领取时建立助理聊天主体并开始发布正在输入。
		agentSubjectID := loadIdentitySubjectID(t, db, identity.Organization.ID, assistant.IdentityID)
		feed.expectTyping(t, feed.userTyping(identity.User.ID, conversationID, agentSubjectID, true))
		// 其他设备上报结果不影响本设备运行的输入状态。
		foreign := agentrunaction.RunDevice{OrganizationID: identity.Organization.ID, UserID: identity.User.ID, DeviceID: uuid.NewV7().String()}
		if err := fixture.executor.CompleteDeviceRun(ctx, foreign, run.ID, agentruntime.RunResult{Content: "他机", EndSeq: 1}); err == nil {
			t.Fatal("foreign device completed run")
		}
		if lease, err := fixture.executor.RenewDeviceRunLease(ctx, fixture.device, run.ID); err != nil || lease.Ended {
			t.Fatalf("renew=%+v %v", lease, err)
		}
		fixture.complete(run.ID, "本机回复")
		feed.expectTypingStopped(t, feed.userTyping(identity.User.ID, conversationID, agentSubjectID, false))
		// 收尾后迟到的续租不再开始发布。
		if lease, err := fixture.executor.RenewDeviceRunLease(ctx, fixture.device, run.ID); err != nil || !lease.Ended {
			t.Fatalf("renew after complete=%+v %v", lease, err)
		}
		feed.expectNoTyping(t)
	})
	t.Run("租约过期", func(t *testing.T) {
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "租约")
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID); err != nil {
			t.Fatal(err)
		}
		if lease, err := fixture.executor.RenewDeviceRunLease(ctx, fixture.device, run.ID); err != nil || lease.Ended {
			t.Fatalf("renew=%+v %v", lease, err)
		}
		if _, err := db.NewUpdate().Model((*servermodels.AgentRun)(nil)).Set("lease_expires_at = now() - interval '1 second'").Where("id = ?", run.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.executor.RenewDeviceRunLease(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("expired renew=%v", err)
		}
		// 扫描收敛前设备上报的迟到结果被拒绝，运行随即按租约过期收敛并保留过程内容。
		if err := fixture.executor.CompleteDeviceRun(ctx, fixture.device, run.ID, agentruntime.RunResult{Content: "迟到", EndSeq: 1, Blocks: fixture.partialBlocks()}); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("expired complete=%v", err)
		}
		fixture.assertFailed(run.ID, domain.AgentRunErrorCodeDeviceLeaseExpired)
		fixture.assertBlocks(run.ID, 1)

		// 收尾请求等待会话锁期间租约过期，取得锁后按租约失效拒绝写入。
		waiting := fixture.sendAndLoadRun(fixture.assistantChat(), "等锁")
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, waiting.ID); err != nil {
			t.Fatal(err)
		}
		triggers, err := fixture.executor.PeekDeviceRunInputs(ctx, fixture.device, waiting.ID, 0)
		if err != nil || len(triggers) == 0 {
			t.Fatalf("peek=%+v %v", triggers, err)
		}
		claimed, err := fixture.executor.ClaimDeviceRunInputs(ctx, fixture.device, waiting.ID, triggers[len(triggers)-1].Seq)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.NewSelect().Model((*servermodels.Conversation)(nil)).Where("id = ?", waiting.ConversationID).For("UPDATE").Exists(ctx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		completed := make(chan error, 1)
		go func() {
			completed <- fixture.executor.CompleteDeviceRun(ctx, fixture.device, waiting.ID, agentruntime.RunResult{Content: "等锁后的回复", EndSeq: claimed.Input.EndSeq})
		}()
		time.Sleep(200 * time.Millisecond)
		if _, err := db.NewUpdate().Model((*servermodels.AgentRun)(nil)).Set("lease_expires_at = now() - interval '1 second'").Where("id = ?", waiting.ID).Exec(ctx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := <-completed; !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("complete after lease expired while waiting=%v", err)
		}
		fixture.sweep()
		fixture.assertFailed(waiting.ID, domain.AgentRunErrorCodeDeviceLeaseExpired)
		// 过期后同一执行范围的新输入另起运行，照常派发到设备。
		next := fixture.sendAndLoadRun(run.ConversationID, "继续")
		if next.ID == run.ID || next.Status != string(domain.AgentRunStatusQueued) {
			t.Fatalf("next run=%+v", next)
		}
		fixture.claimAndComplete(next.ID, "继续后的回复")
	})

	t.Run("超出总时限", func(t *testing.T) {
		expire := func(runID string) {
			if _, err := db.NewUpdate().Model((*servermodels.AgentRun)(nil)).
				Set("claimed_at = now() - make_interval(secs => ?)", agentrunaction.DeviceRunMaxDuration.Seconds()+1).
				Where("id = ?", runID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		// 超出总时限后不再续租、不再代理模型请求，设备上报失败时按超时收敛并保留过程内容。
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "超时")
		if claim, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID); err != nil || len(claim.Assignment) == 0 {
			t.Fatalf("claim=%+v %v", claim, err)
		}
		expire(run.ID)
		if _, err := fixture.executor.RenewDeviceRunLease(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("renew after deadline=%v", err)
		}
		if _, err := fixture.executor.ResolveDeviceModelUpstream(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunLeaseLost) {
			t.Fatalf("model upstream after deadline=%v", err)
		}
		if err := fixture.executor.FailDeviceRun(ctx, fixture.device, run.ID, domain.AgentRunErrorCodeDeviceRunFailed, "context canceled", agentruntime.RunResult{Blocks: fixture.partialBlocks()}); err != nil {
			t.Fatal(err)
		}
		fixture.assertFailed(run.ID, domain.AgentRunErrorCodeDeviceRunTimedOut)
		fixture.assertBlocks(run.ID, 1)

		// 租约仍在续期的运行超出总时限后由扫描收敛。
		swept := fixture.sendAndLoadRun(run.ConversationID, "扫描超时")
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, swept.ID); err != nil {
			t.Fatal(err)
		}
		expire(swept.ID)
		fixture.sweep()
		fixture.assertFailed(swept.ID, domain.AgentRunErrorCodeDeviceRunTimedOut)
	})

	t.Run("失败原因校验", func(t *testing.T) {
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "失败")
		if err := fixture.executor.FailDeviceRun(ctx, fixture.device, run.ID, domain.AgentRunErrorCodeUserCancelled, "", agentruntime.RunResult{}); !errors.Is(err, agentrunaction.ErrDeviceRunFailureCodeInvalid) {
			t.Fatalf("invalid failure code=%v", err)
		}
		if err := fixture.executor.FailDeviceRun(ctx, fixture.device, run.ID, domain.AgentRunErrorCodeDeviceRunFailed, "", agentruntime.RunResult{}); err != nil {
			t.Fatal(err)
		}
		fixture.assertFailed(run.ID, domain.AgentRunErrorCodeDeviceRunFailed)
	})

	t.Run("任务清单随结果保存", func(t *testing.T) {
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "列个清单再做")
		claim, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		// 设备执行的有效配置提供任务清单与委派工具。
		var assignment agentruntime.Assignment
		if err := json.Unmarshal(claim.Assignment, &assignment); err != nil || !slices.Contains(assignment.Tools, "TaskCreate") || !slices.Contains(assignment.Tools, "agent") {
			t.Fatalf("assignment tools=%v %v", assignment.Tools, err)
		}
		triggers, err := fixture.executor.PeekDeviceRunInputs(ctx, fixture.device, run.ID, 0)
		if err != nil || len(triggers) == 0 {
			t.Fatalf("peek=%+v %v", triggers, err)
		}
		claimed, err := fixture.executor.ClaimDeviceRunInputs(ctx, fixture.device, run.ID, triggers[len(triggers)-1].Seq)
		if err != nil || claimed.Suppressed {
			t.Fatalf("claim inputs=%+v %v", claimed, err)
		}
		plan := []runstream.PlanTask{
			{ID: "1", Subject: "整理报价", Status: domain.AgentPlanTaskCompleted},
			{ID: "2", Subject: "生成表格", Status: domain.AgentPlanTaskCompleted},
		}
		if err := fixture.executor.CompleteDeviceRun(ctx, fixture.device, run.ID, agentruntime.RunResult{
			Content: "做完了", EndSeq: claimed.Input.EndSeq, Blocks: fixture.partialBlocks(), Plan: plan,
		}); err != nil {
			t.Fatal(err)
		}
		processes := conversationaction.NewGetAgentRunProcessQuery(db)
		if process, err := processes.Execute(ctx, identity, run.ID); err != nil || !slices.Equal(process.Plan, plan) {
			t.Fatalf("process=%+v %v", process, err)
		}
		// 中断的运行保留已建立的清单，没有清单的运行不返回任务。
		failed := fixture.sendAndLoadRun(run.ConversationID, "再来一次")
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, failed.ID); err != nil {
			t.Fatal(err)
		}
		if err := fixture.executor.FailDeviceRun(ctx, fixture.device, failed.ID, domain.AgentRunErrorCodeDeviceRunFailed, "stopped", agentruntime.RunResult{Blocks: fixture.partialBlocks(), Plan: plan[:1]}); err != nil {
			t.Fatal(err)
		}
		if process, err := processes.Execute(ctx, identity, failed.ID); err != nil || !slices.Equal(process.Plan, plan[:1]) {
			t.Fatalf("failed process=%+v %v", process, err)
		}
		plain := fixture.sendAndLoadRun(run.ConversationID, "不用清单")
		fixture.claimAndComplete(plain.ID, "好的")
		if process, err := processes.Execute(ctx, identity, plain.ID); err != nil || len(process.Plan) != 0 {
			t.Fatalf("plain process=%+v %v", process, err)
		}
	})

	t.Run("由本机 Agent 完成", func(t *testing.T) {
		update := agentaction.NewUpdateAssistantAction(db)
		localInput := agentaction.AssistantInput{DisplayName: assistant.DisplayName, Execution: agentaction.ExecutionInput{
			Mode: domain.AgentExecutionModeLocalAgent, LocalAgent: &agentaction.LocalAgentExecutionInput{Kind: domain.LocalAgentKindCodex, SystemInstruction: "整理周报。"},
		}}
		// 绑定电脑未上报 Codex 可用时不能选择。
		if _, err := update.Execute(ctx, identity, assistant.ID, localInput); !hasFieldCode(err, "localAgent", agentaction.ValidationLocalAgentUnavailable) {
			t.Fatalf("unavailable local agent=%v", err)
		}
		if err := deviceaction.NewReportLocalAgentsAction(db).Execute(ctx, identity, registered.ID, []domain.LocalAgentKind{domain.LocalAgentKindCodex, "unknown"}); err != nil {
			t.Fatal(err)
		}
		// 本机 Agent 执行不使用企业 MCP 服务。
		withMCP := localInput
		withMCP.MCPServerIDs = []string{uuid.NewV7().String()}
		if _, err := update.Execute(ctx, identity, assistant.ID, withMCP); !hasFieldCode(err, "mcpServerIds", agentaction.ValidationMCPServerInvalid) {
			t.Fatalf("local agent with mcp=%v", err)
		}
		updated, err := update.Execute(ctx, identity, assistant.ID, localInput)
		if err != nil || updated.Execution.LocalAgent == nil || updated.Execution.LocalAgent.Kind != domain.LocalAgentKindCodex || !slices.Equal(updated.DeviceLocalAgents, []domain.LocalAgentKind{domain.LocalAgentKindCodex}) {
			t.Fatalf("updated=%+v %v", updated, err)
		}
		if _, execution, err := agentaction.NewGetAssistantQuery(db).Execute(ctx, identity, assistant.ID); err != nil || execution.LocalAgent == nil || execution.LocalAgent.SystemInstruction != "整理周报。" || execution.Managed != nil {
			t.Fatalf("execution=%+v %v", execution, err)
		}
		// 领取时有效配置指定本机 Agent，不含模型与应用工具，模型代理拒绝该运行。
		conversationID := fixture.assistantChat()
		run := fixture.sendAndLoadRun(conversationID, "整理一下")
		claim, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		var assignment agentruntime.Assignment
		if err := json.Unmarshal(claim.Assignment, &assignment); err != nil || assignment.LocalAgent != domain.LocalAgentKindCodex || len(assignment.Tools) != 0 ||
			assignment.Model.Identifier != "" || !strings.Contains(assignment.Instruction, "整理周报。") {
			t.Fatalf("assignment=%+v %v", assignment, err)
		}
		if _, err := fixture.executor.ResolveDeviceModelUpstream(ctx, fixture.device, run.ID); !errors.Is(err, agentrunaction.ErrDeviceRunUnavailable) {
			t.Fatalf("model upstream for local agent=%v", err)
		}
		fixture.complete(run.ID, "周报整理好了")
		// 本机 Agent 未登录的失败原因随失败消息返回。
		failed := fixture.sendAndLoadRun(conversationID, "再整理一次")
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, failed.ID); err != nil {
			t.Fatal(err)
		}
		if err := fixture.executor.FailDeviceRun(ctx, fixture.device, failed.ID, domain.AgentRunErrorCodeLocalAgentAuthRequired, "", agentruntime.RunResult{}); err != nil {
			t.Fatal(err)
		}
		fixture.assertFailed(failed.ID, domain.AgentRunErrorCodeLocalAgentAuthRequired)
		var failedRun servermodels.AgentRun
		if err := db.NewSelect().Model(&failedRun).Where("agr.id = ?", failed.ID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
		if err != nil {
			t.Fatal(err)
		}
		index := slices.IndexFunc(history.Messages, func(message conversationaction.ConversationMessage) bool {
			return message.ID == *failedRun.ResponseMessageID
		})
		if index < 0 || history.Messages[index].AgentErrorCode == nil || *history.Messages[index].AgentErrorCode != domain.AgentRunErrorCodeLocalAgentAuthRequired {
			t.Fatalf("failure message=%+v", history.Messages)
		}
		// 改回由助理自己完成，后续用例沿用托管执行。
		if _, err := update.Execute(ctx, identity, assistant.ID, agentaction.AssistantInput{DisplayName: assistant.DisplayName, Execution: agentaction.ExecutionInput{
			Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: employee.Execution.Managed.ProviderID, ModelIdentifier: employee.Execution.Managed.ModelIdentifier},
		}}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("暂停与恢复", func(t *testing.T) {
		conversationID := fixture.assistantChat()
		paused, err := agentaction.NewSetAssistantPausedAction(db).Execute(ctx, identity, assistant.ID, true)
		if err != nil || paused.Presence(time.Now()) != domain.AssistantPresencePaused {
			t.Fatalf("pause=%+v %v", paused, err)
		}
		_, err = fixture.send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "暂停中"})
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflict.Reason != conversationaction.ConflictReasonAssistantPaused {
			t.Fatalf("send to paused assistant=%v", err)
		}
		if _, err := agentaction.NewSetAssistantPausedAction(db).Execute(ctx, identity, assistant.ID, false); err != nil {
			t.Fatal(err)
		}
		fixture.claimAndComplete(fixture.sendAndLoadRun(conversationID, "恢复后").ID, "恢复后的回复")
	})

	t.Run("换到另一台电脑", func(t *testing.T) {
		conversationID := fixture.assistantChat()
		queued := fixture.sendAndLoadRun(conversationID, "换电脑前")
		otherDevice, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, identity, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "新电脑", Platform: domain.DevicePlatformLinux})
		if err != nil {
			t.Fatal(err)
		}
		moved, err := agentaction.NewMoveAssistantAction(db).Execute(ctx, identity, assistant.ID, otherDevice.ID)
		if err != nil || moved.DeviceID != otherDevice.ID {
			t.Fatalf("move=%+v %v", moved, err)
		}
		// 原电脑上未领取的运行被收敛。
		if _, err := fixture.executor.ClaimDeviceRun(ctx, fixture.device, queued.ID); !errors.Is(err, agentrunaction.ErrDeviceRunUnavailable) {
			t.Fatalf("claim on previous device=%v", err)
		}
		fixture.sweep()
		fixture.assertFailed(queued.ID, domain.AgentRunErrorCodeExecutionChanged)
		next := fixture.sendAndLoadRun(conversationID, "换电脑后")
		if next.ExecutionDeviceID == nil || *next.ExecutionDeviceID != otherDevice.ID {
			t.Fatalf("run after move=%+v", next)
		}
		if _, err := fixture.executor.StopAgentReply(ctx, identity, conversationID, next.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := agentaction.NewMoveAssistantAction(db).Execute(ctx, identity, assistant.ID, registered.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("撤销设备", func(t *testing.T) {
		run := fixture.sendAndLoadRun(fixture.assistantChat(), "撤销")
		if err := deviceaction.NewRevokeDeviceAction(db).Execute(ctx, identity, registered.ID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = deviceaction.NewRegisterDeviceAction(db).Execute(ctx, identity, deviceaction.RegisterInput{InstallID: installID, Name: "测试电脑", Platform: domain.DevicePlatformMacOS})
		})
		if _, err := deviceaction.NewAuthenticateDeviceAction(db).Execute(ctx, identity, registered.ID); !errors.Is(err, deviceaction.ErrNotFound) {
			t.Fatalf("revoked device auth=%v", err)
		}
		fixture.sweep()
		fixture.assertFailed(run.ID, domain.AgentRunErrorCodeDeviceUnavailable)
		// 绑定电脑撤销后，发送前即拒绝且未绑定优先于暂停，不再排队。
		if _, err := agentaction.NewSetAssistantPausedAction(db).Execute(ctx, identity, assistant.ID, true); err != nil {
			t.Fatal(err)
		}
		_, err := fixture.send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: run.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "撤销后"})
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflict.Reason != conversationaction.ConflictReasonAssistantUnbound {
			t.Fatalf("send to unbound assistant=%v", err)
		}
		reloaded, _, err := agentaction.NewGetAssistantQuery(db).Execute(ctx, identity, assistant.ID)
		if err != nil || reloaded.Presence(time.Now()) != domain.AssistantPresenceUnbound {
			t.Fatalf("reload assistant=%+v %v", reloaded, err)
		}
	})
}

// testDeviceKnowledge 为每个知识库提供一条以库名和查询拼成正文的检索结果。
type testDeviceKnowledge struct{}

// Sources 按知识库编号构造检索来源。
func (testDeviceKnowledge) Sources(_ context.Context, _ string, knowledgeBaseIDs []string) ([]knowledgeretrieval.Source, error) {
	sources := make([]knowledgeretrieval.Source, 0, len(knowledgeBaseIDs))
	for _, id := range knowledgeBaseIDs {
		sources = append(sources, knowledgeretrieval.Source{ID: id, Name: "设备资料", Retrieve: func(_ context.Context, query string) ([]knowledgeretrieval.Record, error) {
			return []knowledgeretrieval.Record{{KnowledgeBaseID: id, SegmentID: uuid.NewV7().String(), Content: "设备资料：" + query, Matched: true}}, nil
		}})
	}
	return sources, nil
}

// assistantChat 创建一条与测试助理的新单聊，停止首条消息的运行后返回会话编号。
func (f *deviceRunFixture) assistantChat() string {
	f.t.Helper()
	conversationID := uuid.NewV7().String()
	if _, err := f.sendFirst.Execute(f.ctx, f.identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: f.assistant.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "开始",
	}); err != nil {
		f.t.Fatalf("send first=%v", err)
	}
	var run servermodels.AgentRun
	if err := f.db.NewSelect().Model(&run).Where("agr.conversation_id = ?", conversationID).Scan(f.ctx); err != nil {
		f.t.Fatalf("load first run=%v", err)
	}
	if _, err := f.executor.StopAgentReply(f.ctx, f.identity, conversationID, run.ID); err != nil {
		f.t.Fatalf("stop first run=%v", err)
	}
	return conversationID
}

// sendAndLoadRun 在会话中发送一条消息并返回因此建立的排队运行。
func (f *deviceRunFixture) sendAndLoadRun(conversationID, body string) servermodels.AgentRun {
	f.t.Helper()
	if _, err := f.send.Execute(f.ctx, f.identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body}); err != nil {
		f.t.Fatal(err)
	}
	var run servermodels.AgentRun
	if err := f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	return run
}

// claimAndComplete 由设备领取运行并以指定正文收尾。
func (f *deviceRunFixture) claimAndComplete(runID, content string) {
	f.t.Helper()
	if _, err := f.executor.ClaimDeviceRun(f.ctx, f.device, runID); err != nil {
		f.t.Fatal(err)
	}
	f.complete(runID, content)
}

// complete 按设备协议读取并认领全部输入后以指定正文收尾。
func (f *deviceRunFixture) complete(runID, content string) {
	f.t.Helper()
	triggers, err := f.executor.PeekDeviceRunInputs(f.ctx, f.device, runID, 0)
	if err != nil || len(triggers) == 0 {
		f.t.Fatalf("peek=%+v %v", triggers, err)
	}
	claimed, err := f.executor.ClaimDeviceRunInputs(f.ctx, f.device, runID, triggers[len(triggers)-1].Seq)
	if err != nil || claimed.Suppressed || len(claimed.Input.Messages) == 0 {
		f.t.Fatalf("claim inputs=%+v %v", claimed, err)
	}
	if err := f.executor.CompleteDeviceRun(f.ctx, f.device, runID, agentruntime.RunResult{Content: content, EndSeq: claimed.Input.EndSeq}); err != nil {
		f.t.Fatal(err)
	}
	var run servermodels.AgentRun
	if err := f.db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(f.ctx); err != nil || run.Status != string(domain.AgentRunStatusSucceeded) {
		f.t.Fatalf("completed run=%+v %v", run, err)
	}
}

// sweep 执行一次设备运行收敛扫描。
func (f *deviceRunFixture) sweep() {
	f.t.Helper()
	if err := f.executor.SweepDeviceRuns(f.ctx, struct{}{}); err != nil {
		f.t.Fatal(err)
	}
}

// assertFailed 核对运行以指定错误码失败并写入了失败消息。
func (f *deviceRunFixture) assertFailed(runID string, code domain.AgentRunErrorCode) {
	f.t.Helper()
	var run servermodels.AgentRun
	if err := f.db.NewSelect().Model(&run).Where("agr.id = ?", runID).Scan(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	if run.Status != string(domain.AgentRunStatusFailed) || run.ErrorCode == nil || *run.ErrorCode != string(code) || run.ResponseMessageID == nil {
		f.t.Fatalf("failed run=%+v", run)
	}
}

// partialBlocks 构造设备回传的一个正文过程内容块。
func (f *deviceRunFixture) partialBlocks() []agentruntime.Block {
	return []agentruntime.Block{{
		ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
		Kind: domain.AgentRunBlockContent, Payload: agentruntime.BlockPayload{Text: "中断前的内容"},
	}}
}

// assertBlocks 核对运行保存的过程内容块数量。
func (f *deviceRunFixture) assertBlocks(runID string, expected int) {
	f.t.Helper()
	count, err := f.db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", runID).Count(f.ctx)
	if err != nil || count != expected {
		f.t.Fatalf("run blocks=%d %v", count, err)
	}
}

// workSeq 读取测试设备的工作水位。
func (f *deviceRunFixture) workSeq() int64 {
	f.t.Helper()
	var seq int64
	if err := f.db.NewSelect().Model((*servermodels.Device)(nil)).Column("work_seq").Where("d.id = ?", f.device.DeviceID).Scan(f.ctx, &seq); err != nil {
		f.t.Fatal(err)
	}
	return seq
}

// deviceWorkContains 判断待领取运行中是否包含指定运行。
func deviceWorkContains(work agentrunaction.DeviceWork, runID string) bool {
	for _, run := range work.Runs {
		if run.RunID == runID {
			return true
		}
	}
	return false
}

// hasFieldCode 判断错误是否为指定字段的指定校验码。
func hasFieldCode(err error, field string, code common.FieldCode) bool {
	fieldError, ok := errors.AsType[*common.FieldError](err)
	return ok && fieldError.Fields[field] == code
}
