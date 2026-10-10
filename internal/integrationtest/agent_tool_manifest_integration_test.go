//go:build server

package integrationtest

// 本文件经真实执行指派与 Eino 运行时锁定各场景的工具清单不变量：运行快照中的工具清单与模型实际可见的工具名称一致。

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/go-cmp/cmp"
	modelprovider "github.com/runforyou-ai/einorun/provider"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	websearchaction "github.com/runforyou-ai/luway/internal/actions/websearch"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/websearch"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// manifestCaptureModel 记录每次模型调用收到的工具名称并直接以文本回答。
type manifestCaptureModel struct {
	mu    sync.Mutex
	calls [][]string
}

// Generate 记录工具名称后返回固定回答。
func (m *manifestCaptureModel) Generate(_ context.Context, _ []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0)
	for _, info := range model.GetCommonOptions(&model.Options{}, opts...).Tools {
		names = append(names, info.Name)
	}
	m.calls = append(m.calls, names)
	return assistantText("好的"), nil
}

// Stream 以单个分片返回模型输出。
func (m *manifestCaptureModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.AgenticMessage{message}), nil
}

// take 返回并清空自上次读取后首次带工具的模型调用收到的工具名称。
func (m *manifestCaptureModel) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() { m.calls = nil }()
	for _, names := range m.calls {
		if len(names) > 0 {
			return names
		}
	}
	return nil
}

// manifestFixture 保存清单用例共用的工作区、执行入口与捕获模型；t 是当前执行的子测试，子测试依次执行。
type manifestFixture struct {
	t         *testing.T
	ctx       context.Context
	db        *bun.DB
	identity  *servermodels.Identity
	modelID   string
	tasks     *agentrunaction.Scheduler
	runner    *testAgentRun
	model     *manifestCaptureModel
	sendFirst *directchataction.SendFirstAgentTextMessageAction
}

// manifestQueuedRun 读取会话中唯一排队的运行。
func (f *manifestFixture) manifestQueuedRun(conversationID string) servermodels.AgentRun {
	f.t.Helper()
	var run servermodels.AgentRun
	require.NoError(f.t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(f.ctx))
	return run
}

// manifestExecute 执行运行，返回运行快照中的工具清单与模型可见的工具名称。
func (f *manifestFixture) manifestExecute(run servermodels.AgentRun) (agentruntime.Assignment, []string) {
	f.t.Helper()
	require.NoError(f.t, f.runner.Execute(f.ctx, agentrunaction.RunInput{RunID: run.ID}))
	var raw string
	require.NoError(f.t, f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("behavior_snapshot").Where("id = ?", run.ID).Scan(f.ctx, &raw))
	var assignment agentruntime.Assignment
	require.NoError(f.t, json.Unmarshal([]byte(raw), &assignment), "snapshot = %s", raw)
	return assignment, f.model.take()
}

// manifestNewAgent 创建指定服务对象的托管 AI 员工。
func (f *manifestFixture) manifestNewAgent(name string, audiences ...domain.ServiceAudience) *agentaction.Agent {
	f.t.Helper()
	agent, err := agentaction.NewCreateAgentAction(f.db).Execute(f.ctx, f.identity, agentaction.CreateInput{
		DisplayName: name + " " + servertest.UniqueSuffix(), ServiceAudiences: audiences,
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: f.modelID}},
	})
	require.NoError(f.t, err)
	return agent
}

// manifestWebsiteConversation 建立网站渠道并以访客消息开启客户会话，target 为新会话的接待对象，为空时进入公共队列。
func (f *manifestFixture) manifestWebsiteConversation(target string) string {
	f.t.Helper()
	routing := channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}
	if target != "" {
		routing = channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: target}
	}
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(f.ctx, f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "清单渠道 " + servertest.UniqueSuffix(), DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: routing, FallbackTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(f.t, err)
	inbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, f.tasks, testEnqueuer, servertest.DisabledMail{}).Execute(f.ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channel.ID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "退货政策是什么",
	})
	require.NoError(f.t, err)
	return inbound.Conversation.ID
}

