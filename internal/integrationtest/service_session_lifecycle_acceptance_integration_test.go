//go:build server

// 本文件按服务周期流转全路径锁定 service_sessions 的最终字段值：AI 首接待、转人工排队（含客户不在等待时转人工）、领取、内部备注与对客回复、转接成员与团队、关闭、重新打开与关闭后客户再次来信。
package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/runforyou-ai/einorun"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// lifecycleSessionFixture 保存服务周期流转验收共用的工作区、AI 客服、两名真人客服、团队与网站渠道。
type lifecycleSessionFixture struct {
	db      *bun.DB
	owner   *servermodels.Identity
	tasks   *servertest.Tasks
	agent   *agentaction.Agent
	first   *servermodels.Identity
	second  *servermodels.Identity
	teamID  string
	aiChan  string
	queueCh string
}

// newLifecycleSessionFixture 建立首接待为 AI 客服、失败去向为公共队列的网站渠道，以及只进公共队列的网站渠道；关闭自动分配使负责人只随显式操作变化。
func newLifecycleSessionFixture(t *testing.T) lifecycleSessionFixture {
	t.Helper()
	ctx := context.Background()
	db, owner, _, modelID := newAIWorkspace(t)
	tasks := servertest.NewTasks()
	agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, owner, agentaction.CreateInput{
		DisplayName: "周期验收客服", ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer},
		Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
	})
	require.NoError(t, err)
	members := make([]*servermodels.Identity, 0, 2)
	for _, name := range []string{"一号客服", "二号客服"} {
		email := servertest.UniqueEmail("lifecycle")
		_, err := newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{
			DisplayName: name, Email: email, Password: "password123", RoleID: owner.User.RoleID, HandlesServiceRequests: true, MaxServiceSessions: 10,
		})
		require.NoError(t, err)
		members = append(members, servertest.LoginMember(t, db, owner.Workspace.ID, email, "password123").Identity)
	}
	team, err := teamaction.NewCreateTeamAction(db).Execute(ctx, owner, teamaction.Input{Name: "周期验收组 " + servertest.UniqueSuffix()})
	require.NoError(t, err)
	_, err = teamaction.NewAddMembersAction(db, testEnqueuer).Execute(ctx, owner, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: members[1].WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	disableAutoAssignment(t, db, owner.Workspace.ID)
	newChannel := func(target channelaction.RoutingTarget) string {
		channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, owner, channelaction.CreateMessageChannelInput{
			Type: domain.ChannelTypeWebsite, Name: "周期验收", DefaultLocale: domain.CustomerLocaleChineseSimplified,
			NewConversationTarget: target,
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		require.NoError(t, err)
		return channel.ID
	}
	return lifecycleSessionFixture{
		db: db, owner: owner, tasks: tasks, agent: agent, first: members[0], second: members[1], teamID: team.ID,
		aiChan:  newChannel(channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: agent.IdentityID}),
		queueCh: newChannel(channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue}),
	}
}

// receive 以访客身份写入一条网站消息，后续消息沿用同一会话，返回消息编号。
func (f lifecycleSessionFixture) receive(t *testing.T, input *customerchataction.WebsiteCustomerTextMessageInput, body string) customerchataction.ReceiveWebsiteCustomerMessageResult {
	t.Helper()
	input.ClientMessageID, input.Body = uuid.NewV7().String(), body
	result, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(f.db, agentrunaction.NewScheduler(f.tasks), testEnqueuer, servertest.DisabledMail{}).Execute(context.Background(), *input)
	require.NoError(t, err)
	input.ConversationID = &result.Conversation.ID
	return result
}

// current 读取会话当前服务周期。
func (f lifecycleSessionFixture) current(t *testing.T, conversationID string) servermodels.ServiceSession {
	t.Helper()
	session := servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(&session).
		Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.workspace_id = ss.workspace_id").
		Where("svc.workspace_id = ? AND svc.conversation_id = ?", f.owner.Workspace.ID, conversationID).
		Scan(context.Background()))
	return session
}

