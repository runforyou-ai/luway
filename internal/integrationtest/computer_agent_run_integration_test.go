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
	"github.com/runforyou-ai/einorun"
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
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// personalAgentFixture 是个人 AI 员工集成测试共用的个人 AI 员工、电脑与调用入口。
type personalAgentFixture struct {
	t             *testing.T
	ctx           context.Context
	db            *bun.DB
	identity      *servermodels.Identity
	personalAgent *agentaction.PersonalAgent
	tasks         *servertest.Tasks
	installID     string
	computer      *computeraction.Registration
	stopper       *testAgentRun
	sendFirst     *directchataction.SendFirstAgentTextMessageAction
	send          *directchataction.SendAgentTextMessageAction
	model         *scriptedComputerModel
	runner        *testAgentRun
}

// scriptedComputerModel 是按输入决定输出的对话模型：最后一条是用户消息时发起预设的工具调用，没有预设调用时直接回答，拿到工具结果后把结果原样写进回答；
// 设置了推理内容块时把它放在工具调用之前，并记下最近一次收到的输入。
type scriptedComputerModel struct {
	mu        sync.Mutex
	call      *schema.FunctionToolCall
	reasoning *schema.ContentBlock
	input     []*schema.AgenticMessage
}

// use 设置下一次运行发起的工具调用，不带推理内容块。
func (m *scriptedComputerModel) use(name, arguments string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.call = &schema.FunctionToolCall{CallID: "call-" + uuid.NewV7().String(), Name: name, Arguments: arguments}
	m.reasoning = nil
}

// useReasoning 设置工具调用之前输出的推理内容块。
func (m *scriptedComputerModel) useReasoning(block *schema.ContentBlock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reasoning = block
}

// lastInput 返回最近一次收到的输入。
func (m *scriptedComputerModel) lastInput() []*schema.AgenticMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.input
}

