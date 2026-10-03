//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// personalAgentFixture 是个人 AI 员工集成测试共用的个人 AI 员工、电脑与调用入口。
type personalAgentFixture struct {
	t             *testing.T
	ctx           context.Context
	db            *bun.DB
	identity      *servermodels.Identity
	personalAgent *agentaction.PersonalAgent
	tasks         *servertask.Runtime
	installID     string
	computer      *computeraction.Registration
	stopper       *agentrunaction.ExecuteAction
	sendFirst     *directchataction.SendFirstAgentTextMessageAction
	send          *directchataction.SendAgentTextMessageAction
	model         *scriptedComputerModel
	runner        *agentrunaction.ExecuteAction
}

// scriptedComputerModel 是按输入决定输出的对话模型：最后一条是用户消息时发起预设的工具调用，拿到工具结果后把结果原样写进回答。
type scriptedComputerModel struct {
	mu   sync.Mutex
	call *schema.FunctionToolCall
}

// use 设置下一次运行发起的工具调用。
func (m *scriptedComputerModel) use(name, arguments string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.call = &schema.FunctionToolCall{CallID: "call-" + uuid.NewV7().String(), Name: name, Arguments: arguments}
}

// Generate 按最后一条消息返回工具调用或带工具结果的回答。
func (m *scriptedComputerModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	last := input[len(input)-1]
	for _, block := range last.ContentBlocks {
		if block.Type == schema.ContentBlockTypeFunctionToolResult {
			var text strings.Builder
			for _, content := range block.FunctionToolResult.Content {
				if content.Type == schema.FunctionToolResultContentBlockTypeText {
					text.WriteString(content.Text.Text)
				}
			}
			return assistantText("结果：" + text.String()), nil
		}
	}
	message := assistantText("")
	message.ContentBlocks = append(message.ContentBlocks, schema.NewContentBlock(m.call))
	return message, nil
}