// runAgent 以给定的运行结果执行会话中排队的 AI 运行，返回运行写入的回复消息编号。
func (f lifecycleSessionFixture) runAgent(t *testing.T, conversationID string, result func(endSeq int64) agentruntime.RunResult) string {
	t.Helper()
	ctx := context.Background()
	run := servermodels.AgentRun{}
	require.NoError(t, f.db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.status = ?", conversationID, domain.AgentRunStatusQueued).Scan(ctx))
	runtime := &testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed einorun.Feed) (agentruntime.RunResult, error) {
		triggers, err := pendingTriggers(ctx, feed, 0)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		claimed, err := feed.Claim(ctx, triggers[len(triggers)-1].Seq)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		return result(claimed.EndSeq), nil
	}}
	require.NoError(t, newTestAgentRun(f.db, f.tasks, runtime, testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.NoError(t, f.db.NewSelect().Model(&run).WherePK().Scan(ctx))
	require.Equal(t, string(domain.AgentRunStatusSucceeded), run.Status, "run=%+v", run)
	require.NotNil(t, run.ResponseMessageID)
	return *run.ResponseMessageID
}

// reply 由成员向服务周期发送一条文本，visibility 区分对客回复与内部备注，返回消息编号。
func (f lifecycleSessionFixture) reply(t *testing.T, identity *servermodels.Identity, conversationID string, visibility domain.MessageVisibility) string {
	t.Helper()
	message, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(context.Background(), identity, servicesessionaction.ServiceTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "周期验收回复", Visibility: visibility,
	})
	require.NoError(t, err)
	return message.ID
}

// transfer 由当前负责人把周期转给成员或团队。
func (f lifecycleSessionFixture) transfer(t *testing.T, identity *servermodels.Identity, input servicesessionaction.TransferServiceSessionInput) {
	t.Helper()
	coordinator := testServiceSessionReturner(f.db)
	_, err := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, agentrunaction.NewScheduler(f.tasks), testEnqueuer).Execute(context.Background(), identity, input)
	require.NoError(t, err)
}

// lifecycleSessionOriginatedAt 读取消息的发生时间。
func lifecycleSessionOriginatedAt(t *testing.T, db *bun.DB, messageID string) time.Time {
	t.Helper()
	var at time.Time
	require.NoError(t, db.NewSelect().TableExpr("messages").Column("originated_at").Where("id = ?", messageID).Scan(context.Background(), &at))
	return at
}

// lifecycleSessionDynamic 列出由编号、数据库时钟与异步小结决定的字段，状态比较时忽略，时间字段另按是否为空与相对关系断言。
var lifecycleSessionDynamic = cmpopts.IgnoreFields(servermodels.ServiceSession{},
	"ID", "CreatedAt", "UpdatedAt", "WorkspaceID", "ConversationID", "ServiceConversationID", "OpeningMessageID", "LastMessageID", "LastMessageAt",
	"AssignedAt", "AssigneeAssignedAt", "AwaitingReplySince", "QueuedAt", "RemindedAt", "FirstResponseAt", "HumanRequestedAt", "HumanAssignedAt",
	"HumanFirstResponseAt", "HumanFirstResponseSec", "StatusChangedAt", "ClosedAt", "ResolutionRequestedAt", "VisitorContext",
	"SummaryStatus", "Summary", "Resolved", "SummaryEditedByID", "SummaryEditedAt", "HandoffMessageID", "HandoffSummary")

// lifecycleSessionTimes 给出周期各时间字段是否已记录。
func lifecycleSessionTimes(session servermodels.ServiceSession) map[string]bool {
	return map[string]bool{
		"assigned_at": session.AssignedAt != nil, "assignee_assigned_at": session.AssigneeAssignedAt != nil,
		"awaiting_reply_since": session.AwaitingReplySince != nil, "queued_at": session.QueuedAt != nil, "reminded_at": session.RemindedAt != nil,
		"first_response_at": session.FirstResponseAt != nil, "human_requested_at": session.HumanRequestedAt != nil,
		"human_assigned_at": session.HumanAssignedAt != nil, "human_first_response_at": session.HumanFirstResponseAt != nil,
		"closed_at": session.ClosedAt != nil, "resolution_requested_at": session.ResolutionRequestedAt != nil,
	}
}

// lifecycleSessionTimeSet 由已记录的时间字段名构造期望集合，其余字段视为未记录。
func lifecycleSessionTimeSet(recorded ...string) map[string]bool {
	times := lifecycleSessionTimes(servermodels.ServiceSession{})
	for _, name := range recorded {
		times[name] = true
	}
	return times
}