// Generate 按最后一条消息返回工具调用或带工具结果的回答。
func (m *scriptedComputerModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.input = input
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
	if m.call == nil {
		return assistantText("好的"), nil
	}
	message := assistantText("")
	if m.reasoning != nil {
		message.ContentBlocks = append(message.ContentBlocks, m.reasoning)
	}
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
func testPersonalAgentComputer(t *testing.T, db *bun.DB, identity *servermodels.Identity, employee *agentaction.Agent, tasks *servertest.Tasks) {
	ctx := context.Background()
	installID := uuid.NewV7().String()
	registered, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, identity, computeraction.RegisterInput{InstallID: installID, Name: "测试电脑"})
	require.NoError(t, err)
	personalAgent, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, identity, registered.Record.ID, agentaction.PersonalAgentInput{
		DisplayName: "小码", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
			ModelID: employee.Execution.Managed.Model.ID,
		}},
	})
	require.NoError(t, err)
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	chat := &scriptedComputerModel{}
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return chat, nil }
	fixture := &personalAgentFixture{
		t: t, ctx: ctx, db: db, identity: identity, personalAgent: personalAgent, tasks: tasks, installID: installID, computer: registered, model: chat,
		stopper:   newTestAgentRun(db, tasks, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil),
		runner:    newTestAgentRun(db, tasks, runtime, modelcall.New(db, upstreams, nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil),
		sendFirst: directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)),
		send:      directchataction.NewSendAgentTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(tasks)),
	}
	t.Run("个人 AI 员工归属负责人", func(t *testing.T) {
		require.Equal(t, identity.User.ID, personalAgent.ResponsibleUserID)
		require.Equal(t, registered.Record.ID, personalAgent.ComputerID)
		require.Equal(t, domain.PersonalAgentPresenceOffline, personalAgent.Presence())
		fixture.online()
		reloaded, _, err := agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, identity, personalAgent.ID)
		require.NoError(t, err)
		require.Equal(t, domain.PersonalAgentPresenceOnline, reloaded.Presence(), "online personalAgent")
		other := newChatLockUser(t, db, identity)
		// 其他成员不能用别人的电脑创建个人 AI 员工，也不能与别人的个人 AI 员工单聊。
		_, err = agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, other, registered.Record.ID, agentaction.PersonalAgentInput{
			DisplayName: "冒用", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: employee.Execution.Managed.Model.ID}},
		})
		require.ErrorIs(t, err, agentaction.ErrPersonalAgentComputerNotFound, "create on foreign computer")
		_, err = fixture.sendFirst.Execute(ctx, other, directchataction.FirstAgentTextMessageInput{
			ConversationID: uuid.NewV7().String(), AgentIdentityID: personalAgent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "借用",
		})
		require.ErrorIs(t, err, conversationaction.ErrAgentTargetNotFound, "foreign personalAgent chat")
		// AI 员工管理入口不能修改个人 AI 员工。
		_, err = agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusInactive)
		require.Error(t, err, "agent status action changed personalAgent")
		_, err = agentaction.NewGetAgentQuery(db).Execute(ctx, identity, personalAgent.ID)
		require.ErrorIs(t, err, agentaction.ErrNotFound, "agent query read personalAgent")
	})

	t.Run("运行使用电脑上报的能力", func(t *testing.T) {
		capabilities := domain.ComputerCapabilities{
			Shell: "bash", FolderRoot: "/Users/test/Documents/App",
			MCPServers: []domain.ComputerMCPServer{{Name: "notes", Tools: []domain.ComputerMCPTool{{Name: "append", Description: "追加笔记", InputSchema: json.RawMessage(`{"type":"object"}`)}}}},
			Skills:     []domain.ComputerSkill{{Name: "xlsx", Description: "处理 Excel 表格", Dir: "/Users/test/.app/skills/xlsx"}},
		}
		require.NoError(t, computeraction.NewReportCapabilitiesAction(db).Execute(ctx, fixture.computerIdentity(), computeraction.CapabilitiesInput{Capabilities: capabilities, ExecutorVersion: "1", MaxConcurrency: 2}))
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "看看有哪些工具")
		inspect := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			for _, name := range agentruntime.ComputerTools() {
				assert.Contains(t, request.Assignment.Tools, name)
			}
			assert.True(t, request.Computer != nil && request.ComputerCapabilities.Skills[0].Name == "xlsx" && request.Memory != nil && request.Assignment.Memory,
				"request computer=%v capabilities=%+v memory=%v", request.Computer, request.ComputerCapabilities, request.Assignment.Memory)
			assert.True(t, request.ComputerCapabilities.MCPServers[0].Name == "notes" && request.ComputerAccess.Grant == nil,
				"capabilities=%+v access=%+v", request.ComputerCapabilities, request.ComputerAccess)
			return completeTestRun(ctx, feed, "看过了")
		}}
		fixture.execute(inspect, run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		// AI 员工的运行不使用电脑。
		_, employeeRun := createAgentLockChat(t, ctx, db, identity, employee.IdentityID, tasks)
		employeeRuntime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
			assert.False(t, request.Computer != nil || slices.ContainsFunc(request.Assignment.Tools, agentruntime.IsComputerTool),
				"employee request computer=%v tools=%v", request.Computer, request.Assignment.Tools)
			return completeTestRun(ctx, feed, "好的")
		}}
		fixture.execute(employeeRuntime, employeeRun.ID)
	})

	t.Run("个人 AI 员工记忆", func(t *testing.T) {
		testAgentMemory(t, fixture)
	})

	t.Run("委派本机 Agent", func(t *testing.T) {
		testLocalAgentDelegation(t, fixture, employee.Execution.Managed.Model.ID)
	})

	t.Run("电脑操作的派发、领取与上报", func(t *testing.T) {
		fixture.online()
		conversationID := fixture.personalAgentChat()
		run := fixture.sendAndLoadRun(conversationID, "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		// 运行在带串联编号的作用域中执行，派发的操作记下该编号，执行器领取时一并拿到。
		traceID := logscope.NewTraceID()
		done := fixture.simulateExecutor(func(operation computeraction.Operation) *domain.ComputerOutcome {
			assert.Equal(t, domain.ComputerOperationReadFile, operation.Operation.Kind)
			assert.Equal(t, "notes.txt", operation.Operation.Path)
			assert.Equal(t, conversationID, operation.Operation.Folder)
			assert.Equal(t, traceID, operation.TraceID)
			return &domain.ComputerOutcome{Output: "     1\t会议纪要", Path: "/Users/test/notes.txt", Hash: "h1"}
		})
		require.NoError(t, fixture.runner.Execute(logscope.WithTrace(fixture.ctx, traceID), agentrunaction.RunInput{RunID: run.ID}))
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "结果：     1\t会议纪要")
		call := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallSucceeded), call.Status)
		require.NotNil(t, call.Operation.Outcome)
		require.Equal(t, "h1", call.Operation.Outcome.Hash)
		require.NotNil(t, call.Result)
		require.Equal(t, "     1\t会议纪要", *call.Result)
	})

	t.Run("电脑上报结果后立即唤醒等待的运行", func(t *testing.T) {
		channel := servertest.BusChannel()
		startTestPublisherOn(t, openRealtimeDB(t), channel)
		// 另一服务端实例订阅调用的结果写入信号。
		other := servertest.StartBus(t, openRealtimeDB(t), channel, uuid.NewV7().String())
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		remote := make(chan struct{}, 1)
		var completed time.Time
		done := fixture.simulateExecutor(func(operation computeraction.Operation) *domain.ComputerOutcome {
			unsubscribe, err := other.Subscribe(realtime.Topic(identity.Workspace.ID, realtime.AudienceAgentToolCall, operation.ID), func([]byte) {
				select {
				case remote <- struct{}{}:
				default:
				}
			})
			if !assert.NoError(t, err, "subscribe tool call signal") {
				return nil
			}
			t.Cleanup(unsubscribe)
			assert.NoError(t, other.Sync(ctx), "sync tool call signal")
			completed = time.Now()
			return &domain.ComputerOutcome{Output: "     1\t会议纪要"}
		})
		fixture.run(run.ID)
		<-done
		// 结果提交到运行结束的耗时明显短于兜底读取间隔。
		require.Less(t, time.Since(completed), wakeSignalTimeout, "run woken after result")
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "结果：     1\t会议纪要")
		select {
		case <-remote:
		case <-time.After(wakeSignalTimeout):
			require.Fail(t, "other instance did not receive the tool call signal")
		}
	})

	t.Run("电脑离线时直接失败", func(t *testing.T) {
		fixture.offline()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, agentcontract.ErrComputerOffline.Error())
		var calls []servermodels.AgentToolCall
		require.NoError(t, db.NewSelect().Model(&calls).Where("atc.agent_run_id = ?", run.ID).Scan(ctx))
		require.Len(t, calls, 1)
		require.Nil(t, calls[0].ComputerID)
		require.Equal(t, string(domain.AgentToolCallFailed), calls[0].Status)
	})

	t.Run("结果超出等待时长时挂起，电脑掉线后结算并唤醒", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		dispatched := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallQueued), dispatched.Status)
		require.Nil(t, dispatched.StartedAt)
		fixture.offline()
		require.NoError(t, computeraction.NewSweepAction(db, newTestToolDecisions(db, tasks)).Execute(ctx, struct{}{}))
		call := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallFailed), call.Status)
		require.NotNil(t, call.Error)
		require.Equal(t, agentcontract.ErrComputerOffline.Error(), *call.Error)
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, agentcontract.ErrComputerOffline.Error())
	})

	t.Run("恢复状态的保存格式", func(t *testing.T) {
		testAgentRunCheckpoint(t, fixture)
	})

	t.Run("撤销电脑结算执行中的调用", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "清理临时文件")
		fixture.model.use("execute", `{"command":"rm -rf tmp"}`)
		done := fixture.simulateExecutor(func(computeraction.Operation) *domain.ComputerOutcome { return nil })
		fixture.run(run.ID)
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		require.NoError(t, computeraction.NewRevokeComputerAction(db, newTestToolDecisions(db, tasks)).Execute(ctx, identity, registered.Record.ID))
		require.Equal(t, string(domain.AgentToolCallNeedsReview), fixture.computerCall(run.ID).Status, "revoked call")
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "已交由人工核对")
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
		operations := computeraction.NewOperationsAction(db, newTestToolDecisions(db, tasks))
		held := fixture.computerCall(run.ID)
		_, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4, Running: []string{held.ID}})
		require.NoError(t, err)
		require.Equal(t, string(domain.AgentToolCallRunning), fixture.computerCall(run.ID).Status, "held call")
		_, err = operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
		require.NoError(t, err)
		require.Equal(t, string(domain.AgentToolCallNeedsReview), fixture.computerCall(run.ID).Status, "released call")
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "已交由人工核对")
	})

	t.Run("执行器丢失的可重新执行调用重新派发，达到领取上限后中断，补报的结果补记实际结果", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "读一下会议纪要")
		fixture.model.use("read_file", `{"file_path":"notes.txt"}`)
		done := fixture.simulateExecutor(func(computeraction.Operation) *domain.ComputerOutcome { return nil })
		fixture.run(run.ID)
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		// 执行器每次领取都不再持有该调用：领取次数未达上限时重新派发并在同一次领取中交给执行器。
		operations := computeraction.NewOperationsAction(db, newTestToolDecisions(db, tasks))
		for claims := 2; claims <= 3; claims++ {
			claimed, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
			require.NoError(t, err)
			call := fixture.computerCall(run.ID)
			require.Len(t, claimed.Operations, 1, "claims %d", claims)
			require.Equal(t, call.ID, claimed.Operations[0].ID, "claims %d", claims)
			require.Equal(t, domain.ComputerOperationTimeout, claimed.Operations[0].Timeout, "claims %d", claims)
			require.Equal(t, string(domain.AgentToolCallRunning), call.Status, "claims %d", claims)
			require.Equal(t, claims, call.Claims, "claims %d", claims)
		}
		_, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
		require.NoError(t, err)
		exhausted := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallInterrupted), exhausted.Status)
		require.Equal(t, 3, exhausted.Claims)
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "可以重新调用")
		// 已中断的调用结果未知，执行器补报的执行结果补记实际结果。
		call := fixture.computerCall(run.ID)
		require.NoError(t, operations.Complete(ctx, fixture.computerIdentity(), call.ID, domain.ComputerOutcome{Output: "     1\t会议纪要"}))
		resolved := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallSucceeded), resolved.Status)
		require.NotNil(t, resolved.Result)
		require.Equal(t, "     1\t会议纪要", *resolved.Result)
	})

	t.Run("超过执行时限的调用在电脑在线时也结算，执行器补报的结果补记待核对", func(t *testing.T) {
		fixture.online()
		run := fixture.sendAndLoadRun(fixture.personalAgentChat(), "整理下载文件夹")
		fixture.model.use("execute", `{"command":"mv ~/Downloads/*.pdf ~/Documents"}`)
		done := fixture.simulateExecutor(func(computeraction.Operation) *domain.ComputerOutcome { return nil })
		fixture.run(run.ID)
		<-done
		fixture.assertStatus(run.ID, domain.AgentRunStatusWaiting)
		sweep := computeraction.NewSweepAction(db, newTestToolDecisions(db, tasks))
		require.NoError(t, sweep.Execute(ctx, struct{}{}))
		call := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallRunning), call.Status, "call within deadline")
		_, err := db.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
			Set("started_at = now() - interval '12 minutes'").Where("id = ?", call.ID).Exec(ctx)
		require.NoError(t, err)
		fixture.online()
		require.NoError(t, sweep.Execute(ctx, struct{}{}))
		require.Equal(t, string(domain.AgentToolCallNeedsReview), fixture.computerCall(run.ID).Status, "expired call")
		fixture.assertStatus(run.ID, domain.AgentRunStatusQueued)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, "已交由人工核对")
		// 已中止的补报不能确定实际结果，待核对保持不变；执行器补报的执行结果补记实际结果。
		operations := computeraction.NewOperationsAction(db, newTestToolDecisions(db, tasks))
		require.NoError(t, operations.Complete(ctx, fixture.computerIdentity(), call.ID, domain.ComputerOutcome{Error: "已中止", Aborted: true}))
		require.Equal(t, string(domain.AgentToolCallNeedsReview), fixture.computerCall(run.ID).Status, "aborted late call")
		require.NoError(t, operations.Complete(ctx, fixture.computerIdentity(), call.ID, domain.ComputerOutcome{Output: "moved 2 files"}))
		resolved := fixture.computerCall(run.ID)
		require.Equal(t, string(domain.AgentToolCallSucceeded), resolved.Status)
		require.NotNil(t, resolved.Result)
		require.Equal(t, "moved 2 files", *resolved.Result)
		require.NotNil(t, resolved.Operation.Outcome)
		require.Equal(t, "moved 2 files", resolved.Operation.Outcome.Output)
		// 结果确定后的重复补报不改变调用。
		require.NoError(t, operations.Complete(ctx, fixture.computerIdentity(), call.ID, domain.ComputerOutcome{Error: "late failure"}))
		require.Equal(t, string(domain.AgentToolCallSucceeded), fixture.computerCall(run.ID).Status, "repeated late call")
	})

	t.Run("停止回复时中止电脑上执行中的调用", func(t *testing.T) {
		operations := computeraction.NewOperationsAction(db, newTestToolDecisions(db, tasks))
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
			require.Equal(t, string(domain.AgentToolCallRunning), call.Status, "finished %v", finished)
			require.NotNil(t, call.StartedAt, "finished %v", finished)
			_, err := fixture.stopper.StopAgentReply(ctx, identity, conversationID, run.ID)
			require.NoError(t, err)
			fixture.assertStatus(run.ID, domain.AgentRunStatusCancelled)
			// 运行结束后电脑上的调用仍在执行，执行器下次领取时收到中止要求。
			require.Equal(t, string(domain.AgentToolCallRunning), fixture.computerCall(run.ID).Status, "stopping call, finished %v", finished)
			claimed, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4, Running: []string{call.ID}})
			require.NoError(t, err)
			require.Equal(t, []string{call.ID}, claimed.Abort, "finished %v", finished)
			outcome, want := domain.ComputerOutcome{Aborted: true}, domain.AgentToolCallNeedsReview
			if finished {
				// 收到中止前已经执行完的命令如实记录结果。
				outcome, want = domain.ComputerOutcome{Output: "built"}, domain.AgentToolCallSucceeded
			}
			require.NoError(t, operations.Complete(ctx, fixture.computerIdentity(), call.ID, outcome))
			settled := fixture.computerCall(run.ID)
			require.Equal(t, string(want), settled.Status, "finished %v", finished)
			require.NotNil(t, settled.CompletedAt, "finished %v", finished)
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
				ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, AgentRunID: run.ID, ModelCallID: uuid.NewV7().String(),
				ProviderCallID: uuid.NewV7().String(), Name: "remote", Source: string(domain.AgentToolSourceMCP), Arguments: "{}",
				Replayable: !sideEffects, SideEffects: sideEffects, Status: string(domain.AgentToolCallRunning),
			}
			_, err := db.NewInsert().Model(row).Exec(ctx)
			require.NoError(t, err)
			inProcess[sideEffects] = row.ID
		}
		_, err := fixture.stopper.StopAgentReply(ctx, identity, conversationID, run.ID)
		require.NoError(t, err)
		require.Equal(t, string(domain.AgentToolCallCancelled), fixture.computerCall(run.ID).Status, "pending call")
		for sideEffects, want := range map[bool]domain.AgentToolCallStatus{false: domain.AgentToolCallInterrupted, true: domain.AgentToolCallNeedsReview} {
			call := &servermodels.AgentToolCall{}
			require.NoError(t, db.NewSelect().Model(call).Where("atc.id = ?", inProcess[sideEffects]).Scan(ctx))
			require.Equal(t, string(want), call.Status, "side effects %v", sideEffects)
			require.NotNil(t, call.Result, "side effects %v", sideEffects)
		}
		claimed, err := computeraction.NewOperationsAction(db, newTestToolDecisions(db, tasks)).Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
		require.NoError(t, err)
		require.Empty(t, claimed.Operations, "claim after stop")
	})

	t.Run("暂停与恢复", func(t *testing.T) {
		conversationID := fixture.personalAgentChat()
		paused, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, true)
		require.NoError(t, err)
		require.Equal(t, domain.PersonalAgentPresencePaused, paused.Presence())
		_, err = fixture.send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "暂停中"})
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "send to paused personalAgent")
		require.Equal(t, conversationaction.ConflictReasonPersonalAgentPaused, conflict.Reason)
		_, err = agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, false)
		require.NoError(t, err)
		run := fixture.sendAndLoadRun(conversationID, "恢复后")
		_, err = fixture.stopper.StopAgentReply(ctx, identity, conversationID, run.ID)
		require.NoError(t, err)
	})

	t.Run("撤销电脑后不接收新请求", func(t *testing.T) {
		conversationID := fixture.personalAgentChat()
		require.NoError(t, computeraction.NewRevokeComputerAction(db, newTestToolDecisions(db, tasks)).Execute(ctx, identity, fixture.computer.Record.ID))
		t.Cleanup(fixture.reregister)
		// 电脑撤销后，发送前即拒绝且电脑撤销优先于暂停。
		_, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, true)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, identity, personalAgent.ID, false)
		})
		_, err = fixture.send.Execute(ctx, identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "撤销后"})
		var conflict *conversationaction.ConflictError
		require.ErrorAs(t, err, &conflict, "send to unbound personalAgent")
		require.Equal(t, conversationaction.ConflictReasonPersonalAgentUnbound, conflict.Reason)
		reloaded, _, err := agentaction.NewGetPersonalAgentQuery(db).Execute(ctx, identity, personalAgent.ID)
		require.NoError(t, err)
		require.Equal(t, domain.PersonalAgentPresenceUnbound, reloaded.Presence(), "reload personalAgent")
	})
}