// Stream 以单个分片返回模型输出。
func (m *scriptedComputerModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// testPersonalAgentComputer 验证个人 AI 员工的运行在服务端执行，文件、命令与本机 MCP 操作作为工具调用派发到其电脑，
// 以及电脑离线、结果超出等待时长、电脑掉线与撤销时的结算，暂停、换电脑与撤销电脑后的请求处理。
func testPersonalAgentComputer(t *testing.T, db *bun.DB, identity *servermodels.Identity, employee *agentaction.Agent, tasks *servertask.Runtime) {
	ctx := context.Background()
	installID := uuid.NewV7().String()
	registered, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, identity, computeraction.RegisterInput{InstallID: installID, Name: "测试电脑", Platform: domain.ComputerPlatformMacOS})
	if err != nil {
		t.Fatal(err)
	}
	personalAgent, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, identity, registered.Record.ID, agentaction.PersonalAgentInput{
		DisplayName: "小码", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: employee.Execution.Managed.Model.ID,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := agentruntime.New()
	if err != nil {
		t.Fatal(err)
	}
	chat := &scriptedComputerModel{}
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return chat, nil }
	fixture := &personalAgentFixture{
		t: t, ctx: ctx, db: db, identity: identity, personalAgent: personalAgent, tasks: tasks, installID: installID, computer: registered, model: chat,
		stopper:   agentrunaction.NewExecuteAction(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, nil),
		runner:    agentrunaction.NewExecuteAction(db, tasks, runtime, modelcall.New(db, upstreams), testAttachmentReader(db), nil, nil),
		sendFirst: directchataction.NewSendFirstAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)),
		send:      directchataction.NewSendAgentTextMessageAction(db, agentrunaction.NewScheduler(tasks)),
	}
	t.Run("个人 AI 员工归属负责人", func(t *testing.T) {
		if personalAgent.ResponsibleUserID != identity.User.ID || personalAgent.ComputerID != registered.Record.ID || personalAgent.Presence(time.Now()) != domain.PersonalAgentPresenceOffline {
			t.Fatalf("personalAgent=%+v", personalAgent)
		}
		fixture.online()
		reloaded, _, err := agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, identity, personalAgent.ID)
		if err != nil || reloaded.Presence(time.Now()) != domain.PersonalAgentPresenceOnline {
			t.Fatalf("online personalAgent=%+v %v", reloaded, err)
		}
		other := newChatLockUser(t, db, identity)
		// 其他成员不能用别人的电脑创建个人 AI 员工，也不能与别人的个人 AI 员工单聊。
		if _, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, other, registered.Record.ID, agentaction.PersonalAgentInput{
			DisplayName: "冒用", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: employee.Execution.Managed.Model.ID}},
		}); !errors.Is(err, agentaction.ErrPersonalAgentComputerNotFound) {
			t.Fatalf("create on foreign computer=%v", err)
		}
		if _, err := fixture.sendFirst.Execute(ctx, other, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: personalAgent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "借用",
		}); !errors.Is(err, conversationaction.ErrAgentTargetNotFound) {
			t.Fatalf("foreign personalAgent chat=%v", err)
		}
		// AI 员工管理入口不能修改个人 AI 员工。
		if _, err := agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusInactive); err == nil {
			t.Fatal("agent status action changed personalAgent")
		}
		if _, err := agentaction.NewGetAgentQuery(db).Execute(ctx, identity, personalAgent.ID); !errors.Is(err, agentaction.ErrNotFound) {
			t.Fatalf("agent query read personalAgent=%v", err)
		}
	})

	t.Run("运行使用电脑上报的能力", func(t *testing.T) {
		capabilities := domain.ComputerCapabilities{
			Shell: "bash", FolderRoot: "/Users/test/Documents/App",
			MCPServers: []domain.ComputerMCPServer{{Name: "notes", Tools: []domain.ComputerMCPTool{{Name: "append", Description: "追加笔记", InputSchema: json.RawMessage(`{"type":"object"}`)}}}},
			Skills:     []domain.ComputerSkill{{Name: "xlsx", Description: "处理 Excel 表格", Dir: "/Users/test/.app/skills/xlsx"}},
		}
		if err := computeraction.NewReportCapabilitiesAction(db).Execute(ctx, fixture.computerIdentity(), computeraction.CapabilitiesInput{Capabilities: capabilities, ExecutorVersion: "1", MaxConcurrency: 2}); err != nil {
			t.Fatal(err)
		}
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "看看有哪些工具")
		inspect := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			for _, name := range agentruntime.ComputerTools() {
				if !slices.Contains(request.Assignment.Tools, name) {
					t.Errorf("tools=%v missing %s", request.Assignment.Tools, name)
				}
			}
			if request.Computer == nil || request.ComputerCapabilities.Skills[0].Name != "xlsx" || request.Memory == nil || !request.Assignment.Memory {
				t.Errorf("request computer=%v capabilities=%+v memory=%v", request.Computer, request.ComputerCapabilities, request.Assignment.Memory)
			}
			if !slices.ContainsFunc(request.MCPConnections, func(server agentruntime.MCPServer) bool {
				return server.Source == agentruntime.MCPSourceLocal && server.Name == "notes"
			}) {
				t.Errorf("mcp connections=%+v", request.MCPConnections)
			}
			return completeTestRun(ctx, feed, "看过了")
		}}
		fixture.execute(inspect, run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		// AI 员工的运行不使用电脑。
		_, employeeRun := createAgentLockChat(t, ctx, db, identity, employee.IdentityID, tasks)
		employeeRuntime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			if request.Computer != nil || slices.ContainsFunc(request.Assignment.Tools, agentruntime.IsComputerTool) {
				t.Errorf("employee request computer=%v tools=%v", request.Computer, request.Assignment.Tools)
			}
			return completeTestRun(ctx, feed, "好的")
		}}
		fixture.execute(employeeRuntime, employeeRun.ID)
	})

	t.Run("个人 AI 员工记忆", func(t *testing.T) {
		testAgentMemory(t, fixture)
	})

	t.Run("电脑操作的派发、领取与上报", func(t *testing.T) {
		fixture.online()
		conversationID := fixture.personalAgentChat()
		run := fixture.sendAndLoadRun(conversationID, "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		done := fixture.simulateExecutor(func(operation computeraction.Operation) *domain.ComputerOutcome {
			if operation.Operation.Kind != domain.ComputerOperationReadFile || operation.Operation.Path != "notes.txt" || operation.Operation.Folder != conversationID {
				t.Errorf("operation=%+v", operation)
			}
			return &domain.ComputerOutcome{Output: "     1\t会议纪要", Path: "/Users/test/notes.txt", Hash: "h1"}
		})
		fixture.run(run.ID)
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "结果：     1\t会议纪要")
		call := fixture.computerCall(run.ID)
		if call.Status != string(domain.AgentToolCallSucceeded) || call.Operation.Outcome == nil || call.Operation.Outcome.Hash != "h1" || call.Result == nil || *call.Result != "     1\t会议纪要" {
			t.Fatalf("call=%+v", call)
		}
	})

	t.Run("电脑离线时直接失败", func(t *testing.T) {
		fixture.offline()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, agentruntime.ErrComputerOffline.Error())
		var calls []servermodels.AgentToolCall
		if err := db.NewSelect().Model(&calls).Where("atc.agent_run_id = ?", run.ID).Scan(ctx); err != nil || len(calls) != 1 || calls[0].ComputerID != nil || calls[0].Status != string(domain.AgentToolCallFailed) {
			t.Fatalf("calls=%+v err=%v", calls, err)
		}
	})

	t.Run("结果超出等待时长时挂起，电脑掉线后结算并唤醒", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallQueued) || call.StartedAt != nil {
			t.Fatalf("dispatched call=%+v", call)
		}
		fixture.offline()
		if err := computeraction.NewSweepAction(db, tasks).Execute(ctx, struct{}{}); err != nil {
			t.Fatal(err)
		}
		call := fixture.computerCall(run.ID)
		if call.Status != string(domain.AgentToolCallFailed) || call.Error == nil || *call.Error != agentruntime.ErrComputerOffline.Error() {
			t.Fatalf("swept call=%+v", call)
		}
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, agentruntime.ErrComputerOffline.Error())
	})

	t.Run("撤销电脑结算执行中的调用", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "清理临时文件")
		fixture.model.use("execute", `{"command":"rm -rf tmp"}`)
		done := fixture.simulateExecutor(func(computeraction.Operation) *domain.ComputerOutcome { return nil })
		fixture.run(run.ID)
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		if err := computeraction.NewRevokeComputerAction(db, tasks).Execute(ctx, identity, registered.Record.ID); err != nil {
			t.Fatal(err)
		}
		if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallNeedsReview) {
			t.Fatalf("revoked call=%+v", call)
		}
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "已交由人工核对")
		// 撤销后迟到的结果不改变已结算的调用。
		if err := computeraction.NewOperationsAction(db, tasks).Complete(ctx, fixture.computerIdentity(), fixture.computerCall(run.ID).ID, domain.ComputerOutcome{Output: "late"}); err != nil {
			t.Fatal(err)
		}
		if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallNeedsReview) {
			t.Fatalf("late completed call=%+v", call)
		}
		fixture.reregister()
	})

	t.Run("执行器不再持有的调用在下次领取时结算", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "整理下载文件夹")
		fixture.model.use("execute", `{"command":"mv ~/Downloads/*.pdf ~/Documents"}`)
		done := fixture.simulateExecutor(func(computeraction.Operation) *domain.ComputerOutcome { return nil })
		fixture.run(run.ID)
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		// 执行器仍持有该操作时保持执行中，重启后不再持有时按丢失结算并唤醒运行。
		operations := computeraction.NewOperationsAction(db, tasks)
		held := fixture.computerCall(run.ID)
		if _, err := operations.Claim(ctx, fixture.computerIdentity(), 4, []string{held.ID}); err != nil {
			t.Fatal(err)
		}
		if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallRunning) {
			t.Fatalf("held call=%+v", call)
		}
		if _, err := operations.Claim(ctx, fixture.computerIdentity(), 4, nil); err != nil {
			t.Fatal(err)
		}
		if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallNeedsReview) {
			t.Fatalf("released call=%+v", call)
		}
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "已交由人工核对")
	})

	t.Run("停止回复时中止电脑上执行中的调用", func(t *testing.T) {
		operations := computeraction.NewOperationsAction(db, tasks)
		for _, finished := range []bool{false, true} {
			fixture.online()
			conversationID := fixture.personalAgentChat()
			run := fixture.sendAndLoadRun(conversationID, "跑一下构建")
			fixture.model.use("execute", `{"command":"make build"}`)
			done := fixture.simulateExecutor(func(computeraction.Operation) *domain.ComputerOutcome { return nil })
			fixture.run(run.ID)
			<-done
			fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
			call := fixture.computerCall(run.ID)
			if call.Status != string(domain.AgentToolCallRunning) || call.StartedAt == nil {
				t.Fatalf("claimed call=%+v", call)
			}
			if _, err := fixture.stopper.StopAgentReply(ctx, identity, conversationID, run.ID); err != nil {
				t.Fatal(err)
			}
			fixture.assertStatus(run.ID, domain.AgentRunStatusCancelled)
			// 运行结束后电脑上的调用仍在执行，执行器下次领取时收到中止要求。
			if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallRunning) {
				t.Fatalf("stopping call=%+v", call)
			}
			claimed, err := operations.Claim(ctx, fixture.computerIdentity(), 4, []string{call.ID})
			if err != nil || len(claimed.Abort) != 1 || claimed.Abort[0] != call.ID {
				t.Fatalf("claim=%+v err=%v", claimed, err)
			}
			outcome, want := domain.ComputerOutcome{Aborted: true}, domain.AgentToolCallNeedsReview
			if finished {
				// 收到中止前已经执行完的命令如实记录结果。
				outcome, want = domain.ComputerOutcome{Output: "built"}, domain.AgentToolCallSucceeded
			}
			if err := operations.Complete(ctx, fixture.computerIdentity(), call.ID, outcome); err != nil {
				t.Fatal(err)
			}
			if call := fixture.computerCall(run.ID); call.Status != string(want) || call.CompletedAt == nil {
				t.Fatalf("settled call=%+v want %s", call, want)
			}
			fixture.assertStatus(run.ID, domain.AgentRunStatusCancelled)
		}
	})

	t.Run("停止回复时取消未开始的调用并中断进程内调用", func(t *testing.T) {
		fixture.online()
		conversationID := fixture.personalAgentChat()
		run := fixture.sendAndLoadRun(conversationID, "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		// 补两条服务端进程内执行中的调用：一条可重新执行，一条有外部副作用。
		inProcess := map[bool]string{}
		for _, sideEffects := range []bool{false, true} {
			row := &servermodels.AgentToolCall{
				ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID, AgentRunID: run.ID, ModelCallID: uuid.NewV7().String(),
				ProviderCallID: uuid.NewV7().String(), Name: "remote", Source: string(domain.AgentToolSourceMCP), Arguments: "{}",
				Replayable: !sideEffects, SideEffects: sideEffects, Status: string(domain.AgentToolCallRunning),
			}
			if _, err := db.NewInsert().Model(row).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			inProcess[sideEffects] = row.ID
		}
		if _, err := fixture.stopper.StopAgentReply(ctx, identity, conversationID, run.ID); err != nil {
			t.Fatal(err)
		}
		if call := fixture.computerCall(run.ID); call.Status != string(domain.AgentToolCallCancelled) {
			t.Fatalf("pending call=%+v", call)
		}
		for sideEffects, want := range map[bool]domain.AgentToolCallStatus{false: domain.AgentToolCallInterrupted, true: domain.AgentToolCallNeedsReview} {
			call := &servermodels.AgentToolCall{}
			if err := db.NewSelect().Model(call).Where("atc.id = ?", inProcess[sideEffects]).Scan(ctx); err != nil || call.Status != string(want) || call.Result == nil {
				t.Fatalf("in-process call=%+v err=%v want %s", call, err, want)
			}
		}
		claimed, err := computeraction.NewOperationsAction(db, tasks).Claim(ctx, fixture.computerIdentity(), 4, nil)
		if err != nil || len(claimed.Operations) != 0 {
			t.Fatalf("claim after stop=%+v err=%v", claimed, err)
		}
	})

	t.Run("暂停与恢复", func(t *testing.T) {
		conversationID := fixture.personalAgentChat()
		paused, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, true)
		if err != nil || paused.Presence(time.Now()) != domain.PersonalAgentPresencePaused {
			t.Fatalf("pause=%+v %v", paused, err)
		}
		_, err = fixture.send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "暂停中"})
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflict.Reason != conversationaction.ConflictReasonPersonalAgentPaused {
			t.Fatalf("send to paused personalAgent=%v", err)
		}
		if _, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, false); err != nil {
			t.Fatal(err)
		}
		run := fixture.sendAndLoadRun(conversationID, "恢复后")
		if _, err := fixture.stopper.StopAgentReply(ctx, identity, conversationID, run.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("换到另一台电脑", func(t *testing.T) {
		other, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, identity, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "新电脑", Platform: domain.ComputerPlatformLinux})
		if err != nil {
			t.Fatal(err)
		}
		moved, err := agentaction.NewMovePersonalAgentAction(db).Execute(ctx, identity, personalAgent.ID, other.Record.ID)
		if err != nil || moved.ComputerID != other.Record.ID {
			t.Fatalf("move=%+v %v", moved, err)
		}
		if _, err := agentaction.NewMovePersonalAgentAction(db).Execute(ctx, identity, personalAgent.ID, fixture.computer.Record.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("撤销电脑后不接收新请求", func(t *testing.T) {
		conversationID := fixture.personalAgentChat()
		if err := computeraction.NewRevokeComputerAction(db, tasks).Execute(ctx, identity, fixture.computer.Record.ID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(fixture.reregister)
		// 电脑撤销后，发送前即拒绝且电脑撤销优先于暂停。
		if _, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, true); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, false)
		})
		_, err := fixture.send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "撤销后"})
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflict.Reason != conversationaction.ConflictReasonPersonalAgentUnbound {
			t.Fatalf("send to unbound personalAgent=%v", err)
		}
		reloaded, _, err := agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, identity, personalAgent.ID)
		if err != nil || reloaded.Presence(time.Now()) != domain.PersonalAgentPresenceUnbound {
			t.Fatalf("reload personalAgent=%+v %v", reloaded, err)
		}
	})
}

