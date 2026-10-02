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

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	servicecategoryaction "github.com/runforyou-ai/luway/internal/actions/servicecategory"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// testServiceSessionReturner 创建管理操作退回服务周期所用的运行协调器。
func testServiceSessionReturner(db *bun.DB) *agentrunaction.ExecuteAction {
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(deliveryaction.SendActionName, func(context.Context, deliveryaction.Input) error { return nil }); err != nil {
		panic(err)
	}
	return agentrunaction.NewExecuteAction(db, tasks, nil, testAttachmentReader(db), nil, nil)
}

// testUserStatusAction 创建测试用的成员账号状态修改操作。
func testUserStatusAction(db *bun.DB) *useraction.UpdateStatusAction {
	coordinator := testServiceSessionReturner(db)
	return useraction.NewUpdateStatusAction(db, coordinator, groupchataction.NewPersonalAgentRetirer(coordinator))
}

// handoffFixture 保存转人工集成测试共用的企业身份、任务运行时与客服角色。
type handoffFixture struct {
	db         *bun.DB
	identity   *servermodels.Identity
	tasks      *servertask.Runtime
	providerID string
	modelID    string
}

// newAgent 创建一个开启接待客户的 AI 员工。
func (f handoffFixture) newAgent(t *testing.T, name string) *agentaction.Agent {
	t.Helper()
	created, err := agentaction.NewCreateAgentAction(f.db).Execute(context.Background(), f.identity, agentaction.CreateInput{
		DisplayName: name, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer},
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: f.providerID, ModelIdentifier: f.modelID}},
	})
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
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
	result, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(f.tasks), newTestTasks(f.db), nil).Execute(context.Background(), *input)
	if err != nil {
		t.Fatal(err)
	}
	input.ConversationID = &result.Conversation.ID
	return result
}

// queuedRun 读取会话中排队的运行。
func (f handoffFixture) queuedRun(t *testing.T, conversationID string) servermodels.AgentRun {
	t.Helper()
	run := servermodels.AgentRun{}
	if err := f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return run
}

// handoffRuntime 认领首批输入后按需执行插入动作，再返回模型给出的转人工决定。
func handoffRuntime(reasonText string, during func()) *testAgentRuntime {
	return categoryHandoffRuntime(reasonText, "", during)
}

// categoryHandoffRuntime 认领首批输入后按需执行插入动作，再返回带咨询分类编号的转人工决定。
func categoryHandoffRuntime(reasonText, categoryID string, during func()) *testAgentRuntime {
	return &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		triggers, err := feed.Peek(ctx, 0)
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
		return agentruntime.RunResult{EndSeq: claimed.EndSeq, Decision: agentruntime.TerminalDecision{
			Kind: domain.AgentRunOutcomeHandoff, Reason: domain.AgentHandoffReasonKnowledgeGap, ReasonText: reasonText, CategoryID: categoryID,
		}}, nil
	}}
}