// requireLifecycleSession 比较周期的状态字段与时间字段记录情况。
func requireLifecycleSession(t *testing.T, step string, want servermodels.ServiceSession, wantTimes map[string]bool, got servermodels.ServiceSession) {
	t.Helper()
	if diff := cmp.Diff(want, got, lifecycleSessionDynamic); diff != "" {
		t.Fatalf("%s 状态字段不符 (-want +got):\n%s", step, diff)
	}
	if diff := cmp.Diff(wantTimes, lifecycleSessionTimes(got)); diff != "" {
		t.Fatalf("%s 时间字段记录不符 (-want +got):\n%s", step, diff)
	}
}

// requireSameTime 断言两个已记录的时间相等。
func requireSameTime(t *testing.T, step string, want time.Time, got *time.Time) {
	t.Helper()
	require.NotNil(t, got, step)
	require.True(t, want.Equal(*got), "%s: want %s got %s", step, want, *got)
}

// TestServiceSessionLifecycleAcceptance 验证服务周期各流转步骤后的最终字段值。
func TestServiceSessionLifecycleAcceptance(t *testing.T) {
	t.Parallel()
	f := newLifecycleSessionFixture(t)
	t.Run("AI 首接待到转人工、领取、转接、关闭与重开", func(t *testing.T) { testLifecycleSessionAIPath(t, f) })
	t.Run("公共队列进线由成员回复领取", func(t *testing.T) { testLifecycleSessionQueuePath(t, f) })
	t.Run("关闭后客户再次来信", func(t *testing.T) { testLifecycleSessionInboundAfterClose(t, f) })
	t.Run("转给 AI 后再退回公共队列", func(t *testing.T) { testLifecycleSessionTransferToAgent(t, f) })
	t.Run("客户不在等待时 AI 转人工", func(t *testing.T) { testLifecycleSessionHandoffWithoutAwaiting(t, f) })
}

