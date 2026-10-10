//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	seataction "github.com/runforyou-ai/luway/internal/actions/seat"
	servicecategoryaction "github.com/runforyou-ai/luway/internal/actions/servicecategory"
	servicehandoffaction "github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
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

// testServiceSessionReturner 创建管理操作退回服务周期所用的运行协调器。
func testServiceSessionReturner(db *bun.DB) *testAgentRun {
	return newTestAgentRun(db, testEnqueuer, nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
}

// testUserStatusAction 创建测试用的成员账号状态修改操作。
func testUserStatusAction(db *bun.DB) *useraction.UpdateStatusAction {
	coordinator := testServiceSessionReturner(db)
	return useraction.NewUpdateStatusAction(db, testEnqueuer, seataction.Seats{}, coordinator, groupchataction.NewPersonalAgentRetirer(testEnqueuer, coordinator))
}

// handoffFixture 保存转人工集成测试共用的企业身份、任务登记器与客服角色。
type handoffFixture struct {
	db         *bun.DB
	identity   *servermodels.Identity
	tasks      *servertest.Tasks
	providerID string
	modelID    string
}

// newAgent 创建一个开启接待客户的 AI 员工。
func (f handoffFixture) newAgent(t *testing.T, name string) *agentaction.Agent {
	t.Helper()
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(context.Background(), f.identity, agentaction.CreateInput{
		DisplayName: name, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer},
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: f.modelID}},
	})
	require.NoError(t, err)
	return created
}

// newChannel 创建初始路由到指定 AI 员工的网站渠道并返回渠道编号。
func (f handoffFixture) newChannel(t *testing.T, agentIdentityID string, fallback channelaction.RoutingTarget) string {
	t.Helper()
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(context.Background(), f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "转人工验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agentIdentityID},
		FallbackTarget:        fallback,
	})
	require.NoError(t, err)
	return channel.ID
}

// visitorInput 为新访客构造一条网站消息。
func visitorInput(channelID, body string) customerchataction.WebsiteCustomerTextMessageInput {
	return customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""),
		ClientMessageID: uuid.NewV7().String(), Body: body,
	}
}

// receive 写入访客消息并返回结果，后续消息沿用同一会话。
func (f handoffFixture) receive(t *testing.T, input *customerchataction.WebsiteCustomerTextMessageInput, body string) customerchataction.ReceiveWebsiteCustomerMessageResult {
	t.Helper()
	input.ClientMessageID, input.Body = uuid.NewV7().String(), body
	result, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(f.tasks), testEnqueuer, servertest.DisabledMail{}).Execute(context.Background(), *input)
	require.NoError(t, err)
	input.ConversationID = &result.Conversation.ID
	return result
}

// queuedRun 读取会话中排队的运行。
func (f handoffFixture) queuedRun(t *testing.T, conversationID string) servermodels.AgentRun {
	t.Helper()
	run := servermodels.AgentRun{}
	require.NoError(t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(context.Background()))
	return run
}

// handoffRuntime 认领首批输入后按需执行插入动作，再返回模型给出的转人工决定。
func handoffRuntime(reasonText string, during func()) *testAgentRuntime {
	return categoryHandoffRuntime(reasonText, "", during)
}

// categoryHandoffRuntime 认领首批输入后按需执行插入动作，再返回带咨询分类编号的转人工决定。
func categoryHandoffRuntime(reasonText, categoryID string, during func()) *testAgentRuntime {
	return &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		if during != nil {
			during()
		}
		return agentruntime.RunResult{EndSeq: claimed.EndSeq, Decision: agentcontract.TerminalDecision{
			Kind: domain.AgentRunOutcomeHandoff, Reason: domain.AgentHandoffReasonKnowledgeGap, ReasonText: reasonText, CategoryID: categoryID,
		}}, nil
	}}
}

// handoffEvents 读取会话中的转人工系统事件。
func handoffEvents(t *testing.T, db *bun.DB, conversationID string) []domain.ServiceSessionHandedOffEvent {
	t.Helper()
	var messages []servermodels.Message
	require.NoError(t, db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionHandedOff).
		OrderExpr("msg.message_seq").Scan(context.Background()))
	events := make([]domain.ServiceSessionHandedOffEvent, 0, len(messages))
	for _, message := range messages {
		require.Equal(t, string(domain.MessageVisibilityInternal), message.Visibility, "handoff event message = %+v", message)
		require.Nil(t, message.SenderParticipantID, "handoff event message = %+v", message)
		event := domain.ServiceSessionHandedOffEvent{}
		require.NoError(t, json.Unmarshal(message.SystemEventPayload, &event))
		events = append(events, event)
	}
	return events
}

// returnedEvents 读取会话中的周期退回队列事件。
func returnedEvents(t *testing.T, db *bun.DB, conversationID string) []domain.ServiceSessionReturnedEvent {
	t.Helper()
	var messages []servermodels.Message
	require.NoError(t, db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionReturned).
		OrderExpr("msg.message_seq").Scan(context.Background()))
	events := make([]domain.ServiceSessionReturnedEvent, 0, len(messages))
	for _, message := range messages {
		require.Equal(t, string(domain.MessageVisibilityInternal), message.Visibility, "returned event message = %+v", message)
		require.Nil(t, message.SenderParticipantID, "returned event message = %+v", message)
		event := domain.ServiceSessionReturnedEvent{}
		require.NoError(t, json.Unmarshal(message.SystemEventPayload, &event))
		events = append(events, event)
	}
	return events
}

// loadSession 读取客服周期的当前状态。
func loadSession(t *testing.T, db *bun.DB, sessionID string) servermodels.ServiceSession {
	t.Helper()
	session := servermodels.ServiceSession{}
	require.NoError(t, db.NewSelect().Model(&session).Where("ss.id = ?", sessionID).Scan(context.Background()))
	return session
}