// handoffEvents 读取会话中的转人工系统事件。
func handoffEvents(t *testing.T, db *bun.DB, conversationID string) []domain.ServiceSessionHandedOffEvent {
	t.Helper()
	var messages []servermodels.Message
	if err := db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionHandedOff).
		OrderExpr("msg.message_seq").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := make([]domain.ServiceSessionHandedOffEvent, 0, len(messages))
	for _, message := range messages {
		if message.Visibility != string(domain.MessageVisibilityInternal) || message.SenderParticipantID != nil {
			t.Fatalf("handoff event message = %+v", message)
		}
		event := domain.ServiceSessionHandedOffEvent{}
		if err := json.Unmarshal(message.SystemEventPayload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

// returnedEvents 读取会话中的周期退回队列事件。
func returnedEvents(t *testing.T, db *bun.DB, conversationID string) []domain.ServiceSessionReturnedEvent {
	t.Helper()
	var messages []servermodels.Message
	if err := db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionReturned).
		OrderExpr("msg.message_seq").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := make([]domain.ServiceSessionReturnedEvent, 0, len(messages))
	for _, message := range messages {
		if message.Visibility != string(domain.MessageVisibilityInternal) || message.SenderParticipantID != nil {
			t.Fatalf("returned event message = %+v", message)
		}
		event := domain.ServiceSessionReturnedEvent{}
		if err := json.Unmarshal(message.SystemEventPayload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

// loadSession 读取客服周期的当前状态。
func loadSession(t *testing.T, db *bun.DB, sessionID string) servermodels.ServiceSession {
	t.Helper()
	session := servermodels.ServiceSession{}
	if err := db.NewSelect().Model(&session).Where("ss.id = ?", sessionID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

// handoffNotice 读取按幂等键写入的对客通知正文。
func handoffNotice(t *testing.T, db *bun.DB, key string) string {
	t.Helper()
	notice := servermodels.Message{}
	if err := db.NewSelect().Model(&notice).Where("msg.idempotency_key = ? AND msg.type = ?", key, domain.MessageTypeText).Scan(context.Background()); err != nil {
		t.Fatalf("load notice %s: %v", key, err)
	}
	return notice.Body
}

// runReturnedHandoffs 执行指定客服周期已投递的转人工承接任务，返回执行的任务数。
func runReturnedHandoffs(t *testing.T, db *bun.DB, serviceSessionID string) int {
	t.Helper()
	ctx := context.Background()
	var runs []servermodels.TaskRun
	if err := db.NewSelect().Model(&runs).
		Where("tr.action_name = ? AND tr.payload->>'serviceSessionId' = ?", agentrunaction.ReturnedHandoffActionName, serviceSessionID).
		OrderExpr("tr.created_at").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	executor := testServiceSessionReturner(db)
	for _, run := range runs {
		input := agentrunaction.ReturnedHandoffInput{}
		if err := json.Unmarshal(run.Payload, &input); err != nil {
			t.Fatal(err)
		}
		if err := executor.HandOffReturnedSession(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	return len(runs)
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
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Organization.ID)
	t.Run("主动转人工后转回同一 AI", func(t *testing.T) { testModelHandoffRoundTrip(t, f) })
	t.Run("失败路由去向", func(t *testing.T) { testHandoffTargets(t, f) })
	t.Run("咨询分类路由", func(t *testing.T) { testHandoffCategoryRouting(t, f) })
	t.Run("转人工自动分配", func(t *testing.T) { testHandoffAutoAssignment(t, f) })
	t.Run("工作时间与转人工话术", func(t *testing.T) { testBusinessHoursHandoffNotice(t, f) })
	t.Run("人工接管与交接先后", func(t *testing.T) { testHandoffCommitOrder(t, f) })
	t.Run("停用与关闭接待退回队列", func(t *testing.T) { testManagementReturn(t, f) })
	t.Run("入站路由与资格变更交错", func(t *testing.T) { testInboundRoutingVersusEligibility(t, f) })
	t.Run("入站发现负责人失效", func(t *testing.T) { testInboundUnavailableAssignee(t, f) })
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
	executor := agentrunaction.NewExecuteAction(f.db, f.tasks, runtime, testAttachmentReader(f.db), nil, nil)
	for range 2 {
		if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if run.Status != string(domain.AgentRunStatusSucceeded) || run.Outcome == nil || *run.Outcome != string(domain.AgentRunOutcomeHandoff) ||
		run.OutcomeReason == nil || *run.OutcomeReason != string(domain.AgentHandoffReasonKnowledgeGap) ||
		run.InputEndSeq == nil || *run.InputEndSeq != 1 || run.HandoffSettledSeq == nil || *run.HandoffSettledSeq != 2 || run.ResponseMessageID == nil {
		t.Fatalf("handoff run = %+v", run)
	}
	lane := servermodels.AgentLane{}
	if err := f.db.NewSelect().Model(&lane).Where("al.id = ?", run.LaneID).Scan(ctx); err != nil || lane.DesiredSeq != 2 || lane.ProcessedSeq != 2 {
		t.Fatalf("lane = %+v, error = %v", lane, err)
	}
	session := loadSession(t, f.db, run.ScopeID)
	if session.AssigneeIdentityID != nil || session.TeamID != nil || session.Status != string(domain.ServiceSessionStatusOpen) {
		t.Fatalf("session = %+v", session)
	}
	events := handoffEvents(t, f.db, first.Conversation.ID)
	if len(events) != 1 || events[0].Target.Kind != domain.ServiceSessionTargetPublicQueue || events[0].ReasonText != "客户要求退款" ||
		events[0].FromDisplayName != "转人工客服" || events[0].AgentRunID == nil || *events[0].AgentRunID != run.ID {
		t.Fatalf("events = %+v", events)
	}
	if active, err := f.db.NewSelect().Model((*servermodels.AgentRun)(nil)).Where("agr.conversation_id = ? AND agr.status = ?", first.Conversation.ID, domain.AgentRunStatusQueued).Exists(ctx); err != nil || active {
		t.Fatalf("queued run after handoff = %t, error = %v", active, err)
	}
	// 访客只看到两条消息和对客通知，内部原因不外露。
	visible, err := customerchataction.NewListWebsiteMessagesQuery(f.db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channelID, ExternalID: input.ExternalID, ConversationID: first.Conversation.ID})
	if err != nil || len(visible.Messages) != 3 || visible.Messages[2].ID != *run.ResponseMessageID || visible.Messages[2].Body != handoffQueuedNotice {
		t.Fatalf("visitor messages = %+v, error = %v", visible, err)
	}
	for _, message := range visible.Messages {
		if strings.Contains(message.Body, "客户要求退款") {
			t.Fatalf("internal reason leaked: %+v", message)
		}
	}
	// 成员领取后转回同一 AI，新消息只触发新输入。
	coordinator := testServiceSessionReturner(f.db)
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.identity, first.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(f.tasks), newTestTasks(f.db)).Execute(ctx, f.identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: first.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	}); err != nil {
		t.Fatal(err)
	}
	f.receive(t, &input, "新的问题")
	next := f.queuedRun(t, first.Conversation.ID)
	reply := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
		triggers, err := feed.Peek(ctx, 0)
		if err != nil || len(triggers) != 1 || triggers[0].Seq != 3 {
			t.Fatalf("triggers after handoff = %+v, error = %v", triggers, err)
		}
		claimed, err := feed.Claim(ctx, triggers[0].Seq)
		return agentruntime.RunResult{Content: "新问题的回答", EndSeq: claimed.EndSeq}, err
	}}
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, reply, testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: next.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewSelect().Model(&next).WherePK().Scan(ctx); err != nil || next.Status != string(domain.AgentRunStatusSucceeded) ||
		next.InputStartSeq != 3 || next.Outcome == nil || *next.Outcome != string(domain.AgentRunOutcomeReply) {
		t.Fatalf("next run = %+v, error = %v", next, err)
	}
}

// testHandoffTargets 验证转人工按渠道失败去向进入团队或公共队列，失败团队无效时进入公共队列。
func testHandoffTargets(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "去向验证客服")
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.identity, teamaction.Input{Name: "售后组 " + uuid.NewV7().String()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	human, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "人工客服", Email: "handoff-" + uuid.NewV7().String()[:8] + "@handoff.test", Password: "password123", RoleID: f.identity.User.RoleID,
	})
	if err != nil {
		t.Fatal(err)
	}
	disableAutoAssignment(t, f.db, f.identity.Organization.ID)
	// 团队队列须有开启接待的真人成员才可用。
	if _, err := teamaction.NewAddMembersAction(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: human.IdentityID},
	}); err != nil {
		t.Fatal(err)
	}
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
				if _, err := f.db.NewUpdate().Model((*servermodels.Channel)(nil)).Set("fallback_routing_target_id = ?", uuid.NewV7().String()).Where("id = ?", channelID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			input := visitorInput(channelID, "")
			first := f.receive(t, &input, "需要人工")
			run := f.queuedRun(t, first.Conversation.ID)
			if err := agentrunaction.NewExecuteAction(f.db, f.tasks, handoffRuntime("无法确认", nil), testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
				t.Fatal(err)
			}
			session := loadSession(t, f.db, run.ScopeID)
			if (scenario.wantTeam == nil) != (session.TeamID == nil) || (scenario.wantTeam != nil && *session.TeamID != *scenario.wantTeam) ||
				session.AssigneeIdentityID != nil {
				t.Fatalf("session = %+v", session)
			}
			events := handoffEvents(t, f.db, first.Conversation.ID)
			if len(events) != 1 || events[0].Target.Kind != scenario.wantKind ||
				(scenario.wantKind == domain.ServiceSessionTargetTeam && (events[0].Target.TeamName == nil || *events[0].Target.TeamName != team.Name)) {
				t.Fatalf("events = %+v", events)
			}
			if notice := handoffNotice(t, f.db, "agent:"+run.ID); notice != handoffQueuedNotice {
				t.Fatalf("notice = %q, want %q", notice, handoffQueuedNotice)
			}
		})
	}
}