// testLifecycleSessionAIPath 走完 AI 首接待周期的完整流转，逐步核对字段。
func testLifecycleSessionAIPath(t *testing.T, f lifecycleSessionFixture) {
	ctx := context.Background()
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.aiChan, ExternalID: lifecycleSessionVisitor()}
	opened := f.receive(t, &input, "我要退款")
	conversationID := opened.Conversation.ID
	agentID := f.agent.IdentityID
	session := f.current(t, conversationID)
	openingAt := lifecycleSessionOriginatedAt(t, f.db, session.OpeningMessageID)
	// 进线即由 AI 客服负责，客户等待从首条消息起算，尚无真人参与。
	requireLifecycleSession(t, "进线", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &agentID, AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "awaiting_reply_since"), session)
	requireSameTime(t, "进线等待起点", openingAt, session.AwaitingReplySince)
	require.True(t, session.StatusChangedAt.Equal(openingAt), "进线状态时间=%s 首条=%s", session.StatusChangedAt, openingAt)
	assignedAt := *session.AssignedAt

	// AI 对客回复记录首响并结束客户等待。
	aiReply := f.runAgent(t, conversationID, func(endSeq int64) agentruntime.RunResult {
		return agentruntime.RunResult{Content: "请提供订单号", EndSeq: endSeq}
	})
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "AI 回复", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &agentID, AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "first_response_at"), session)
	aiReplyAt := lifecycleSessionOriginatedAt(t, f.db, aiReply)
	requireSameTime(t, "AI 首响", aiReplyAt, session.FirstResponseAt)
	require.Equal(t, aiReply, session.LastMessageID)

	// 客户再次来信重新开始等待，AI 随后转人工进入公共队列，等待起点保留客户追问的时刻。
	followUp := f.receive(t, &input, "订单号 123")
	followUpAt := lifecycleSessionOriginatedAt(t, f.db, followUp.Message.ID)
	session = f.current(t, conversationID)
	requireSameTime(t, "客户追问等待起点", followUpAt, session.AwaitingReplySince)
	f.runAgent(t, conversationID, func(endSeq int64) agentruntime.RunResult {
		return agentruntime.RunResult{EndSeq: endSeq, Decision: agentcontract.TerminalDecision{
			Kind: domain.AgentRunOutcomeHandoff, Reason: domain.AgentHandoffReasonKnowledgeGap, ReasonText: "需要人工退款",
		}}
	})
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "转人工", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "awaiting_reply_since", "queued_at", "first_response_at", "human_requested_at"), session)
	requireSameTime(t, "转人工后首次负责时间不变", assignedAt, session.AssignedAt)
	requireSameTime(t, "转人工后首响不变", aiReplyAt, session.FirstResponseAt)
	requireSameTime(t, "排队起点即需要真人时间", *session.HumanRequestedAt, session.QueuedAt)
	requireSameTime(t, "转人工保留客户等待起点", followUpAt, session.AwaitingReplySince)
	humanRequestedAt := *session.HumanRequestedAt
	handoffAwaiting := *session.AwaitingReplySince

	// 成员领取后清空排队，记录真人接手，客户等待保持。
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.first, conversationID)
	require.NoError(t, err)
	firstID := f.first.WorkspaceIdentity.ID
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "领取", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &firstID, AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "awaiting_reply_since", "first_response_at", "human_requested_at", "human_assigned_at"), session)
	requireSameTime(t, "领取后首次负责时间不变", assignedAt, session.AssignedAt)
	requireSameTime(t, "领取后需要真人时间不变", humanRequestedAt, session.HumanRequestedAt)
	requireSameTime(t, "领取后等待起点不变", handoffAwaiting, session.AwaitingReplySince)
	requireSameTime(t, "真人接手即负责人接手", *session.AssigneeAssignedAt, session.HumanAssignedAt)
	humanAssignedAt := *session.HumanAssignedAt

	// 内部备注不结束客户等待，也不记录真人首响。
	note := f.reply(t, f.first, conversationID, domain.MessageVisibilityInternal)
	session = f.current(t, conversationID)
	requireSameTime(t, "备注后等待起点不变", handoffAwaiting, session.AwaitingReplySince)
	require.Nil(t, session.HumanFirstResponseAt, "备注记录了真人首响")
	require.NotEqual(t, note, session.LastMessageID, "备注推进了周期最后消息")

	// 成员对客回复结束等待并记录真人首响，周期首响保留 AI 的时间。
	memberReply := f.reply(t, f.first, conversationID, domain.MessageVisibilityShared)
	memberReplyAt := lifecycleSessionOriginatedAt(t, f.db, memberReply)
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "成员回复", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &firstID, AgentIdentityID: &agentID,
		HumanFirstResponseSec: session.HumanFirstResponseSec,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "first_response_at", "human_requested_at", "human_assigned_at", "human_first_response_at"), session)
	requireSameTime(t, "真人首响", memberReplyAt, session.HumanFirstResponseAt)
	requireSameTime(t, "成员回复后周期首响仍为 AI", aiReplyAt, session.FirstResponseAt)
	require.Equal(t, memberReply, session.LastMessageID)

	// 转给另一成员只换负责人与接手时间，真人首次接手与首响不变。
	f.transfer(t, f.first, servicesessionaction.TransferServiceSessionInput{ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.second.WorkspaceIdentity.ID})
	secondID := f.second.WorkspaceIdentity.ID
	before := session
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "转给成员", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &secondID, AgentIdentityID: &agentID,
		HumanFirstResponseSec: before.HumanFirstResponseSec,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "first_response_at", "human_requested_at", "human_assigned_at", "human_first_response_at"), session)
	require.True(t, session.AssigneeAssignedAt.After(*before.AssigneeAssignedAt), "转接后接手时间未更新")
	requireSameTime(t, "转接后真人首次接手不变", humanAssignedAt, session.HumanAssignedAt)
	requireSameTime(t, "转接后真人首响不变", memberReplyAt, session.HumanFirstResponseAt)

	// 转给团队退回该团队队列：清空负责人与接手时间，从转接时刻排队。
	f.transfer(t, f.second, servicesessionaction.TransferServiceSessionInput{ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetTeam, TeamID: f.teamID})
	teamID := f.teamID
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "转给团队", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), TeamID: &teamID, AgentIdentityID: &agentID,
		HumanFirstResponseSec: before.HumanFirstResponseSec,
	}, lifecycleSessionTimeSet("assigned_at", "queued_at", "first_response_at", "human_requested_at", "human_assigned_at", "human_first_response_at"), session)
	requireSameTime(t, "转团队后需要真人时间不变", humanRequestedAt, session.HumanRequestedAt)

	// 队列中的周期由关闭人领取后关闭，关闭清空排队与等待，负责人记为关闭人。
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.first, conversationID)
	require.NoError(t, err)
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "关闭", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusClosed), TeamID: &teamID, AssigneeIdentityID: &firstID, AgentIdentityID: &agentID,
		ClosedByIdentityID: &firstID, CloseReason: new(string(domain.ServiceSessionCloseManual)), HumanFirstResponseSec: before.HumanFirstResponseSec,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "first_response_at", "human_requested_at", "human_assigned_at", "human_first_response_at", "closed_at"), session)
	requireSameTime(t, "关闭时间即状态变化时间", session.StatusChangedAt, session.ClosedAt)
	requireSameTime(t, "关闭人领取即关闭时刻", *session.ClosedAt, session.AssigneeAssignedAt)
	closedAt := *session.ClosedAt

	// 重新打开清空关闭记录并交给重开人，客户等待不恢复。
	_, err = servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer).Execute(ctx, f.second, conversationID)
	require.NoError(t, err)
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "重开", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), TeamID: &teamID, AssigneeIdentityID: &secondID, AgentIdentityID: &agentID,
		HumanFirstResponseSec: before.HumanFirstResponseSec,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "first_response_at", "human_requested_at", "human_assigned_at", "human_first_response_at"), session)
	require.True(t, session.StatusChangedAt.After(closedAt), "重开状态时间未推进")
	requireSameTime(t, "重开人接手即状态变化时间", session.StatusChangedAt, session.AssigneeAssignedAt)
	requireSameTime(t, "重开后首次负责时间不变", assignedAt, session.AssignedAt)
	requireSameTime(t, "重开后真人首次接手不变", humanAssignedAt, session.HumanAssignedAt)
}