// handoffNotice 读取按幂等键写入的对客通知正文。
func handoffNotice(t *testing.T, db *bun.DB, key string) string {
	t.Helper()
	notice := servermodels.Message{}
	require.NoError(t, db.NewSelect().Model(&notice).Where("msg.idempotency_key = ? AND msg.type = ?", key, domain.MessageTypeText).Scan(context.Background()), "load notice %s", key)
	return notice.Body
}

// runReturnedHandoffs 执行登记器中指定客服周期已投递的转人工承接任务，返回执行的任务数。
func runReturnedHandoffs(t *testing.T, db *bun.DB, tasks *servertest.Tasks, serviceSessionID string) int {
	t.Helper()
	ctx := context.Background()
	inputs := servertest.QueuedInputs(t, tasks, servicehandoffaction.ReturnedHandoffActionName, func(input servicehandoffaction.ReturnedHandoffInput) bool {
		return input.ServiceSessionID == serviceSessionID
	})
	executor := testServiceSessionReturner(db)
	for _, input := range inputs {
		require.NoError(t, executor.HandOffReturnedSession(ctx, input))
	}
	return len(inputs)
}

// 转人工对客话术按渠道语言 zh-CN 取词。
const (
	handoffQueuedNotice      = "已为您转接人工客服，正在排队，请稍候。"
	handoffUnscheduledNotice = "现在是非工作时间，我们已记录您的问题，工作时间内会尽快为您处理。"
)

// TestAgentHandoffs 验证 AI 客服转人工的去向、并发边界、幂等、管理操作交接与资格变更互斥。
func TestAgentHandoffs(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	tasks := servertest.NewTasks()
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	t.Run("主动转人工后转回同一 AI", func(t *testing.T) { testModelHandoffRoundTrip(t, f) })
	t.Run("失败路由去向", func(t *testing.T) { testHandoffTargets(t, f) })
	t.Run("咨询分类路由", func(t *testing.T) { testHandoffCategoryRouting(t, f) })
	t.Run("转人工自动分配", func(t *testing.T) { testHandoffAutoAssignment(t, f) })
	t.Run("工作时间与转人工话术", func(t *testing.T) { testBusinessHoursHandoffNotice(t, f) })
	t.Run("人工接管与交接先后", func(t *testing.T) { testHandoffCommitOrder(t, f) })
	t.Run("停用与关闭接待退回队列", func(t *testing.T) { testManagementReturn(t, f) })
	t.Run("去掉员工按服务对象退回", func(t *testing.T) { testRemoveEmployeeKeepsCustomerSessions(t, f) })
	t.Run("入站路由与资格变更交错", func(t *testing.T) { testInboundRoutingVersusEligibility(t, f) })
	t.Run("Telegram 主动转人工", func(t *testing.T) { testTelegramModelHandoff(t, f) })
	t.Run("渠道编辑与停用交错", func(t *testing.T) { testChannelEditVersusDeactivation(t, f) })
	t.Run("成员操作客服周期事件", func(t *testing.T) { testServiceSessionOperationEvents(t, f) })
	t.Run("Telegram 入站失效退回与运行收尾交错", func(t *testing.T) { testTelegramInboundReturnVersusRunFailure(t, f) })
}

// testModelHandoffRoundTrip 验证最终认领后到达的消息随交接结算，人工转回 AI 后只处理新消息。
func testModelHandoffRoundTrip(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "转人工客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "我要退款")
	run := f.queuedRun(t, first.Conversation.ID)
	runtime := handoffRuntime("客户要求退款", func() { f.receive(t, &input, "还在吗") })
	executor := newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	for range 2 {
		require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	}
	require.NoError(t, f.db.NewSelect().Model(&run).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status)
	require.NotNil(t, run.Outcome)
	require.Equal(t, string(domain.AgentRunOutcomeHandoff), *run.Outcome)
	require.NotNil(t, run.OutcomeReason)
	require.Equal(t, string(domain.AgentHandoffReasonKnowledgeGap), *run.OutcomeReason)
	require.NotNil(t, run.InputEndSeq)
	require.Equal(t, int64(1), *run.InputEndSeq)
	require.NotNil(t, run.HandoffSettledSeq)
	require.Equal(t, int64(2), *run.HandoffSettledSeq)
	require.NotNil(t, run.ResponseMessageID)
	lane := servermodels.AgentLane{}
	require.NoError(t, f.db.NewSelect().Model(&lane).Where("al.id = ?", run.LaneID).Scan(ctx))
	require.Equal(t, int64(2), lane.DesiredSeq)
	require.Equal(t, int64(2), lane.ProcessedSeq)
	session := loadSession(t, f.db, run.ScopeID)
	require.Nil(t, session.AssigneeIdentityID)
	require.Nil(t, session.TeamID)
	require.Equal(t, string(domain.ServiceSessionStatusOpen), session.Status)
	events := handoffEvents(t, f.db, first.Conversation.ID)
	require.Len(t, events, 1)
	require.Equal(t, domain.ServiceSessionTargetPublicQueue, events[0].Target.Kind)
	require.Equal(t, "客户要求退款", events[0].ReasonText)
	require.Equal(t, "转人工客服", events[0].FromDisplayName)
	require.NotNil(t, events[0].AgentRunID)
	require.Equal(t, run.ID, *events[0].AgentRunID)
	active, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", first.Conversation.ID, domain.AgentRunStatusQueued).Exists(ctx)
	require.NoError(t, err)
	require.False(t, active, "queued run after handoff")
	// 访客只看到两条消息和对客通知，内部原因不外露。
	visible, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channelID, ExternalID: input.ExternalID, ConversationID: first.Conversation.ID})
	require.NoError(t, err)
	require.Len(t, visible.Messages, 3)
	require.Equal(t, *run.ResponseMessageID, visible.Messages[2].ID)
	require.Equal(t, handoffQueuedNotice, visible.Messages[2].Body)
	for _, message := range visible.Messages {
		require.NotContains(t, message.Body, "客户要求退款", "internal reason leaked")
	}
	// 成员领取后转回同一 AI，新消息只触发新输入。
	coordinator := testServiceSessionReturner(f.db)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.identity, first.Conversation.ID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(f.tasks), testEnqueuer).Execute(ctx, f.identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: first.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	})
	require.NoError(t, err)
	f.receive(t, &input, "新的问题")
	next := f.queuedRun(t, first.Conversation.ID)
	reply := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
		require.NoError(t, err)
		require.Len(t, triggers, 1)
		require.Equal(t, int64(3), triggers[0].Seq)
		claimed, err := feed.Claim(ctx, triggers[0].Seq)
		return agentruntime.RunResult{Content: "新问题的回答", EndSeq: claimed.EndSeq}, err
	}}
	require.NoError(t, newTestAgentRun(f.db, f.tasks, reply, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: next.ID}))
	require.NoError(t, f.db.NewSelect().Model(&next).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), next.Status)
	require.Equal(t, int64(3), next.InputStartSeq)
	require.NotNil(t, next.Outcome)
	require.Equal(t, string(domain.AgentRunOutcomeReply), *next.Outcome)
}