// testHandoffCategoryRouting 验证 AI 选择的咨询分类写入周期与事件，分类团队可用时优先于渠道失败路由；未关联团队或团队无人接待时记下分类并按失败路由，已归档或未选择时不记分类。
func testHandoffCategoryRouting(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "分类路由验证客服")
	suffix := uuid.NewV7().String()[:8]
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.identity, teamaction.Input{Name: "退款组 " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	human, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "退款客服", Email: "category-" + suffix + "@handoff.test", Password: "password123", RoleID: f.identity.User.RoleID,
	})
	if err != nil {
		t.Fatal(err)
	}
	disableAutoAssignment(t, f.db, f.identity.Organization.ID)
	if _, err := teamaction.NewAddMembersAction(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: human.IdentityID},
	}); err != nil {
		t.Fatal(err)
	}
	refund, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "退款 " + suffix, Description: "退款、退货", TeamID: &team.ID})
	if err != nil {
		t.Fatal(err)
	}
	shipping, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "物流 " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "旧分类 " + suffix, TeamID: &team.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := servicecategoryaction.NewArchiveAction(f.db).Execute(ctx, f.identity, archived.ID); err != nil {
		t.Fatal(err)
	}
	emptyTeam, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.identity, teamaction.Input{Name: "空团队 " + suffix})
	if err != nil {
		t.Fatal(err)
	}
	unstaffed, err := servicecategoryaction.NewCreateAction(f.db).Execute(ctx, f.identity, servicecategoryaction.Input{Name: "投诉 " + suffix, TeamID: &emptyTeam.ID})
	if err != nil {
		t.Fatal(err)
	}
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
			if err := agentrunaction.NewExecuteAction(f.db, f.tasks, categoryHandoffRuntime("客户要求退款", categoryID, nil), testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
				t.Fatal(err)
			}
			session := loadSession(t, f.db, run.ScopeID)
			if (scenario.wantTeam == nil) != (session.TeamID == nil) || (scenario.wantTeam != nil && *session.TeamID != *scenario.wantTeam) ||
				(scenario.wantCategory == nil) != (session.CategoryID == nil) || (scenario.wantCategory != nil && *session.CategoryID != *scenario.wantCategory) {
				t.Fatalf("session = %+v", session)
			}
			events := handoffEvents(t, f.db, received.Conversation.ID)
			if len(events) != 1 || events[0].Reason != domain.AgentHandoffReasonKnowledgeGap ||
				(scenario.wantCategory == nil) != (events[0].CategoryName == nil) || (scenario.wantCategory != nil && *events[0].CategoryName != scenario.category.Name) {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

// testHandoffAutoAssignment 验证转人工进入队列时在交接事务内分配给可接待的真人成员，事件去向为实际承接成员并记录客户等待起点。
func testHandoffAutoAssignment(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	t.Cleanup(func() { disableAutoAssignment(t, f.db, f.identity.Organization.ID) })
	agent := f.newAgent(t, "自动分配验证客服")
	human, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 1, DisplayName: "承接客服", Email: "assign-" + uuid.NewV7().String()[:8] + "@handoff.test", Password: "password123", RoleID: f.identity.User.RoleID,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	handOff := func() (servermodels.ServiceSession, []domain.ServiceSessionHandedOffEvent, string) {
		t.Helper()
		input := visitorInput(channelID, "")
		received := f.receive(t, &input, "需要人工")
		run := f.queuedRun(t, received.Conversation.ID)
		if err := agentrunaction.NewExecuteAction(f.db, f.tasks, handoffRuntime("无法确认", nil), testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		return loadSession(t, f.db, run.ScopeID), handoffEvents(t, f.db, received.Conversation.ID), handoffNotice(t, f.db, "agent:"+run.ID)
	}
	session, events, notice := handOff()
	if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != human.IdentityID || session.TeamID != nil ||
		session.AssigneeAssignedAt == nil || session.AwaitingReplySince == nil {
		t.Fatalf("assigned handoff session = %+v", session)
	}
	if len(events) != 1 || events[0].Target.Kind != domain.ServiceSessionTargetMember || events[0].Target.IdentityID == nil || *events[0].Target.IdentityID != human.IdentityID {
		t.Fatalf("assigned handoff events = %+v", events)
	}
	if notice != "已为您转接人工客服承接客服，请稍候。" {
		t.Fatalf("assigned handoff notice = %q", notice)
	}
	// 承接客服满员后转人工留在公共队列。
	session, events, notice = handOff()
	if session.AssigneeIdentityID != nil || session.AssigneeAssignedAt != nil || session.AwaitingReplySince == nil {
		t.Fatalf("queued handoff session = %+v", session)
	}
	if len(events) != 1 || events[0].Target.Kind != domain.ServiceSessionTargetPublicQueue {
		t.Fatalf("queued handoff events = %+v", events)
	}
	if notice != handoffQueuedNotice {
		t.Fatalf("queued handoff notice = %q", notice)
	}
}

// testBusinessHoursHandoffNotice 验证工作时间的保存与校验，以及无人可分配时按工作时间选择排队、非工作时间和无下次处理时间的话术。
func testBusinessHoursHandoffNotice(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(ctx, `UPDATE customer_service_settings SET business_hours_enabled = DEFAULT, business_hours_time_zone = DEFAULT,
			business_hours_weekly = DEFAULT, business_hours_overrides = DEFAULT WHERE organization_id = ?`, f.identity.Organization.ID); err != nil {
			t.Error(err)
		}
	})
	update := customerserviceaction.NewUpdateBusinessHoursAction(f.db)
	// 新工作区的设置行取列默认值。
	hours, err := customerserviceaction.NewGetBusinessHoursQuery(f.db).Execute(ctx, f.identity)
	if err != nil || hours.Enabled || hours.TimeZone != "Asia/Shanghai" || len(hours.Weekly[0]) != 1 || len(hours.Weekly[6]) != 0 || hours.Overrides == nil {
		t.Fatalf("default business hours = %+v, error = %v", hours, err)
	}
	// 时区、每周时段和日期覆盖分别校验。
	invalid := hours
	invalid.TimeZone = "Mars/Base"
	invalid.Weekly[0] = []domain.BusinessHoursPeriod{{Start: "09:00", End: "13:00"}, {Start: "12:00", End: "18:00"}}
	invalid.Overrides = []domain.BusinessHoursOverride{{Date: "2026-10-01"}, {Date: "2026-10-01"}}
	var validation *customerserviceaction.ValidationError
	if _, err := update.Execute(ctx, f.identity, invalid); !errors.As(err, &validation) || len(validation.Fields) != 3 ||
		validation.Fields["timeZone"] != customerserviceaction.ValidationTimeZoneInvalid ||
		validation.Fields["weekly"] != customerserviceaction.ValidationWeeklyInvalid ||
		validation.Fields["overrides"] != customerserviceaction.ValidationOverrideInvalid {
		t.Fatalf("validation error = %v", err)
	}

	agent := f.newAgent(t, "工作时间验证客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
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
			if _, err := update.Execute(ctx, f.identity, hours); err != nil {
				t.Fatal(err)
			}
			// 保存后的日期覆盖时段按开始时间排序。
			saved, err := customerserviceaction.LoadBusinessHours(ctx, f.db, f.identity.Organization.ID)
			if err != nil || !saved.Enabled || len(saved.Overrides) != len(scenario.overrides) ||
				(len(saved.Overrides) > 0 && saved.Overrides[0].Periods[0].Start != "09:30") {
				t.Fatalf("saved business hours = %+v, error = %v", saved, err)
			}
			input := visitorInput(channelID, "")
			received := f.receive(t, &input, "需要人工")
			run := f.queuedRun(t, received.Conversation.ID)
			if err := agentrunaction.NewExecuteAction(f.db, f.tasks, handoffRuntime("无法确认", nil), testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
				t.Fatal(err)
			}
			if notice := handoffNotice(t, f.db, "agent:"+run.ID); notice != scenario.want {
				t.Fatalf("notice = %q, want %q", notice, scenario.want)
			}
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
		if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.identity, first.Conversation.ID); err != nil {
			t.Fatal(err)
		}
	})
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, takeover, testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil || run.Status != string(domain.AgentRunStatusCancelled) || run.Outcome != nil || run.ResponseMessageID != nil {
		t.Fatalf("suppressed run = %+v, error = %v", run, err)
	}
	session := loadSession(t, f.db, run.ScopeID)
	if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != f.identity.OrganizationIdentity.ID || len(handoffEvents(t, f.db, first.Conversation.ID)) != 0 {
		t.Fatalf("session after takeover = %+v", session)
	}

	input = visitorInput(channelID, "")
	second := f.receive(t, &input, "AI 先转人工")
	run = f.queuedRun(t, second.Conversation.ID)
	if err := agentrunaction.NewExecuteAction(f.db, f.tasks, handoffRuntime("无法确认", nil), testAttachmentReader(f.db), nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	claimed, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.identity, second.Conversation.ID)
	if err != nil || claimed.Assignee == nil || claimed.Assignee.IdentityID != f.identity.OrganizationIdentity.ID || len(handoffEvents(t, f.db, second.Conversation.ID)) != 1 {
		t.Fatalf("claim after handoff = %+v, error = %v", claimed, err)
	}
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
			runQueuedAgentRun(t, f.db, agentrunaction.NewExecuteAction(f.db, f.tasks, &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
				claimed, err := feed.Claim(ctx, 1)
				return agentruntime.RunResult{Content: "已回答", EndSeq: claimed.EndSeq}, err
			}}, testAttachmentReader(f.db), nil, nil), idle.Conversation.ID)

			returner := testServiceSessionReturner(f.db)
			switch change {
			case "停用":
				if _, err := agentaction.NewUpdateStatusAction(f.db, returner).Execute(ctx, f.identity, agent.ID, domain.IdentityStatusInactive); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := agentaction.NewUpdateAgentAction(f.db, returner).Execute(ctx, f.identity, agent.ID, agentaction.UpdateInput{
					DisplayName: agent.DisplayName, TeamIDs: []string{}, ServiceAudiences: []domain.ServiceAudience{}, WorkStatus: domain.WorkStatusWorking,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.db.NewSelect().Model(&pendingRun).WherePK().Scan(ctx); err != nil || pendingRun.Status != string(domain.AgentRunStatusCancelled) ||
				pendingRun.ErrorCode == nil || *pendingRun.ErrorCode != string(domain.AgentRunErrorCodeAgentUnavailable) {
				t.Fatalf("pending run = %+v, error = %v", pendingRun, err)
			}
			for _, conversationID := range []string{pending.Conversation.ID, idle.Conversation.ID} {
				events := returnedEvents(t, f.db, conversationID)
				if len(events) != 1 || events[0].Reason != domain.ServiceSessionReturnAssigneeUnavailable ||
					events[0].FromIdentityID != agent.IdentityID || events[0].Target.Kind != domain.ServiceSessionTargetPublicQueue {
					t.Fatalf("events = %+v", events)
				}
				session := loadSession(t, f.db, events[0].ServiceSessionID)
				if session.AssigneeIdentityID != nil || session.TeamID != nil {
					t.Fatalf("session = %+v", session)
				}
				// 本轮已发出的队列提醒不随承接通知清空；任务重复执行只写一条通知。
				if _, err := f.db.NewUpdate().Table("service_sessions").Set("reminded_at = now()").Where("id = ?", session.ID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if tasks := runReturnedHandoffs(t, f.db, session.ID); tasks != 1 {
						t.Fatalf("returned handoff tasks = %d", tasks)
					}
				}
				// 对客通知不结束客户等待：有在途输入的周期保留等待起点，已回答的周期保持无等待。
				if returned := loadSession(t, f.db, session.ID); (returned.AwaitingReplySince == nil) != (conversationID == idle.Conversation.ID) || returned.RemindedAt == nil {
					t.Fatalf("returned session awaiting reply = %v, reminded = %v", returned.AwaitingReplySince, returned.RemindedAt)
				}
				var notices []servermodels.Message
				if err := f.db.NewSelect().Model(&notices).
					Where("msg.conversation_id = ? AND msg.idempotency_key LIKE ?", conversationID, "returned:"+session.ID+":%").
					Where("msg.type = ?", domain.MessageTypeText).Scan(ctx); err != nil || len(notices) != 1 || notices[0].Body != handoffQueuedNotice {
					t.Fatalf("return notices = %+v, error = %v", notices, err)
				}
			}
			lane := servermodels.AgentLane{}
			if err := f.db.NewSelect().Model(&lane).Where("al.id = ?", pendingRun.LaneID).Scan(ctx); err != nil || lane.ProcessedSeq != lane.DesiredSeq {
				t.Fatalf("lane = %+v, error = %v", lane, err)
			}
		})
	}
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
					_, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(f.tasks), newTestTasks(f.db), nil).Execute(ctx, input)
					errs <- err
				}()
				if index == 3 {
					wait.Add(1)
					go func() {
						defer wait.Done()
						var err error
						if change == "停用" {
							_, err = agentaction.NewUpdateStatusAction(f.db, testServiceSessionReturner(f.db)).Execute(ctx, f.identity, agent.ID, domain.IdentityStatusInactive)
						} else {
							_, err = agentaction.NewUpdateAgentAction(f.db, testServiceSessionReturner(f.db)).Execute(ctx, f.identity, agent.ID, agentaction.UpdateInput{
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
				if err != nil {
					t.Fatal(err)
				}
			}
			stranded, err := f.db.NewSelect().Model((*servermodels.ServiceSession)(nil)).
				Where("ss.assignee_identity_id = ? AND ss.status = ?", agent.IdentityID, domain.ServiceSessionStatusOpen).Count(ctx)
			if err != nil || stranded != 0 {
				t.Fatalf("open sessions left on unavailable agent = %d, error = %v", stranded, err)
			}
		})
	}
}