// completeTestRun 认领全部输入并以指定正文结束运行。
func completeTestRun(ctx context.Context, feed einorun.Feed, content string) (agentruntime.RunResult, error) {
	triggers, err := pendingTriggers(ctx, feed, 0)
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
	return computeraction.Identity{WorkspaceID: f.identity.Workspace.ID, ComputerID: f.computer.Record.ID}
}

// online 记录测试电脑在线。
func (f *personalAgentFixture) online() {
	f.t.Helper()
	require.NoError(f.t, computeraction.NewTouchComputerAction(f.db).Execute(f.ctx, f.computerIdentity(), f.computer.Credential))
}

// offline 把测试电脑的最近在线时间改到在线时限之前。
func (f *personalAgentFixture) offline() {
	f.t.Helper()
	_, err := f.db.NewUpdate().Model((*servermodels.Computer)(nil)).
		Set("last_seen_at = now() - interval '5 minutes'").Where("id = ?", f.computer.Record.ID).Exec(f.ctx)
	require.NoError(f.t, err)
}

// reregister 以同一安装重新注册测试电脑，撤销标记清除并换用新凭据。
func (f *personalAgentFixture) reregister() {
	registered, err := computeraction.NewRegisterComputerAction(f.db).Execute(f.ctx, f.identity, computeraction.RegisterInput{InstallID: f.installID, Name: "测试电脑"})
	require.NoError(f.t, err)
	f.computer = registered
}