// testHandoffTargets 验证转人工按渠道失败去向进入团队或公共队列，失败团队无效时进入公共队列。
func testHandoffTargets(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "去向验证客服")
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.identity, teamaction.Input{Name: "售后组 " + servertest.UniqueSuffix()})
	require.NoError(t, err)
	human, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "人工客服", Email: servertest.UniqueEmail("handoff"), Password: "password123", RoleID: f.identity.User.RoleID,
	})
	require.NoError(t, err)
	disableAutoAssignment(t, f.db, f.identity.Workspace.ID)
	// 团队队列须有开启接待的真人成员才可用。
	_, err = teamaction.NewAddMembersAction(f.db, testEnqueuer).Execute(ctx, f.identity, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: human.IdentityID},
	})
	require.NoError(t, err)
	for _, scenario := range []struct {
		name     string
		fallback channelaction.RoutingTarget
		invalid  bool
		wantKind domain.ServiceSessionTargetKind
		wantTeam *string
	}{
		{name: "团队", fallback: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeTeam, ID: team.ID}, wantKind: domain.ServiceSessionTargetTeam, wantTeam: &team.ID},
		{name: "公共队列", fallback: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}, wantKind: domain.ServiceSessionTargetPublicQueue},
		{name: "无效团队", fallback: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeTeam, ID: team.ID}, invalid: true, wantKind: domain.ServiceSessionTargetPublicQueue},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			channelID := f.newChannel(t, agent.IdentityID, scenario.fallback)
			if scenario.invalid {
				_, err := f.db.NewUpdate().Model((*servermodels.Channel)(nil)).Set("fallback_routing_target_id = ?", uuid.NewV7().String()).Where("id = ?", channelID).Exec(ctx)
				require.NoError(t, err)
			}
			input := visitorInput(channelID, "")
			first := f.receive(t, &input, "需要人工")
			run := f.queuedRun(t, first.Conversation.ID)
			require.NoError(t, newTestAgentRun(f.db, f.tasks, handoffRuntime("无法确认", nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
			session := loadSession(t, f.db, run.ScopeID)
			if scenario.wantTeam == nil {
				require.Nil(t, session.TeamID)
			} else {
				require.NotNil(t, session.TeamID)
				require.Equal(t, *scenario.wantTeam, *session.TeamID)
			}
			require.Nil(t, session.AssigneeIdentityID)
			events := handoffEvents(t, f.db, first.Conversation.ID)
			require.Len(t, events, 1)
			require.Equal(t, scenario.wantKind, events[0].Target.Kind)
			if scenario.wantKind == domain.ServiceSessionTargetTeam {
				require.NotNil(t, events[0].Target.TeamName)
				require.Equal(t, team.Name, *events[0].Target.TeamName)
			}
			require.Equal(t, handoffQueuedNotice, handoffNotice(t, f.db, "agent:"+run.ID))
		})
	}
}