// completeTestRun 认领全部输入并以指定正文结束运行。
func completeTestRun(ctx context.Context, feed agentruntime.InputFeed, content string) (agentruntime.RunResult, error) {
	triggers, err := feed.Peek(ctx, 0)
	if err != nil || len(triggers) == 0 {
		return agentruntime.RunResult{}, errors.Join(err, errors.New("no input to claim"))
	}
	claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
	if err != nil {
		return agentruntime.RunResult{}, err
	}
	return agentruntime.RunResult{Content: content, EndSeq: claimed.EndSeq}, nil
}

// computerIdentity 返回测试电脑的认证身份。
func (f *personalAgentFixture) computerIdentity() computeraction.Identity {
	return computeraction.Identity{OrganizationID: f.identity.Organization.ID, ComputerID: f.computer.Record.ID}
}

// online 记录测试电脑在线。
func (f *personalAgentFixture) online() {
	f.t.Helper()
	if err := computeraction.NewTouchComputerAction(f.db).Execute(f.ctx, f.computerIdentity()); err != nil {
		f.t.Fatal(err)
	}
}

// offline 把测试电脑的最近在线时间改到在线时限之前。
func (f *personalAgentFixture) offline() {
	f.t.Helper()
	if _, err := f.db.NewUpdate().Model((*servermodels.Computer)(nil)).
		Set("last_seen_at = now() - interval '5 minutes'").Where("id = ?", f.computer.Record.ID).Exec(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}

// reregister 以同一安装重新注册测试电脑，撤销标记清除并换用新凭据。
func (f *personalAgentFixture) reregister() {
	registered, err := computeraction.NewRegisterComputerAction(f.db).Execute(f.ctx, f.identity, computeraction.RegisterInput{InstallID: f.installID, Name: "测试电脑", Platform: domain.ComputerPlatformMacOS})
	if err != nil {
		f.t.Fatal(err)
	}
	f.computer = registered
}

// simulateExecutor 模拟执行器领取一次操作，outcome 返回非空时上报该结果，为空时保持执行中；返回的通道在领取并处理后关闭。
func (f *personalAgentFixture) simulateExecutor(outcome func(computeraction.Operation) *domain.ComputerOutcome) <-chan struct{} {
	done := make(chan struct{})
	operations := computeraction.NewOperationsAction(f.db, f.tasks)
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			claimed, err := operations.Claim(f.ctx, f.computerIdentity(), 4, nil)
			if err != nil {
				f.t.Errorf("claim=%v", err)
				return
			}
			if len(claimed.Operations) == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if result := outcome(claimed.Operations[0]); result != nil {
				if err := operations.Complete(f.ctx, f.computerIdentity(), claimed.Operations[0].ID, *result); err != nil {
					f.t.Errorf("complete=%v", err)
				}
			}
			return
		}
		f.t.Error("no computer operation dispatched")
	}()
	return done
}