// simulateExecutor 模拟执行器领取一次操作，outcome 返回非空时上报该结果，为空时保持执行中；返回的通道在领取并处理后关闭。
func (f *personalAgentFixture) simulateExecutor(outcome func(computeraction.Operation) *domain.ComputerOutcome) <-chan struct{} {
	done := make(chan struct{})
	operations := computeraction.NewOperationsAction(f.db, newTestToolDecisions(f.db, f.tasks))
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			claimed, err := operations.Claim(f.ctx, f.computerIdentity(), computeraction.ClaimInput{Limit: 4})
			if !assert.NoError(f.t, err, "claim") {
				return
			}
			if len(claimed.Operations) == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if result := outcome(claimed.Operations[0]); result != nil {
				assert.NoError(f.t, operations.Complete(f.ctx, f.computerIdentity(), claimed.Operations[0].ID, *result), "complete")
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
	require.NoError(f.t, newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).
		Execute(f.ctx, agentrunaction.RunInput{RunID: runID}))
}

// run 以真实运行时与脚本化模型执行一次运行。
func (f *personalAgentFixture) run(runID string) {
	f.t.Helper()
	require.NoError(f.t, f.runner.Execute(f.ctx, agentrunaction.RunInput{RunID: runID}))
}

// computerCall 读取运行中派发到电脑的唯一一次工具调用。
func (f *personalAgentFixture) computerCall(runID string) servermodels.AgentToolCall {
	f.t.Helper()
	var call servermodels.AgentToolCall
	require.NoError(f.t, f.db.NewSelect().Model(&call).Where("atc.agent_run_id = ? AND atc.computer_id IS NOT NULL", runID).Scan(f.ctx), "computer call")
	require.NotNil(f.t, call.Operation, "computer call=%+v", call)
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
	require.NoError(f.t, f.db.NewSelect().TableExpr("agent_runs AS agr").ColumnExpr("msg.body").
		Join("JOIN messages AS msg ON msg.id = agr.response_message_id").
		Where("agr.id = ?", runID).Scan(f.ctx, &body))
	require.Contains(f.t, body, text)
}