// testHandoffCategoryRouting 验证 AI 选择的咨询分类写入周期与事件，分类团队可用时优先于渠道失败路由；未关联团队或团队无人接待时记下分类并按失败路由，已归档或未选择时不记分类。
func testHandoffCategoryRouting(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "分类路由验证客服")
	suffix := servertest.UniqueSuffix()
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.identity, teamaction.Input{Name: "退款组 " + suffix})
	require.NoError(t, err)
	human, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "退款客服", Email: servertest.UniqueEmail("category"), Password: "password123", RoleID: f.identity.User.RoleID,
	})
	require.NoError(t, err)
	disableAutoAssignment(t, f.db, f.identity.Workspace.ID)
	_, err = teamaction.NewAddMembersAction(f.db, testEnqueuer).Execute(ctx, f.identity, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: human.IdentityID},
	})
	require.NoError(t, err)
	refund, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "退款 " + suffix, Description: "退款、退货", TeamID: &team.ID})
	require.NoError(t, err)
	shipping, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "物流 " + suffix})
	require.NoError(t, err)
	archived, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "旧分类 " + suffix, TeamID: &team.ID})
	require.NoError(t, err)
	require.NoError(t, servicecategoryaction.NewArchiveAction(f.db).Execute(ctx, f.identity, archived.ID))
	emptyTeam, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.identity, teamaction.Input{Name: "空团队 " + suffix})
	require.NoError(t, err)
	unstaffed, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "投诉 " + suffix, TeamID: &emptyTeam.ID})
	require.NoError(t, err)
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	for _, scenario := range []struct {
		name         string
		category     *servicecategoryaction.Record
		wantTeam     *string
		wantCategory *string
	}{
		{name: "分类团队", category: refund, wantTeam: &team.ID, wantCategory: &refund.ID},
		{name: "分类未关联团队", category: shipping, wantCategory: &shipping.ID},
		{name: "分类团队无人接待", category: unstaffed, wantCategory: &unstaffed.ID},
		{name: "分类已归档", category: archived},
		{name: "未选择分类"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := visitorInput(channelID, "")
			received := f.receive(t, &input, "我要退款")
			run := f.queuedRun(t, received.Conversation.ID)
			categoryID := ""
			if scenario.category != nil {
				categoryID = scenario.category.ID
			}
			require.NoError(t, newTestAgentRun(f.db, f.tasks, categoryHandoffRuntime("客户要求退款", categoryID, nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
			session := loadSession(t, f.db, run.ScopeID)
			if scenario.wantTeam == nil {
				require.Nil(t, session.TeamID)
			} else {
				require.NotNil(t, session.TeamID)
				require.Equal(t, *scenario.wantTeam, *session.TeamID)
			}
			if scenario.wantCategory == nil {
				require.Nil(t, session.CategoryID)
			} else {
				require.NotNil(t, session.CategoryID)
				require.Equal(t, *scenario.wantCategory, *session.CategoryID)
			}
			events := handoffEvents(t, f.db, received.Conversation.ID)
			require.Len(t, events, 1)
			require.Equal(t, domain.AgentHandoffReasonKnowledgeGap, events[0].Reason)
			if scenario.wantCategory == nil {
				require.Nil(t, events[0].CategoryName)
			} else {
				require.NotNil(t, events[0].CategoryName)
				require.Equal(t, scenario.category.Name, *events[0].CategoryName)
			}
		})
	}
}

// testHandoffAutoAssignment 验证转人工进入队列时在交接事务内分配给可接待的真人成员，事件去向为实际承接成员并记录客户等待起点。
func testHandoffAutoAssignment(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	t.Cleanup(func() { disableAutoAssignment(t, f.db, f.identity.Workspace.ID) })
	agent := f.newAgent(t, "自动分配验证客服")
	human, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 1, DisplayName: "承接客服", Email: servertest.UniqueEmail("assign"), Password: "password123", RoleID: f.identity.User.RoleID,
	})
	require.NoError(t, err)
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	handOff := func() (servermodels.ServiceSession, []domain.ServiceSessionHandedOffEvent, string) {
		t.Helper()
		input := visitorInput(channelID, "")
		received := f.receive(t, &input, "需要人工")
		run := f.queuedRun(t, received.Conversation.ID)
		require.NoError(t, newTestAgentRun(f.db, f.tasks, handoffRuntime("无法确认", nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
		return loadSession(t, f.db, run.ScopeID), handoffEvents(t, f.db, received.Conversation.ID), handoffNotice(t, f.db, "agent:"+run.ID)
	}
	session, events, notice := handOff()
	require.NotNil(t, session.AssigneeIdentityID)
	require.Equal(t, human.IdentityID, *session.AssigneeIdentityID)
	require.Nil(t, session.TeamID)
	require.NotNil(t, session.AssigneeAssignedAt)
	require.NotNil(t, session.AwaitingReplySince)
	require.Len(t, events, 1)
	require.Equal(t, domain.ServiceSessionTargetMember, events[0].Target.Kind)
	require.NotNil(t, events[0].Target.IdentityID)
	require.Equal(t, human.IdentityID, *events[0].Target.IdentityID)
	require.Equal(t, "已为您转接人工客服承接客服，请稍候。", notice)
	// 承接客服满员后转人工留在公共队列。
	session, events, notice = handOff()
	require.Nil(t, session.AssigneeIdentityID)
	require.Nil(t, session.AssigneeAssignedAt)
	require.NotNil(t, session.AwaitingReplySince)
	require.Len(t, events, 1)
	require.Equal(t, domain.ServiceSessionTargetPublicQueue, events[0].Target.Kind)
	require.Equal(t, handoffQueuedNotice, notice)
}

// testBusinessHoursHandoffNotice 验证工作时间的保存与校验，以及无人可分配时按工作时间选择排队、非工作时间和无下次处理时间的话术。
func testBusinessHoursHandoffNotice(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	t.Cleanup(func() {
		_, err := f.db.ExecContext(ctx, `UPDATE customer_service_settings SET business_hours_enabled = DEFAULT, business_hours_time_zone = DEFAULT,
			business_hours_weekly = DEFAULT, business_hours_overrides = DEFAULT WHERE workspace_id = ?`, f.identity.Workspace.ID)
		assert.NoError(t, err)
	})
	update := customerserviceaction.NewUpdateBusinessHoursAction(f.db)
	// 新工作区的设置行取列默认值。
	hours, err := customerserviceaction.NewGetBusinessHoursQuery(f.db).Execute(ctx, f.identity)
	require.NoError(t, err)
	require.False(t, hours.Enabled)
	require.Equal(t, "Asia/Shanghai", hours.TimeZone)
	require.Len(t, hours.Weekly[0], 1)
	require.Empty(t, hours.Weekly[6])
	require.NotNil(t, hours.Overrides)
	// 每周时段和日期覆盖分别校验。
	invalid := hours
	invalid.Weekly[0] = []domain.BusinessHoursPeriod{{Start: "09:00", End: "13:00"}, {Start: "12:00", End: "18:00"}}
	invalid.Overrides = []domain.BusinessHoursOverride{{Date: "2026-10-01"}, {Date: "2026-10-01"}}
	var validation *customerserviceaction.ValidationError
	_, err = update.Execute(ctx, f.identity, invalid)
	require.ErrorAs(t, err, &validation)
	require.Len(t, validation.Fields, 2)
	require.Equal(t, customerserviceaction.ValidationWeeklyInvalid, validation.Fields["weekly"])
	require.Equal(t, customerserviceaction.ValidationOverrideInvalid, validation.Fields["overrides"])

	agent := f.newAgent(t, "工作时间验证客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	tomorrow := time.Now().In(location).AddDate(0, 0, 1)
	allDay := []domain.BusinessHoursPeriod{{Start: "00:00", End: "24:00"}}
	for _, scenario := range []struct {
		name      string
		weekly    []domain.BusinessHoursPeriod
		overrides []domain.BusinessHoursOverride
		want      string
	}{
		{name: "工作时间内排队", weekly: allDay, want: handoffQueuedNotice},
		{name: "非工作时间有下次处理时间", overrides: []domain.BusinessHoursOverride{
			{Date: tomorrow.Format(domain.BusinessHoursDateLayout), Periods: []domain.BusinessHoursPeriod{{Start: "13:00", End: "18:00"}, {Start: "09:30", End: "12:00"}}},
		}, want: fmt.Sprintf("现在是非工作时间，我们已记录您的问题，将于%d月%d日 09:30（GMT+8）起为您处理。", tomorrow.Month(), tomorrow.Day())},
		{name: "非工作时间无下次处理时间", want: handoffUnscheduledNotice},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			hours := domain.BusinessHours{Enabled: true, TimeZone: "Asia/Shanghai", Overrides: scenario.overrides}
			for day := range hours.Weekly {
				hours.Weekly[day] = scenario.weekly
			}
			_, err := update.Execute(ctx, f.identity, hours)
			require.NoError(t, err)
			// 保存后的日期覆盖时段按开始时间排序。
			saved, err := customerserviceaction.LoadBusinessHours(ctx, f.db, f.identity.Workspace.ID)
			require.NoError(t, err)
			require.True(t, saved.Enabled)
			require.Len(t, saved.Overrides, len(scenario.overrides))
			if len(saved.Overrides) > 0 {
				require.Equal(t, "09:30", saved.Overrides[0].Periods[0].Start)
			}
			input := visitorInput(channelID, "")
			received := f.receive(t, &input, "需要人工")
			run := f.queuedRun(t, received.Conversation.ID)
			require.NoError(t, newTestAgentRun(f.db, f.tasks, handoffRuntime("无法确认", nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
			require.Equal(t, scenario.want, handoffNotice(t, f.db, "agent:"+run.ID))
		})
	}
}

// testHandoffCommitOrder 验证人工接管先提交时抑制迟到的转人工结果，AI 交接先提交时人工基于新负责人继续操作。
func testHandoffCommitOrder(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "先后验证客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	coordinator := testServiceSessionReturner(f.db)

	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "人工先接管")
	run := f.queuedRun(t, first.Conversation.ID)
	takeover := handoffRuntime("无法确认", func() {
		_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.identity, first.Conversation.ID)
		require.NoError(t, err)
	})
	require.NoError(t, newTestAgentRun(f.db, f.tasks, takeover, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, f.db.NewSelect().Model(&run).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusCancelled), run.Status)
	require.Nil(t, run.Outcome)
	require.Nil(t, run.ResponseMessageID)
	session := loadSession(t, f.db, run.ScopeID)
	require.NotNil(t, session.AssigneeIdentityID)
	require.Equal(t, f.identity.WorkspaceIdentity.ID, *session.AssigneeIdentityID)
	require.Empty(t, handoffEvents(t, f.db, first.Conversation.ID))

	input = visitorInput(channelID, "")
	second := f.receive(t, &input, "AI 先转人工")
	run = f.queuedRun(t, second.Conversation.ID)
	require.NoError(t, newTestAgentRun(f.db, f.tasks, handoffRuntime("无法确认", nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	claimed, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.identity, second.Conversation.ID)
	require.NoError(t, err)
	require.NotNil(t, claimed.Assignee)
	require.Equal(t, f.identity.WorkspaceIdentity.ID, claimed.Assignee.IdentityID)
	require.Len(t, handoffEvents(t, f.db, second.Conversation.ID), 1)
}

// testManagementReturn 验证停用 AI 员工和从其服务对象中去掉客户时，负责的开放周期连同在途运行一并退回原队列，转人工承接任务按承接结果补发一次对客通知。
func testManagementReturn(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	for _, change := range []string{"停用", "去掉客户"} {
		t.Run(change, func(t *testing.T) {
			agent := f.newAgent(t, change+"客服")
			channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
			// 一个周期有在途运行，另一个周期已由 AI 回答完毕、没有在途运行。
			pendingInput := visitorInput(channelID, "")
			pending := f.receive(t, &pendingInput, "等待回答")
			pendingRun := f.queuedRun(t, pending.Conversation.ID)
			idleInput := visitorInput(channelID, "")
			idle := f.receive(t, &idleInput, "已回答的问题")
			runQueuedAgentRun(t, f.db, newTestAgentRun(f.db, f.tasks, &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
				claimed, err := feed.Claim(ctx, 1)
				return agentruntime.RunResult{Content: "已回答", EndSeq: claimed.EndSeq}, err
			}}, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil), idle.Conversation.ID)

			returner := testServiceSessionReturner(f.db)
			switch change {
			case "停用":
				_, err := agentaction.NewUpdateStatusAction(f.db, testEnqueuer, returner).Execute(ctx, f.identity, agent.ID, domain.IdentityStatusInactive)
				require.NoError(t, err)
			default:
				_, err := agentaction.NewUpdateAgentAction(f.db, testEnqueuer, returner).Execute(ctx, f.identity, agent.ID, agentaction.UpdateInput{
					DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: []domain.ServiceAudience{}, WorkStatus: domain.WorkStatusWorking,
				})
				require.NoError(t, err)
			}
			require.NoError(t, f.db.NewSelect().Model(&pendingRun).WherePK().Scan(ctx))
			require.Equal(t, string(domain.AgentRunStatusCancelled), pendingRun.Status)
			require.NotNil(t, pendingRun.ErrorCode)
			require.Equal(t, string(domain.AgentRunErrorCodeAgentUnavailable), *pendingRun.ErrorCode)
			for _, conversationID := range []string{pending.Conversation.ID, idle.Conversation.ID} {
				events := returnedEvents(t, f.db, conversationID)
				require.Len(t, events, 1)
				require.Equal(t, domain.ServiceSessionReturnAssigneeUnavailable, events[0].Reason)
				require.Equal(t, agent.IdentityID, events[0].FromIdentityID)
				require.Equal(t, domain.ServiceSessionTargetPublicQueue, events[0].Target.Kind)
				session := loadSession(t, f.db, events[0].ServiceSessionID)
				require.Nil(t, session.AssigneeIdentityID)
				require.Nil(t, session.TeamID)
				// 本轮已发出的队列提醒不随承接通知清空；任务重复执行只写一条通知。
				_, err := f.db.NewUpdate().Table("service_sessions").Set("reminded_at = now()").Where("id = ?", session.ID).Exec(ctx)
				require.NoError(t, err)
				for range 2 {
					require.Equal(t, 1, runReturnedHandoffs(t, f.db, testEnqueuer, session.ID), "returned handoff tasks")
				}
				// 对客通知不结束客户等待：有在途输入的周期保留等待起点，已回答的周期保持无等待。
				returned := loadSession(t, f.db, session.ID)
				require.Equal(t, conversationID == idle.Conversation.ID, returned.AwaitingReplySince == nil, "returned session awaiting reply = %v", returned.AwaitingReplySince)
				require.NotNil(t, returned.RemindedAt)
				var notices []servermodels.Message
				require.NoError(t, f.db.NewSelect().Model(&notices).
					Where("msg.conversation_id = ? AND msg.idempotency_key LIKE ?", conversationID, "returned:"+session.ID+":%").
					Where("msg.type = ?", domain.MessageTypeText).Scan(ctx))
				require.Len(t, notices, 1)
				require.Equal(t, handoffQueuedNotice, notices[0].Body)
			}
			lane := servermodels.AgentLane{}
			require.NoError(t, f.db.NewSelect().Model(&lane).Where("al.id = ?", pendingRun.LaneID).Scan(ctx))
			require.Equal(t, lane.DesiredSeq, lane.ProcessedSeq)
		})
	}
}

// testRemoveEmployeeKeepsCustomerSessions 验证从同时服务客户与员工的 AI 员工中只去掉员工时，按服务会话的服务对象退回：服务员工的渠道周期退回队列，服务客户的渠道周期保留。
func testRemoveEmployeeKeepsCustomerSessions(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "双服务对象客服")
	update := func(audiences ...domain.ServiceAudience) {
		t.Helper()
		_, err := agentaction.NewUpdateAgentAction(f.db, testEnqueuer, testServiceSessionReturner(f.db)).Execute(ctx, f.identity, agent.ID, agentaction.UpdateInput{
			DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: audiences, WorkStatus: domain.WorkStatusWorking,
		})
		require.NoError(t, err)
	}
	update(domain.ServiceAudienceCustomer, domain.ServiceAudienceEmployee)
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	customerInput := visitorInput(channelID, "")
	customer := f.receive(t, &customerInput, "客户的问题")
	employeeInput := visitorInput(channelID, "")
	employee := f.receive(t, &employeeInput, "员工的问题")
	// 第二个渠道会话按服务员工处理，与来源无关。
	_, err := f.db.NewUpdate().Table("service_conversations").Set("audience = ?", domain.ServiceAudienceEmployee).
		Where("conversation_id = ?", employee.Conversation.ID).Exec(ctx)
	require.NoError(t, err)

	update(domain.ServiceAudienceCustomer)
	require.Empty(t, returnedEvents(t, f.db, customer.Conversation.ID), "客户周期不应退回")
	customerSession := loadSession(t, f.db, customer.Conversation.ServiceSessionID)
	require.NotNil(t, customerSession.AssigneeIdentityID)
	require.Equal(t, agent.IdentityID, *customerSession.AssigneeIdentityID)
	employeeEvents := returnedEvents(t, f.db, employee.Conversation.ID)
	require.Len(t, employeeEvents, 1, "员工周期应退回")
	require.Equal(t, agent.IdentityID, employeeEvents[0].FromIdentityID)
}

// testInboundRoutingVersusEligibility 验证新访客入站与停用、关闭接待并发交错后，没有开放周期留在失去接待资格的 AI 员工名下。
func testInboundRoutingVersusEligibility(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	for _, change := range []string{"停用", "去掉客户"} {
		t.Run(change, func(t *testing.T) {
			agent := f.newAgent(t, change+"并发客服")
			channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
			var wait sync.WaitGroup
			errs := make(chan error, 9)
			for index := range 8 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					input := visitorInput(channelID, "并发入站")
					_, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(f.tasks), testEnqueuer, servertest.DisabledMail{}).Execute(ctx, input)
					errs <- err
				}()
				if index == 3 {
					wait.Add(1)
					go func() {
						defer wait.Done()
						var err error
						if change == "停用" {
							_, err = agentaction.NewUpdateStatusAction(f.db, testEnqueuer, testServiceSessionReturner(f.db)).Execute(ctx, f.identity, agent.ID, domain.IdentityStatusInactive)
						} else {
							_, err = agentaction.NewUpdateAgentAction(f.db, testEnqueuer, testServiceSessionReturner(f.db)).Execute(ctx, f.identity, agent.ID, agentaction.UpdateInput{
								DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: []domain.ServiceAudience{}, WorkStatus: domain.WorkStatusWorking,
							})
						}
						errs <- err
					}()
				}
			}
			wait.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			stranded, err := f.db.NewSelect().Model((*servermodels.ServiceSession)(nil)).
				Where("ss.assignee_identity_id = ? AND ss.status = ?", agent.IdentityID, domain.ServiceSessionStatusOpen).Count(ctx)
			require.NoError(t, err)
			require.Zero(t, stranded, "open sessions left on unavailable agent")
		})
	}
}