// testLifecycleSessionQueuePath 验证进入公共队列的新周期由成员直接回复领取，周期首响与真人首响同为该回复。
func testLifecycleSessionQueuePath(t *testing.T, f lifecycleSessionFixture) {
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.queueCh, ExternalID: lifecycleSessionVisitor()}
	opened := f.receive(t, &input, "怎么开发票")
	conversationID := opened.Conversation.ID
	session := f.current(t, conversationID)
	openingAt := lifecycleSessionOriginatedAt(t, f.db, session.OpeningMessageID)
	// 进线即排队并需要真人，等待从首条消息起算。
	requireLifecycleSession(t, "进线排队", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen),
	}, lifecycleSessionTimeSet("awaiting_reply_since", "queued_at", "human_requested_at"), session)
	requireSameTime(t, "进线等待起点", openingAt, session.AwaitingReplySince)
	requireSameTime(t, "进线排队起点", openingAt, session.QueuedAt)
	requireSameTime(t, "进线需要真人", openingAt, session.HumanRequestedAt)

	reply := f.reply(t, f.first, conversationID, domain.MessageVisibilityShared)
	replyAt := lifecycleSessionOriginatedAt(t, f.db, reply)
	firstID := f.first.WorkspaceIdentity.ID
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "回复领取", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &firstID, HumanFirstResponseSec: session.HumanFirstResponseSec,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "first_response_at", "human_requested_at", "human_assigned_at", "human_first_response_at"), session)
	for name, at := range map[string]*time.Time{
		"首次负责": session.AssignedAt, "负责人接手": session.AssigneeAssignedAt, "真人接手": session.HumanAssignedAt,
		"周期首响": session.FirstResponseAt, "真人首响": session.HumanFirstResponseAt,
	} {
		requireSameTime(t, name, replyAt, at)
	}

	// 客户再次来信重新等待，成员第二次回复不改变首响。
	followUp := f.receive(t, &input, "还需要抬头吗")
	session = f.current(t, conversationID)
	requireSameTime(t, "追问等待起点", lifecycleSessionOriginatedAt(t, f.db, followUp.Message.ID), session.AwaitingReplySince)
	f.reply(t, f.first, conversationID, domain.MessageVisibilityShared)
	session = f.current(t, conversationID)
	require.Nil(t, session.AwaitingReplySince, "再次回复未结束等待")
	requireSameTime(t, "再次回复后周期首响不变", replyAt, session.FirstResponseAt)
	requireSameTime(t, "再次回复后真人首响不变", replyAt, session.HumanFirstResponseAt)
}