// testInboundUnavailableAssignee 验证负责人在管理操作之外失去接待资格时，下一条客户消息在入站事务内把周期退回队列。
func testInboundUnavailableAssignee(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	agent := f.newAgent(t, "失效负责人客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	first := f.receive(t, &input, "第一条")
	run := f.queuedRun(t, first.Conversation.ID)
	if _, err := f.db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).Where("id = ?", agent.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	f.receive(t, &input, "第二条")
	events := returnedEvents(t, f.db, first.Conversation.ID)
	if len(events) != 1 || events[0].Reason != domain.ServiceSessionReturnAssigneeUnavailable {
		t.Fatalf("events = %+v", events)
	}
	if session := loadSession(t, f.db, run.ScopeID); session.AssigneeIdentityID != nil {
		t.Fatalf("session = %+v", session)
	}
	if err := f.db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil || run.Status != string(domain.AgentRunStatusCancelled) {
		t.Fatalf("run = %+v, error = %v", run, err)
	}
}

// testTelegramModelHandoff 验证 Telegram 会话主动转人工只产生一次对客投递，重复执行不追加。
func testTelegramModelHandoff(t *testing.T, f handoffFixture) {
	ctx := context.Background()
	fixture := newAgentTelegramFixture(t, f.db, f.identity, f.providerID, f.modelID)
	executor := agentrunaction.NewExecuteAction(f.db, fixture.tasks, handoffRuntime("需要人工确认", nil), testAttachmentReader(f.db), nil, nil)
	for range 2 {
		if err := executor.Execute(ctx, agentrunaction.RunInput{RunID: fixture.run.ID}); err != nil {
			t.Fatal(err)
		}
	}
	fixture.reload(t)
	var deliveries []servermodels.CustomerMessageDelivery
	if err := f.db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", fixture.run.ConversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if fixture.run.ResponseMessageID == nil || len(deliveries) != 1 || deliveries[0].MessageID != *fixture.run.ResponseMessageID ||
		len(handoffEvents(t, f.db, fixture.run.ConversationID)) != 1 {
		t.Fatalf("run = %+v, deliveries = %+v", fixture.run, deliveries)
	}
}

// testChannelEditVersusDeactivation 验证停用 AI 员工持有身份锁时，以其为路由目标的渠道编辑先等待身份锁再锁渠道，两者不形成循环等待。
func testChannelEditVersusDeactivation(t *testing.T, f handoffFixture) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	agent := f.newAgent(t, "渠道编辑并发客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	// 渠道编辑由另一名成员发起，两个操作人各自持有自己的账号锁。
	email := "channel-editor-" + uuid.NewV7().String()[:8] + "@handoff.test"
	if _, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "渠道编辑成员", Email: email, Password: "password123", RoleID: f.identity.User.RoleID,
	}); err != nil {
		t.Fatal(err)
	}
	disableAutoAssignment(t, f.db, f.identity.Organization.ID)
	editor := loginMember(t, f.db, f.identity.Organization.ID, email, "password123")
	gated := bun.NewDB(f.db.DB, f.db.Dialect())
	gated.AddQueryHook(chatQueryHook{})
	// 停用事务取得 AI 员工身份排他锁后暂停。
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return strings.Contains(event.Query, "organization_identities") && strings.Contains(event.Query, "FOR UPDATE OF oi")
	})
	deactivated, edited := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := agentaction.NewUpdateStatusAction(gated, testServiceSessionReturner(f.db)).Execute(context.WithValue(ctx, chatQueryGateKey{}, gate), f.identity, agent.ID, domain.IdentityStatusInactive)
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
	waitChatDatabaseLock(t, ctx, f.db, "organization_identities", agent.IdentityID)
	gate.open()
	if err := waitChatResult(t, ctx, deactivated); err != nil {
		t.Fatalf("deactivate agent: %v", err)
	}
	// 渠道编辑在停用提交后校验目标，AI 员工已不可用时按校验失败返回。
	var validation *channelaction.ValidationError
	if err := waitChatResult(t, ctx, edited); err != nil && !errors.As(err, &validation) {
		t.Fatalf("edit channel: %v", err)
	}
	channel := servermodels.Channel{}
	if err := f.db.NewSelect().Model(&channel).Where("c.id = ?", channelID).Scan(ctx); err != nil ||
		channel.InitialRoutingTargetType != string(domain.ChannelRoutingTargetTypePublicQueue) {
		t.Fatalf("channel = %+v, error = %v", channel, err)
	}
}