// testTelegramModelHandoff 验证 Telegram 会话主动转人工只产生一次对客投递，重复执行不追加。
func testTelegramModelHandoff(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	fixture := newAgentTelegramFixture(t, f.db, f.identity, f.providerID, f.modelID)
	executor := newTestAgentRun(f.db, fixture.tasks, handoffRuntime("需要人工确认", nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
	for range 2 {
		require.NoError(t, executor.Execute(ctx, agentrunaction.RunInput{RunID: fixture.run.ID}))
	}
	fixture.reload(t)
	var deliveries []servermodels.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", fixture.run.ConversationID).Scan(ctx))
	require.NotNil(t, fixture.run.ResponseMessageID)
	require.Len(t, deliveries, 1)
	require.Equal(t, *fixture.run.ResponseMessageID, deliveries[0].MessageID)
	require.Len(t, handoffEvents(t, f.db, fixture.run.ConversationID), 1)
}

// testChannelEditVersusDeactivation 验证停用 AI 员工持有身份锁时，以其为路由目标的渠道编辑先等待身份锁再锁渠道，两者不形成循环等待。
func testChannelEditVersusDeactivation(t *testing.T, f handoffFixture) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	agent := f.newAgent(t, "渠道编辑并发客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	// 渠道编辑由另一名成员发起，两个操作人各自持有自己的账号锁。
	email := servertest.UniqueEmail("channel-editor")
	_, err := newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "渠道编辑成员", Email: email, Password: "password123", RoleID: f.identity.User.RoleID,
	})
	require.NoError(t, err)
	disableAutoAssignment(t, f.db, f.identity.Workspace.ID)
	editor := servertest.LoginMember(t, f.db, f.identity.Workspace.ID, email, "password123")
	gated := bun.NewDB(f.db.DB, f.db.Dialect())
	gated.AddQueryHook(chatQueryHook{})
	// 停用事务取得 AI 员工身份排他锁后暂停。
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return strings.Contains(event.Query, "workspace_identities") && strings.Contains(event.Query, "FOR UPDATE OF oi")
	})
	deactivated, edited := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := agentaction.NewUpdateStatusAction(gated, testEnqueuer, testServiceSessionReturner(f.db)).Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), f.identity, agent.ID, domain.IdentityStatusInactive)
		deactivated <- err
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		_, err := channelaction.NewUpdateMessageChannelAction(f.db).ExecuteReception(ctx, editor.Identity, channelID, channelaction.MessageChannelReceptionInput{
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		edited <- err
	}()
	waitChatDatabaseLock(t, ctx, f.db, "workspace_identities", agent.IdentityID)
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, deactivated), "deactivate agent")
	// 渠道编辑在停用提交后校验目标，AI 员工已不可用时按校验失败返回。
	var validation *channelaction.ValidationError
	if err := waitChatResult(t, ctx, edited); err != nil {
		require.ErrorAs(t, err, &validation, "edit channel")
	}
	channel := servermodels.Channel{}
	require.NoError(t, f.db.NewSelect().Model(&channel).Where("c.id = ?", channelID).Scan(ctx))
	require.Equal(t, string(domain.ChannelRoutingTargetTypePublicQueue), channel.InitialRoutingTargetType)
}