// testLifecycleSessionInboundAfterClose 验证关闭后补记的内部备注不改变已关闭周期，客户再次来信开启下一周期并进入渠道去向，已关闭周期保持原值。
func testLifecycleSessionInboundAfterClose(t *testing.T, f lifecycleSessionFixture) {
	ctx := context.Background()
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.queueCh, ExternalID: lifecycleSessionVisitor()}
	opened := f.receive(t, &input, "第一次咨询")
	conversationID := opened.Conversation.ID
	f.reply(t, f.first, conversationID, domain.MessageVisibilityShared)
	_, err := servicesessionaction.NewCloseServiceSessionAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.first, conversationID)
	require.NoError(t, err)
	closed := f.current(t, conversationID)
	// 已关闭周期上的内部备注只进入时间线，周期字段不变。
	f.reply(t, f.second, conversationID, domain.MessageVisibilityInternal)
	if diff := cmp.Diff(closed, f.current(t, conversationID), cmpopts.IgnoreFields(servermodels.ServiceSession{}, "UpdatedAt")); diff != "" {
		t.Fatalf("备注改变了已关闭周期 (-want +got):\n%s", diff)
	}
	again := f.receive(t, &input, "又有问题")
	require.Equal(t, conversationID, again.Conversation.ID, "再次来信换了会话")
	require.True(t, again.OpenedNewServiceSession, "再次来信未开启新周期")
	session := f.current(t, conversationID)
	require.NotEqual(t, closed.ID, session.ID)
	requireLifecycleSession(t, "新周期", servermodels.ServiceSession{
		Sequence: 2, Status: string(domain.ServiceSessionStatusOpen),
	}, lifecycleSessionTimeSet("awaiting_reply_since", "queued_at", "human_requested_at"), session)
	requireSameTime(t, "新周期等待起点", lifecycleSessionOriginatedAt(t, f.db, again.Message.ID), session.AwaitingReplySince)
	require.Equal(t, again.Message.ID, session.OpeningMessageID)
	// 上一周期保持关闭时的全部字段。
	previous := loadSession(t, f.db, closed.ID)
	if diff := cmp.Diff(closed, previous, cmpopts.IgnoreFields(servermodels.ServiceSession{}, "UpdatedAt", "SummaryStatus")); diff != "" {
		t.Fatalf("再次来信改变了已关闭周期 (-want +got):\n%s", diff)
	}
}