// testTelegramInboundReturnVersusRunFailure 验证已持有会话锁的入站事务发现负责人失效时，退回不锁渠道身份，与先锁渠道身份的运行收尾不形成循环等待；对客通知由转人工承接任务投递一次。
func testTelegramInboundReturnVersusRunFailure(t *testing.T, f handoffFixture) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fixture := newAgentTelegramFixture(t, f.db, f.identity, f.providerID, f.modelID)
	if _, err := f.db.NewUpdate().Model((*servermodels.Agent)(nil)).Set("status = ?", domain.IdentityStatusInactive).
		Where("identity_id = ?", fixture.run.AgentIdentityID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var messageID string
	if err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Column("id").
		Where("msg.conversation_id = ? AND msg.type = ?", fixture.run.ConversationID, domain.MessageTypeText).
		OrderExpr("msg.message_seq DESC").Limit(1).Scan(ctx, &messageID); err != nil {
		t.Fatal(err)
	}
	gated := bun.NewDB(f.db.DB, f.db.Dialect())
	gated.AddQueryHook(chatQueryHook{})
	// 运行失败收尾取得渠道身份锁后、申请会话锁前暂停。
	gate := newChatQueryGate(t, false, 1, func(event *bun.QueryEvent) bool {
		return strings.Contains(event.Query, "contact_channel_identities") && strings.Contains(event.Query, "FOR UPDATE")
	})
	failed, scheduled := make(chan error, 1), make(chan error, 1)
	go func() {
		executor := agentrunaction.NewExecuteAction(gated, fixture.tasks, nil, testAttachmentReader(f.db), nil, nil)
		failed <- executor.FinalizeFailure(context.WithValue(ctx, chatQueryGateKey{}, gate), agentrunaction.RunInput{RunID: fixture.run.ID}, errors.New("运行失败"))
	}()
	waitChatSignal(t, ctx, gate.reached)
	go func() {
		scheduled <- realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			locked, err := chatstate.LockServiceSession(ctx, tx, fixture.run.OrganizationID, fixture.run.ConversationID)
			if err != nil {
				return err
			}
			_, err = agentrunaction.NewScheduler(fixture.tasks).ScheduleCustomerAuto(ctx, tx, fixture.run.OrganizationID, fixture.run.ConversationID, locked.Session.ID, messageID)
			return err
		})
	}()
	// 入站事务在运行收尾暂停期间独立完成退回。
	select {
	case err := <-scheduled:
		if err != nil {
			t.Fatalf("inbound return: %v", err)
		}
	case <-time.After(5 * time.Second):
		gate.open()
		t.Fatal("inbound return waited for the channel identity lock held by run failure")
	}
	gate.open()
	if err := waitChatResult(t, ctx, failed); err != nil {
		t.Fatalf("finalize run failure: %v", err)
	}
	fixture.reload(t)
	if tasks := runReturnedHandoffs(t, f.db, fixture.run.ScopeID); tasks != 1 {
		t.Fatalf("returned handoff tasks = %d", tasks)
	}
	var deliveries []servermodels.CustomerMessageDelivery
	if err := f.db.NewSelect().Model(&deliveries).Where("cmd.conversation_id = ?", fixture.run.ConversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	events := returnedEvents(t, f.db, fixture.run.ConversationID)
	if fixture.run.Status != string(domain.AgentRunStatusCancelled) || len(events) != 1 ||
		events[0].Reason != domain.ServiceSessionReturnAssigneeUnavailable || len(deliveries) != 1 {
		t.Fatalf("run = %+v, events = %+v, deliveries = %+v", fixture.run, events, deliveries)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	email := "session-events-" + uuid.NewV7().String()[:8] + "@handoff.test"
	if _, err := newTestMemberCreator(f.db, newTestTasks(f.db)).Execute(ctx, f.identity, memberSpec{
		HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "接管成员", Email: email, Password: "password123", RoleID: f.identity.User.RoleID,
	}); err != nil {
		t.Fatal(err)
	}
	disableAutoAssignment(t, f.db, f.identity.Organization.ID)
	other := loginMember(t, f.db, f.identity.Organization.ID, email, "password123")
	input := visitorInput(channel.ID, "")
	first := f.receive(t, &input, "有人吗")
	conversationID := first.Conversation.ID
	coordinator := testServiceSessionReturner(f.db)
	scheduler := agentrunaction.NewScheduler(f.tasks)
	owner, member := f.identity.OrganizationIdentity.ID, other.Identity.OrganizationIdentity.ID
	// 成员回复无人负责的周期即领取。
	reply, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "在的",
	})
	if err != nil {
		t.Fatal(err)
	}
	summary := servermodels.Conversation{}
	if err := f.db.NewSelect().Model(&summary).Where("cv.id = ?", conversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, other.Identity, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, newTestTasks(f.db)).Execute(ctx, other.Identity, servicesessionaction.TransferServiceSessionInput{
		ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agent.IdentityID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.identity, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.identity, conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := servicesessionaction.NewReopenServiceSessionAction(f.db).Execute(ctx, f.identity, conversationID); err != nil {
		t.Fatal(err)
	}

	var messages []servermodels.Message
	if err := f.db.NewSelect().Model(&messages).
		Where("msg.conversation_id = ? AND msg.type = ?", conversationID, domain.MessageTypeSystem).
		OrderExpr("msg.message_seq").Scan(ctx); err != nil {
		t.Fatal(err)
	}
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
	if len(messages) != len(want) {
		t.Fatalf("service session events = %d, want %d", len(messages), len(want))
	}
	for index, message := range messages {
		event := domain.ServiceSessionOperatedEvent{}
		if err := json.Unmarshal(message.SystemEventPayload, &event); err != nil {
			t.Fatal(err)
		}
		expected := want[index]
		if message.SystemEventType == nil || *message.SystemEventType != string(expected.eventType) ||
			message.Visibility != string(domain.MessageVisibilityInternal) || message.ServiceSessionID == nil ||
			event.ActorIdentityID != expected.actor || event.ActorDisplayName == "" ||
			(expected.from == nil) != (event.FromIdentityID == nil) || (expected.from != nil && (*event.FromIdentityID != *expected.from || event.FromDisplayName == nil)) ||
			(expected.target == nil) != (event.Target == nil) || (expected.target != nil && (event.Target.IdentityID == nil || *event.Target.IdentityID != *expected.target)) ||
			(expected.eventType == domain.ConversationSystemEventServiceSessionClosed) != (event.CloseReason != nil) ||
			(event.CloseReason != nil && *event.CloseReason != domain.ServiceSessionCloseManual) {
			t.Fatalf("event %d = %s %+v", index, *message.SystemEventType, event)
		}
	}
	// 重新打开后清除结束方式。
	reopened := servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(&reopened).Where("ss.conversation_id = ?", conversationID).Scan(ctx); err != nil ||
		reopened.Status != string(domain.ServiceSessionStatusOpen) || reopened.CloseReason != nil {
		t.Fatalf("reopened session = %+v, error = %v", reopened, err)
	}
	// 周期事件不改变会话摘要与活动时间。
	after := servermodels.Conversation{}
	if err := f.db.NewSelect().Model(&after).Where("cv.id = ?", conversationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if after.LastMessageID == nil || *after.LastMessageID != reply.ID || after.LastActivityAt == nil || summary.LastActivityAt == nil || !after.LastActivityAt.Equal(*summary.LastActivityAt) {
		t.Fatalf("conversation summary before = %+v, after = %+v", summary, after)
	}
	// 成员历史按群聊事件的 actor 结构返回操作人。
	history, err := conversationaction.NewListConversationMessagesQuery(f.db).Execute(ctx, f.identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history.Messages {
		if message.SystemEvent != nil && (message.SystemEvent.ActorIdentityID == nil || message.SystemEvent.ServiceSessionID == nil) {
			t.Fatalf("history event = %+v", message.SystemEvent)
		}
	}
}