// testTelegramInboundReturnVersusRunFailure 验证已持有会话锁的入站事务发现负责人失效时，退回不锁渠道身份，与先锁渠道身份的运行收尾不形成循环等待；对客通知由转人工承接任务投递一次。
func testTelegramInboundReturnVersusRunFailure(t *testing.T, f handoffFixture) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := newAgentTelegramFixture(t, f.db, f.identity, f.providerID, f.modelID)
	_, err := f.db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).
		Where("identity_id = ?", fixture.run.AgentIdentityID).Exec(ctx)
	require.NoError(t, err)
	var messageID string
	require.NoError(t, f.db.NewSelect().Model((*servermodels.Message)(nil)).Column("id").
		Where("msg.conversation_id = ? AND msg.type = ?", fixture.run.ConversationID, domain.MessageTypeText).
		OrderExpr("msg.message_seq DESC").Limit(1).Scan(ctx, &messageID))
	gated := bun.NewDB(f.db.DB, f.db.Dialect())
	gated.AddQueryHook(chatQueryHook{})
	// 运行失败收尾取得渠道身份锁后、申请会话锁前暂停。
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return strings.Contains(event.Query, "channel_identities") && strings.Contains(event.Query, "FOR UPDATE")
	})
	failed, scheduled := make(chan error, 1), make(chan error, 1)
	go func() {
		executor := newTestAgentRun(gated, fixture.tasks, nil, testModelInvoker(gated), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil)
		failed <- executor.FinalizeFailure(context.WithValue(ctx, chatQueryGateKey{}, gate), agentrunaction.RunInput{RunID: fixture.run.ID}, errors.New("运行失败"))
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		scheduled <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			locked, err := chatstate.LockServiceSession(ctx, tx, fixture.run.WorkspaceID, fixture.run.ConversationID)
			if err != nil {
				return err
			}
			_, err = agentrunaction.NewScheduler(fixture.tasks).ScheduleCustomerAuto(ctx, tx, fixture.run.WorkspaceID, fixture.run.ConversationID, locked.Session.ID, messageID)
			return err
		})
	}()
	// 入站事务在运行收尾暂停期间独立完成退回。
	select {
	case err := <-scheduled:
		require.NoError(t, err, "inbound return")
	case <-time.After(5 * time.Second):
		gate.open()
		t.Fatal("inbound return waited for the channel identity lock held by run failure")
	}
	gate.open()
	require.NoError(t, waitChatResult(t, ctx, failed), "finalize run failure")
	fixture.reload(t)
	require.Equal(t, 1, runReturnedHandoffs(t, f.db, fixture.tasks, fixture.run.ScopeID), "returned handoff tasks")
	var deliveries []servermodels.ChannelMessageDelivery
	require.NoError(t, f.db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", fixture.run.ConversationID).Scan(ctx))
	events := returnedEvents(t, f.db, fixture.run.ConversationID)
	require.Equal(t, string(domain.AgentRunStatusCancelled), fixture.run.Status)
	require.Len(t, events, 1)
	require.Equal(t, domain.ServiceSessionReturnAssigneeUnavailable, events[0].Reason)
	require.Len(t, deliveries, 1)
}