// testLifecycleSessionTransferToAgent 验证队列进线的周期由成员转给 AI 客服时首次记为 AI 接待，再由 AI 负责人之外的流转退回公共队列。
func testLifecycleSessionTransferToAgent(t *testing.T, f lifecycleSessionFixture) {
	ctx := context.Background()
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.queueCh, ExternalID: lifecycleSessionVisitor()}
	conversationID := f.receive(t, &input, "想咨询套餐").Conversation.ID
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.first, conversationID)
	require.NoError(t, err)
	claimed := f.current(t, conversationID)
	agentID, firstID := f.agent.IdentityID, f.first.WorkspaceIdentity.ID
	require.Equal(t, &firstID, claimed.AssigneeIdentityID)
	require.Nil(t, claimed.AgentIdentityID)
	f.transfer(t, f.first, servicesessionaction.TransferServiceSessionInput{ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: agentID})
	session := f.current(t, conversationID)
	// 转给 AI 保留需要真人与真人接手的时间，客户等待保持进线时刻。
	requireLifecycleSession(t, "转给 AI", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AssigneeIdentityID: &agentID, AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "assignee_assigned_at", "awaiting_reply_since", "human_requested_at", "human_assigned_at"), session)
	requireSameTime(t, "转给 AI 后需要真人时间不变", *claimed.HumanRequestedAt, session.HumanRequestedAt)
	requireSameTime(t, "转给 AI 后真人接手不变", *claimed.HumanAssignedAt, session.HumanAssignedAt)
	requireSameTime(t, "转给 AI 后首次负责时间不变", *claimed.AssignedAt, session.AssignedAt)
	require.True(t, session.AssigneeAssignedAt.After(*claimed.AssigneeAssignedAt), "转给 AI 后接手时间未更新")
	requireSameTime(t, "转给 AI 后等待起点不变", *claimed.AwaitingReplySince, session.AwaitingReplySince)

	// AI 回复记录周期首响，真人首响仍为空。
	reply := f.runAgent(t, conversationID, func(endSeq int64) agentruntime.RunResult {
		return agentruntime.RunResult{Content: "套餐如下", EndSeq: endSeq}
	})
	session = f.current(t, conversationID)
	requireSameTime(t, "AI 首响", lifecycleSessionOriginatedAt(t, f.db, reply), session.FirstResponseAt)
	require.Nil(t, session.HumanFirstResponseAt)
	require.Nil(t, session.AwaitingReplySince)

	// 成员领取 AI 负责的周期时需要真人与真人接手的时间保持首次记录，转回公共队列后 AI 接待记录保留。
	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, testServiceSessionReturner(f.db), testEnqueuer).Execute(ctx, f.second, conversationID)
	require.NoError(t, err)
	reclaimed := f.current(t, conversationID)
	requireSameTime(t, "领取 AI 周期后需要真人时间不变", *claimed.HumanRequestedAt, reclaimed.HumanRequestedAt)
	requireSameTime(t, "领取 AI 周期后真人接手不变", *claimed.HumanAssignedAt, reclaimed.HumanAssignedAt)
	f.transfer(t, f.second, servicesessionaction.TransferServiceSessionInput{ConversationID: conversationID, TargetKind: domain.ServiceSessionTargetPublicQueue})
	session = f.current(t, conversationID)
	requireLifecycleSession(t, "转回公共队列", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "queued_at", "first_response_at", "human_requested_at", "human_assigned_at"), session)
	requireSameTime(t, "退回后需要真人时间不变", *reclaimed.HumanRequestedAt, session.HumanRequestedAt)
	requireSameTime(t, "退回后真人接手不变", *reclaimed.HumanAssignedAt, session.HumanAssignedAt)
}

// testLifecycleSessionHandoffWithoutAwaiting 验证 AI 转人工时客户不在等待则以交接时间为客户等待起点。
func testLifecycleSessionHandoffWithoutAwaiting(t *testing.T, f lifecycleSessionFixture) {
	ctx := context.Background()
	input := customerchataction.WebsiteCustomerTextMessageInput{ChannelID: f.aiChan, ExternalID: lifecycleSessionVisitor()}
	conversationID := f.receive(t, &input, "帮我看看物流").Conversation.ID
	session := f.current(t, conversationID)
	require.NotNil(t, session.AwaitingReplySince)
	// 运行转人工前客户等待已结束。
	_, err := f.db.NewUpdate().Model((*servermodels.ServiceSession)(nil)).Set("awaiting_reply_since = NULL").
		Where("workspace_id = ? AND id = ?", session.WorkspaceID, session.ID).Exec(ctx)
	require.NoError(t, err)
	f.runAgent(t, conversationID, func(endSeq int64) agentruntime.RunResult {
		return agentruntime.RunResult{EndSeq: endSeq, Decision: agentcontract.TerminalDecision{
			Kind: domain.AgentRunOutcomeHandoff, Reason: domain.AgentHandoffReasonKnowledgeGap, ReasonText: "需要人工查询物流",
		}}
	})
	session = f.current(t, conversationID)
	agentID := f.agent.IdentityID
	requireLifecycleSession(t, "不等待时转人工", servermodels.ServiceSession{
		Sequence: 1, Status: string(domain.ServiceSessionStatusOpen), AgentIdentityID: &agentID,
	}, lifecycleSessionTimeSet("assigned_at", "awaiting_reply_since", "queued_at", "first_response_at", "human_requested_at"), session)
	requireSameTime(t, "不等待时转人工以交接时间为等待起点", *session.QueuedAt, session.AwaitingReplySince)
	requireSameTime(t, "交接时间即需要真人时间", *session.QueuedAt, session.HumanRequestedAt)
}

// lifecycleSessionVisitor 返回新的网站访客外部编号。
func lifecycleSessionVisitor() string {
	return "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")
}