// execute 以指定运行时执行一次运行。
func (f *personalAgentFixture) execute(runtime agentruntime.Runtime, runID string) {
	f.t.Helper()
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, nil).
		Execute(f.ctx, agentrunaction.RunInput{RunID: runID}); err != nil {
		f.t.Fatal(err)
	}
}

// run 以真实运行时与脚本化模型执行一次运行。
func (f *personalAgentFixture) run(runID string) {
	f.t.Helper()
	if err := f.runner.Execute(f.ctx, agentrunaction.RunInput{RunID: runID}); err != nil {
		f.t.Fatal(err)
	}
}

// computerCall 读取运行中派发到电脑的唯一一次工具调用。
func (f *personalAgentFixture) computerCall(runID string) servermodels.AgentToolCall {
	f.t.Helper()
	var call servermodels.AgentToolCall
	if err := f.db.NewSelect().Model(&call).Where("atc.agent_run_id = ? AND atc.computer_id IS NOT NULL", runID).Scan(f.ctx); err != nil || call.Operation == nil {
		f.t.Fatalf("computer call=%+v err=%v", call, err)
	}
	return call
}

// assertStatus 核对运行状态。
func (f *personalAgentFixture) assertStatus(runID string, status domain.AgentRunStatus) {
	f.t.Helper()
	assertAgentRunStatus(f.t, f.ctx, f.db, runID, status)
}