// testServiceSessionOperationEvents 验证领取、接管、转交、关闭与重开各写一条仅成员可见的周期事件，且不改变会话摘要与活动时间。
func testServiceSessionOperationEvents(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "周期事件客服")
	channel, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "周期事件验证", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	email := servertest.UniqueEmail("session-events")
	_, err = newTestMemberCreator(f.db, testEnqueuer).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "接管成员", Email: email, Password: "password123", RoleID: f.identity.User.RoleID,
	})
	require.NoError(t, err)
	disableAutoAssignment(t, f.db, f.identity.Workspace.ID)
	other := servertest.LoginMember(t, f.db, f.identity.Workspace.ID, email, "password123")
	input := visitorInput(channel.ID, "")
	first := f.receive(t, &input, "有人吗")
	conversationID := first.Conversation.ID
	coordinator := testServiceSessionReturner(f.db)
	scheduler := agentrunaction.NewScheduler(f.tasks)
	owner, member := f.identity.WorkspaceIdentity.ID, other.Identity.WorkspaceIdentity.ID
	// 成员回复无人负责的周期即领取。
	reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "在的",
	})
	require.NoError(t, err)
	summary := servermodels.Conversation{}
	require.NoError(t, f.db.NewSelect().Model(&summary).Where("cv.id = ?", conversationID).Scan(ctx))
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, other.Identity, conversationID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, testEnqueuer).Execute(ctx, other.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	})
	require.NoError(t, err)
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.identity, conversationID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.identity, conversationID)
	require.NoError(t, err)
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.identity, conversationID)
	require.NoError(t, err)

	var messages []servermodels.Message
	require.NoError(t, f.db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.type = ?", conversationID, domain.MessageTypeSystem).
		OrderExpr("msg.message_seq").Scan(ctx))
	want := []struct {
		eventType domain.ConversationSystemEventType
		actor     string
		from      *string
		target    *string
	}{
		{domain.ConversationSystemEventServiceSessionClaimed, owner, nil, nil},
		{domain.ConversationSystemEventServiceSessionTakenOver, member, &owner, nil},
		{domain.ConversationSystemEventServiceSessionTransferred, member, &member, &agent.IdentityID},
		{domain.ConversationSystemEventServiceSessionTakenOver, owner, &agent.IdentityID, nil},
		{domain.ConversationSystemEventServiceSessionClosed, owner, nil, nil},
		{domain.ConversationSystemEventServiceSessionReopened, owner, nil, nil},
	}
	require.Len(t, messages, len(want), "service session events")
	for index, message := range messages {
		event := domain.ServiceSessionOperatedEvent{}
		require.NoError(t, json.Unmarshal(message.SystemEventPayload, &event))
		expected := want[index]
		require.NotNil(t, message.SystemEventType, "event %d", index)
		require.Equal(t, string(expected.eventType), *message.SystemEventType, "event %d", index)
		require.Equal(t, string(domain.MessageVisibilityInternal), message.Visibility, "event %d", index)
		require.NotNil(t, message.ServiceSessionID, "event %d", index)
		require.Equal(t, expected.actor, event.ActorIdentityID, "event %d", index)
		require.NotEmpty(t, event.ActorDisplayName, "event %d", index)
		if expected.from == nil {
			require.Nil(t, event.FromIdentityID, "event %d", index)
		} else {
			require.NotNil(t, event.FromIdentityID, "event %d", index)
			require.Equal(t, *expected.from, *event.FromIdentityID, "event %d", index)
			require.NotNil(t, event.FromDisplayName, "event %d", index)
		}
		if expected.target == nil {
			require.Nil(t, event.Target, "event %d", index)
		} else {
			require.NotNil(t, event.Target, "event %d", index)
			require.NotNil(t, event.Target.IdentityID, "event %d", index)
			require.Equal(t, *expected.target, *event.Target.IdentityID, "event %d", index)
		}
		require.Equal(t, expected.eventType == domain.ConversationSystemEventServiceSessionClosed, event.CloseReason != nil, "event %d", index)
		if event.CloseReason != nil {
			require.Equal(t, domain.ServiceSessionCloseManual, *event.CloseReason, "event %d", index)
		}
	}
	// 重新打开后清除结束方式。
	reopened := servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(&reopened).Where("ss.conversation_id = ?", conversationID).Scan(ctx))
	require.Equal(t, string(domain.ServiceSessionStatusOpen), reopened.Status)
	require.Nil(t, reopened.CloseReason)
	// 周期事件不改变会话摘要与活动时间。
	after := servermodels.Conversation{}
	require.NoError(t, f.db.NewSelect().Model(&after).Where("cv.id = ?", conversationID).Scan(ctx))
	require.NotNil(t, after.LastMessageID)
	require.Equal(t, reply.ID, *after.LastMessageID)
	require.NotNil(t, after.LastActivityAt)
	require.NotNil(t, summary.LastActivityAt)
	require.True(t, after.LastActivityAt.Equal(*summary.LastActivityAt), "conversation summary before = %v, after = %v", *summary.LastActivityAt, *after.LastActivityAt)
	// 成员历史按群聊事件的 actor 结构返回操作人。
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	require.NoError(t, err)
	for _, message := range history.Messages {
		if message.SystemEvent != nil {
			require.NotNil(t, message.SystemEvent.ActorIdentityID, "history event = %+v", message.SystemEvent)
			require.NotNil(t, message.SystemEvent.ServiceSessionID, "history event = %+v", message.SystemEvent)
		}
	}
}