// personalAgentChat 创建一条与测试个人 AI 员工的新单聊，停止首条消息的运行后返回会话编号。
func (f *personalAgentFixture) personalAgentChat() string {
	f.t.Helper()
	conversationID := uuid.NewV7().String()
	_, err := f.sendFirst.Execute(f.ctx, f.identity, directchataction.FirstAgentTextMessageInput{
		ConversationID: conversationID, AgentIdentityID: f.personalAgent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "开始",
	})
	require.NoError(f.t, err, "send first")
	var run servermodels.AgentRun
	require.NoError(f.t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ?", conversationID).Scan(f.ctx), "load first run")
	_, err = f.stopper.StopAgentReply(f.ctx, f.identity, conversationID, run.ID)
	require.NoError(f.t, err, "stop first run")
	return conversationID
}

// sendAndLoadRun 在会话中发送一条消息并返回因此建立的排队运行。
func (f *personalAgentFixture) sendAndLoadRun(conversationID, body string) servermodels.AgentRun {
	f.t.Helper()
	_, err := f.send.Execute(f.ctx, f.identity, directchataction.InternalTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body})
	require.NoError(f.t, err)
	var run servermodels.AgentRun
	require.NoError(f.t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(f.ctx))
	return run
}

// testWorkspaceComputerAgent 验证绑定工作区电脑的服务型 AI 员工按授权使用该电脑：需要审批的授权要求负责人，写入文件直接派发，
// 命令提交负责人审批而不派发，电脑离线时批准记为失败，在线时批准后由电脑领取执行且不随运行结束中止，结果、失败与凭据重置后的待核对都作为事件唤醒 AI 员工；
// 会话文件夹取会话编号，不读取个人 AI 员工的记忆；撤销电脑后解除绑定与授权，运行不再提供电脑工具。
func testWorkspaceComputerAgent(t *testing.T, db *bun.DB, identity *servermodels.Identity, agent *agentaction.Agent, tasks *servertest.Tasks) {
	ctx := context.Background()
	added, err := computeraction.NewCreateWorkspaceComputerAction(db).Execute(ctx, identity, "构建服务器")
	require.NoError(t, err)
	bind := func(responsible string) error {
		_, err := agentaction.NewUpdateAgentAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agent.ID, agentaction.UpdateInput{
			DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: agent.ServiceAudiences, ResponsibleUserID: responsible,
			ComputerID: added.Record.ID, ComputerGrant: domain.ToolGrant{MaxLevel: domain.OperationLevelL3}, WorkStatus: domain.WorkStatusWorking,
		})
		return err
	}
	var fields *common.FieldError
	require.ErrorAs(t, bind(""), &fields, "命令需要审批时不指定负责人")
	require.Equal(t, agentaction.ValidationResponsibleRequired, fields.Fields["responsibleUserId"])
	require.NoError(t, bind(identity.User.ID))
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
	// start 与 AI 员工新建单聊并返回首条消息的排队运行。
	start := func() servermodels.AgentRun {
		t.Helper()
		conversationID := uuid.NewV7().String()
		_, err := fixture.sendFirst.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: agent.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "看下构建目录",
		})
		require.NoError(t, err)
		var run servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ?", conversationID).Scan(ctx))
		return run
	}
	// resolved 确认会话中写入了一条结果事件，并执行它唤醒的运行。
	resolved := func(conversationID string) {
		t.Helper()
		var run servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx), "event run")
		chat.call = nil
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		count, err := db.NewSelect().Model((*servermodels.Message)(nil)).
			Where("conversation_id = ? AND system_event_type = ?", conversationID, domain.ConversationSystemEventAgentToolCallResolved).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(1), count, "resolved events")
	}
	// submit 让 AI 员工在新单聊中执行命令，返回提交审批的调用。
	submit := func(command string) (servermodels.AgentRun, servermodels.AgentToolCall) {
		t.Helper()
		run := start()
		chat.use("execute", `{"command":"`+command+`"}`)
		fixture.run(run.ID)
		fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
		fixture.assertReply(run.ID, agentcontract.SubmittedResult(domain.ToolInterventionApproval))
		return run, fixture.computerCall(run.ID)
	}

	fixture.online()
	run := start()
	chat.use("write_file", `{"file_path":"notes.txt","content":"v1"}`)
	done := fixture.simulateExecutor(func(operation computeraction.Operation) *domain.ComputerOutcome {
		assert.Equal(t, domain.ComputerOperationWriteFile, operation.Operation.Kind)
		assert.Equal(t, run.ConversationID, operation.Operation.Folder)
		assert.False(t, operation.Operation.Confined, "internal run confined")
		return &domain.ComputerOutcome{Output: "已写入：notes.txt", Path: "/srv/notes.txt", Hash: "h1"}
	})
	fixture.run(run.ID)
	<-done
	fixture.assertStatus(run.ID, domain.AgentRunStatusSucceeded)
	fixture.assertReply(run.ID, "结果：已写入：notes.txt")
	call := fixture.computerCall(run.ID)
	require.Equal(t, added.Record.ID, *call.ComputerID)
	require.Equal(t, string(domain.AgentToolCallSucceeded), call.Status)
	require.Equal(t, string(domain.OperationLevelL2), *call.Level)
	require.Nil(t, call.Intervention)
	// snapshot 读取运行固定的有效配置。
	snapshot := func(runID string) agentruntime.Assignment {
		t.Helper()
		var raw string
		require.NoError(t, db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("behavior_snapshot").Where("id = ?", runID).Scan(ctx, &raw))
		var assignment agentruntime.Assignment
		require.NoError(t, json.Unmarshal([]byte(raw), &assignment))
		return assignment
	}
	assignment := snapshot(run.ID)
	require.False(t, assignment.Memory)
	require.Contains(t, assignment.Tools, "execute")
	require.Contains(t, assignment.Instruction, "工作区电脑「构建服务器」")

	// 命令提交审批，电脑在线也不领取；电脑离线时批准记为失败并唤醒。
	run, call = submit("ls build")
	require.Equal(t, string(domain.AgentToolCallAwaitingDecision), call.Status)
	require.Equal(t, string(domain.ToolInterventionApproval), *call.Intervention)
	require.Equal(t, string(domain.OperationLevelL3), *call.Level)
	require.Equal(t, domain.ComputerOperation{Kind: domain.ComputerOperationCommand, Folder: run.ConversationID, Command: "ls build"}, call.Operation.Operation)
	claimed, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
	require.NoError(t, err)
	require.Empty(t, claimed.Operations, "awaiting call claimed")
	pending, err := decisions.List(ctx, identity)
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(pending, func(item agentprocess.ToolDecision) bool {
		return item.ID == call.ID && item.ComputerName != nil && *item.ComputerName == "构建服务器"
	}), "pending = %+v", pending)
	fixture.offline()
	require.NoError(t, decisions.Decide(ctx, identity, call.ID, true))
	call = fixture.computerCall(run.ID)
	require.Equal(t, string(domain.AgentToolCallFailed), call.Status)
	require.Equal(t, agentcontract.ErrComputerOffline.Error(), *call.Error)
	resolved(run.ConversationID)

	// 在线时批准后由电脑领取，领取到的操作带有审批请求的串联编号；运行已结束也不中止，上报结果后唤醒。
	fixture.online()
	run, call = submit("make release")
	approval := logscope.NewTraceID()
	require.NoError(t, decisions.Decide(logscope.WithTrace(ctx, approval), identity, call.ID, true))
	require.Equal(t, string(domain.AgentToolCallQueued), fixture.computerCall(run.ID).Status)
	claimed, err = operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
	require.NoError(t, err)
	require.Len(t, claimed.Operations, 1)
	require.Equal(t, call.ID, claimed.Operations[0].ID)
	require.Equal(t, approval, claimed.Operations[0].TraceID)
	held, err := operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 0, Running: []string{call.ID}})
	require.NoError(t, err)
	require.Empty(t, held.Abort, "approved call aborted after run ended")
	require.NoError(t, operations.Complete(ctx, fixture.computerIdentity(), call.ID, domain.ComputerOutcome{Output: "released"}))
	call = fixture.computerCall(run.ID)
	require.Equal(t, string(domain.AgentToolCallSucceeded), call.Status)
	require.Equal(t, "released", *call.Result)
	resolved(run.ConversationID)

	// 重置凭据后批准后执行中的命令记为待核对并唤醒，电脑记为离线。
	run, call = submit("make deploy")
	require.NoError(t, decisions.Decide(ctx, identity, call.ID, true))
	claimed, err = operations.Claim(ctx, fixture.computerIdentity(), computeraction.ClaimInput{Limit: 4})
	require.NoError(t, err)
	require.Len(t, claimed.Operations, 1)
	reset, err := computeraction.NewResetComputerCredentialAction(db, newTestToolDecisions(db, tasks)).Execute(ctx, identity, added.Record.ID)
	require.NoError(t, err)
	require.Equal(t, string(domain.AgentToolCallNeedsReview), fixture.computerCall(run.ID).Status, "call after reset")
	require.False(t, reset.Record.Online)
	require.Equal(t, 1, reset.Record.AgentCount)
	resolved(run.ConversationID)
	fixture.computer = reset
	fixture.online()

	// 撤销电脑后解除绑定与授权，同一员工的新运行不再提供电脑工具。
	require.NoError(t, computeraction.NewRevokeComputerAction(db, newTestToolDecisions(db, tasks)).Execute(ctx, identity, added.Record.ID))
	var stored servermodels.Agent
	require.NoError(t, db.NewSelect().Model(&stored).Where("id = ?", agent.ID).Scan(ctx))
	require.Nil(t, stored.ComputerID)
	require.Nil(t, stored.ComputerGrant)
	run = start()
	chat.call = nil
	fixture.run(run.ID)
	require.NotContains(t, snapshot(run.ID).Tools, "execute", "tools after revoke")
}