// TestAgentToolManifest 验证 AI 单聊、群聊、客服、副驾驶与员工服务场景中，运行快照的工具清单与模型可见工具一致：
// 企业启用联网搜索时只有内部场景提供联网搜索与网页读取，服务场景提供追问、转人工与结束服务，内部场景提供任务清单与委派，共享文件工具在各场景提供。
func TestAgentToolManifest(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	_, err := websearchaction.NewUpdateSettingsAction(db).Execute(ctx, identity, &websearch.Config{Provider: domain.WebSearchProviderTavily, APIKey: "tvly-key"})
	require.NoError(t, err)
	tasks := servertest.NewTasks()
	runtime, err := agentruntime.New()
	require.NoError(t, err)
	capture := &manifestCaptureModel{}
	upstreams := fakeUpstreams(nil, nil)
	upstreams.Chat = func(context.Context, modelprovider.ChatConfig) (model.AgenticModel, error) { return capture, nil }
	local, err := serverfilecontent.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	s3 := func() serverfilecontent.S3Config { return serverfilecontent.S3Config{} }
	files := conversationfile.NewStore(db, func() domain.FileStorageBackend { return domain.FileStorageBackendLocal },
		serverfilecontent.NewWriter(local, s3), serverfilecontent.NewReader(local, s3))
	scheduler := agentrunaction.NewScheduler(tasks)
	f := &manifestFixture{
		t: t, ctx: ctx, db: db, identity: identity, modelID: modelID, tasks: scheduler, model: capture,
		runner:    newTestAgentRun(db, tasks, runtime, modelcall.New(db, upstreams, nil), testAttachmentReader(db), nil, servertest.DisabledMail{}, files, nil),
		sendFirst: directchataction.NewSendFirstAgentTextMessageAction(db, testEnqueuer, scheduler),
	}
	fileTools := []string{"read_file", "write_file", "edit_file", "list_shared_files", "save_attachment"}
	orchestration := []string{"TaskCreate", "TaskGet", "TaskUpdate", "TaskList", "agent"}
	terminal := []string{"ask_customer", "handoff_to_human", "resolve_conversation"}
	offloaded := []string{"read_offloaded_tool_result"}
	internal := slices.Concat([]string{"web_search", "web_fetch"}, fileTools, orchestration, offloaded)
	service := slices.Concat(fileTools, []string{"search_customer_history"}, terminal, offloaded)
	// check 核对场景与快照清单，并断言模型可见工具等于快照清单。
	check := func(t *testing.T, assignment agentruntime.Assignment, visible []string, scene agentruntime.Scene, tools []string) {
		t.Helper()
		require.Equal(t, scene, assignment.Scene)
		require.Empty(t, cmp.Diff(tools, assignment.Tools), "快照工具清单")
		want := slices.Clone(assignment.Tools)
		slices.Sort(want)
		visible = slices.Clone(visible)
		slices.Sort(visible)
		require.Empty(t, cmp.Diff(want, visible), "模型可见工具 = 快照清单")
	}
	member := f.manifestNewAgent("清单助手")
	customer := f.manifestNewAgent("清单客服", domain.ServiceAudienceCustomer)

	t.Run("AI 单聊", func(t *testing.T) {
		f.t = t
		conversationID := uuid.NewV7().String()
		_, err := f.sendFirst.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: member.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "查一下政策",
		})
		require.NoError(t, err)
		assignment, visible := f.manifestExecute(f.manifestQueuedRun(conversationID))
		check(t, assignment, visible, agentruntime.SceneAgentChat, internal)
	})

	t.Run("群聊点名", func(t *testing.T) {
		f.t = t
		group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
			Title: "清单群", MemberIdentityIDs: []string{member.IdentityID},
		})
		require.NoError(t, err)
		detail, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
		require.NoError(t, err)
		var subjectID string
		for _, participant := range detail.Participants {
			if participant.IdentityID == member.IdentityID {
				subjectID = participant.ChatSubjectID
			}
		}
		_, err = groupchataction.NewSendGroupTextMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "帮忙看看", MentionSubjectIDs: []string{subjectID},
		})
		require.NoError(t, err)
		assignment, visible := f.manifestExecute(f.manifestQueuedRun(group.ID))
		check(t, assignment, visible, agentruntime.SceneGroup, internal)
	})

	t.Run("客服", func(t *testing.T) {
		f.t = t
		conversationID := f.manifestWebsiteConversation(customer.IdentityID)
		assignment, visible := f.manifestExecute(f.manifestQueuedRun(conversationID))
		check(t, assignment, visible, agentruntime.SceneCustomer, service)
		require.Equal(t, agentruntime.GroundingStrict, assignment.Grounding)
	})

	t.Run("副驾驶", func(t *testing.T) {
		f.t = t
		servedID := f.manifestWebsiteConversation("")
		threadID := uuid.NewV7().String()
		_, err := directchataction.NewSendFirstServiceCopilotMessageAction(db, testEnqueuer, scheduler).Execute(ctx, identity, directchataction.FirstServiceCopilotMessageInput{
			ThreadID: threadID, ServedConversationID: servedID, AgentIdentityID: customer.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "客户之前问过什么",
		})
		require.NoError(t, err)
		assignment, visible := f.manifestExecute(f.manifestQueuedRun(threadID))
		// 副驾驶属于内部场景，另可检索所服务客户的历史沟通。
		check(t, assignment, visible, agentruntime.SceneCopilot, slices.Concat([]string{"web_search", "web_fetch"}, fileTools, orchestration, []string{"search_customer_history"}, offloaded))
	})

	t.Run("员工服务", func(t *testing.T) {
		f.t = t
		employee := f.manifestNewAgent("清单服务台", domain.ServiceAudienceEmployee)
		conversationID := uuid.NewV7().String()
		_, err := f.sendFirst.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: employee.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "电脑连不上网",
		})
		require.NoError(t, err)
		assignment, visible := f.manifestExecute(f.manifestQueuedRun(conversationID))
		// 员工服务不关联客户，不提供客户历史检索。
		check(t, assignment, visible, agentruntime.SceneEmployeeService, slices.DeleteFunc(slices.Clone(service), func(name string) bool { return name == agentruntime.CustomerHistoryToolName }))
	})

	// 重复执行尝试以已写入的快照为准：快照写入后企业关闭联网搜索时，web_search 仍按快照注册，调用时返回不可用，清单与注册结果保持一致。
	t.Run("沿用快照后联网搜索关闭", func(t *testing.T) {
		f.t = t
		conversationID := uuid.NewV7().String()
		_, err := f.sendFirst.Execute(ctx, identity, directchataction.FirstAgentTextMessageInput{
			ConversationID: conversationID, AgentIdentityID: member.IdentityID, ClientMessageID: uuid.NewV7().String(), Body: "再查一次",
		})
		require.NoError(t, err)
		run := f.manifestQueuedRun(conversationID)
		snapshot := agentruntime.ResolveAssignment(agentruntime.AssignmentFacts{AgentName: member.DisplayName, Scene: agentruntime.SceneContext{Scene: agentruntime.SceneAgentChat}},
			agentruntime.Capabilities{WebSearch: true, WebFetch: true, SharedFiles: true})
		snapshot.Model = agentruntime.AssignmentModel{ModelID: modelID, MaxOutputTokens: 4096, ContextWindow: 128000, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}}
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		_, err = db.NewUpdate().Model((*servermodels.AgentRun)(nil)).Set("behavior_snapshot = ?::jsonb", string(encoded)).Where("id = ?", run.ID).Exec(ctx)
		require.NoError(t, err)
		_, err = websearchaction.NewUpdateSettingsAction(db).Execute(ctx, identity, nil)
		require.NoError(t, err)
		assignment, visible := f.manifestExecute(run)
		require.Contains(t, assignment.Tools, agentruntime.WebSearchToolName, "快照清单")
		require.Contains(t, visible, agentruntime.WebSearchToolName, "模型按快照看到联网搜索")
		want := slices.Clone(assignment.Tools)
		slices.Sort(want)
		visible = slices.Clone(visible)
		slices.Sort(visible)
		require.Empty(t, cmp.Diff(want, visible))
	})
}