// assertReply 核对运行的回复正文包含指定文本。
func (f *personalAgentFixture) assertReply(runID, text string) {
	f.t.Helper()
	var body string
	if err := f.db.NewSelect().TableExpr("agent_runs AS agr").ColumnExpr("msg.body").
		Join("JOIN messages AS msg ON msg.id = agr.response_message_id").
		Where("agr.id = ?", runID).Scan(f.ctx, &body); err != nil || !strings.Contains(body, text) {
		f.t.Fatalf("reply=%q err=%v, want %q", body, err, text)
	}
}

// personalAgentChat 创建一条与测试个人 AI 员工的新单聊，停止首条消息的运行后返回会话编号。
func (f *personalAgentFixture) personalAgentChat() string {
	f.t.Helper()
	conversationID := uuid.NewV7().String()
	if _, err := f.sendFirst.Execute(f.ctx, f.identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: f.personalAgent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "开始",
	}); err != nil {
		f.t.Fatalf("send first=%v", err)
	}
	var run servermodels.AgentRun
	if err := f.db.NewSelect().Model(&run).Where("agr.conversation_id = ?", conversationID).Scan(f.ctx); err != nil {
		f.t.Fatalf("load first run=%v", err)
	}
	if _, err := f.stopper.StopAgentReply(f.ctx, f.identity, conversationID, run.ID); err != nil {
		f.t.Fatalf("stop first run=%v", err)
	}
	return conversationID
}

// sendAndLoadRun 在会话中发送一条消息并返回因此建立的排队运行。
func (f *personalAgentFixture) sendAndLoadRun(conversationID, body string) servermodels.AgentRun {
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
