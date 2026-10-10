//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// assignmentFixture 保存自动分配测试共用的客服会话环境与分配执行器。
type assignmentFixture struct {
	customerReadFixture
	worker *serviceassignment.Worker
}

// newAssignmentFixture 建立两名开启接待且工作中的客服与一个路由到公共队列的网站渠道。
func newAssignmentFixture(t *testing.T) assignmentFixture {
	t.Helper()
	f := newCustomerReadFixture(t)
	return assignmentFixture{customerReadFixture: f, worker: serviceassignment.NewWorker(f.db, testEnqueuer)}
}

// newConversation 以新访客身份进线并返回客户会话当前客服周期。
func (f assignmentFixture) newConversation(t *testing.T) servermodels.ServiceSession {
	t.Helper()
	result, err := f.receive.Execute(context.Background(), customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), ClientMessageID: uuid.NewV7().String(), Body: "需要帮助",
	})
	require.NoError(t, err)
	return f.currentSession(t, result.Conversation.ID)
}

// currentSession 读取客户会话当前客服周期。
func (f assignmentFixture) currentSession(t *testing.T, conversationID string) servermodels.ServiceSession {
	t.Helper()
	session := servermodels.ServiceSession{}
	require.NoError(t, f.db.NewSelect().Model(&session).
		Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.workspace_id = ss.workspace_id").
		Where("svc.workspace_id = ? AND svc.conversation_id = ?", f.owner.Workspace.ID, conversationID).
		Scan(context.Background()))
	return session
}

// assign 执行单个客服周期的分配任务并返回分配后的周期。
func (f assignmentFixture) assign(t *testing.T, session servermodels.ServiceSession, exclude string) servermodels.ServiceSession {
	t.Helper()
	require.NoError(t, f.worker.Assign(context.Background(), serviceassignment.AssignInput{WorkspaceID: f.owner.Workspace.ID, ServiceSessionID: session.ID, ExcludeIdentityID: exclude}))
	return f.currentSession(t, session.ConversationID)
}

// setMaxSessions 直接修改成员最大接待量。
func (f assignmentFixture) setMaxSessions(t *testing.T, identity *servermodels.Identity, max int) {
	t.Helper()
	_, err := f.db.NewUpdate().Table("users").Set("max_service_sessions = ?", max).Where("id = ?", identity.User.ID).Exec(context.Background())
	require.NoError(t, err)
}

// setWorkStatus 通过工作状态操作切换成员工作状态。
func (f assignmentFixture) setWorkStatus(t *testing.T, identity *servermodels.Identity, status domain.WorkStatus) {
	t.Helper()
	_, err := useraction.NewUpdateWorkStatusAction(f.db, testEnqueuer).Execute(context.Background(), identity, useraction.WorkStatusInput{WorkStatus: status})
	require.NoError(t, err)
}

// assignedEvents 读取客服周期的自动分配事件。
func (f assignmentFixture) assignedEvents(t *testing.T, sessionID string) []domain.ServiceSessionAssignedEvent {
	t.Helper()
	var messages []servermodels.Message
	require.NoError(t, f.db.NewSelect().Model(&messages).
		Where("msg.service_session_id = ? AND msg.system_event_type = ?", sessionID, domain.ConversationSystemEventServiceSessionAssigned).
		Order("msg.message_seq").Scan(context.Background()))
	events := make([]domain.ServiceSessionAssignedEvent, 0, len(messages))
	for _, message := range messages {
		event := domain.ServiceSessionAssignedEvent{}
		require.NoError(t, json.Unmarshal(message.SystemEventPayload, &event))
		events = append(events, event)
	}
	return events
}

// assigneeOf 返回客服周期负责人编号，队列中返回空字符串。
func assigneeOf(session servermodels.ServiceSession) string {
	if session.AssigneeIdentityID == nil {
		return ""
	}
	return *session.AssigneeIdentityID
}

// TestServiceSessionAutoAssignment 验证队列会话按接待量与轮转分配、满员与非工作中不分配、事件与字段写入，以及切换工作中后的补分配。
func TestServiceSessionAutoAssignment(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	owner, member := f.owner.WorkspaceIdentity.ID, f.member.WorkspaceIdentity.ID

	// 固定夹具首条会话已在公共队列中，入站写入客户等待起点。
	first := f.currentSession(t, f.conversationID)
	require.Nil(t, first.AssigneeIdentityID)
	require.NotNil(t, first.AwaitingReplySince)
	first = f.assign(t, first, "")
	firstAssignee := assigneeOf(first)
	require.Contains(t, []string{owner, member}, firstAssignee)
	require.NotNil(t, first.AssigneeAssignedAt)
	require.NotNil(t, first.AssignedAt)
	require.Nil(t, first.TeamID)
	events := f.assignedEvents(t, first.ID)
	require.Len(t, events, 1)
	require.Equal(t, &firstAssignee, events[0].Target.IdentityID)
	require.Equal(t, domain.ServiceSessionTargetPublicQueue, events[0].Source.Kind)
	// 重复执行分配任务不改变结果。
	again := f.assign(t, first, "")
	require.Equal(t, firstAssignee, assigneeOf(again))
	require.Len(t, f.assignedEvents(t, first.ID), 1)

	// 接待量较少的成员优先。
	second := f.assign(t, f.newConversation(t), "")
	require.NotEqual(t, firstAssignee, assigneeOf(second))
	require.NotEmpty(t, assigneeOf(second))

	// 成员满员后只分配给未满员成员。
	f.setMaxSessions(t, f.member, 1)
	third := f.assign(t, f.newConversation(t), "")
	require.Equal(t, owner, assigneeOf(third))

	// 群主休息中且成员满员时留在队列。
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	waiting := f.assign(t, f.newConversation(t), "")
	require.Nil(t, waiting.AssigneeIdentityID)

	// 关闭本人负责的周期后补分配最早等待的队列会话。
	memberSession := first
	if firstAssignee != member {
		memberSession = second
	}
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), testEnqueuer)
	_, err := closeSession.Execute(ctx, f.member, memberSession.ConversationID)
	require.NoError(t, err)
	require.Nil(t, f.currentSession(t, memberSession.ConversationID).AwaitingReplySince)
	require.NoError(t, f.worker.Backfill(ctx, serviceassignment.BackfillInput{WorkspaceID: f.owner.Workspace.ID, IdentityID: member}))
	require.Equal(t, member, assigneeOf(f.currentSession(t, waiting.ConversationID)))

	// 群主切换回工作中后补分配，直到没有等待中的会话。
	extra := f.newConversation(t)
	f.setWorkStatus(t, f.owner, domain.WorkStatusWorking)
	require.NoError(t, f.worker.Backfill(ctx, serviceassignment.BackfillInput{WorkspaceID: f.owner.Workspace.ID, IdentityID: owner}))
	require.Equal(t, owner, assigneeOf(f.currentSession(t, extra.ConversationID)))
	var lastAssigned servermodels.User
	require.NoError(t, f.db.NewSelect().Model(&lastAssigned).Where("u.id = ?", f.owner.User.ID).Scan(ctx))
	require.NotNil(t, lastAssigned.LastServiceAssignedAt)
}

// TestServiceSessionAssignmentScope 验证团队队列只分配给团队成员、排除原负责人，以及成员回复结束客户等待。
func TestServiceSessionAssignmentScope(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	owner, member := f.owner.WorkspaceIdentity.ID, f.member.WorkspaceIdentity.ID
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "售前组"})
	require.NoError(t, err)
	addMember := func() {
		t.Helper()
		_, err := teamaction.NewAddMembersAction(f.db, testEnqueuer).Execute(ctx, f.owner, team.ID, []teamaction.MemberIdentity{
			{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: member},
		})
		require.NoError(t, err)
	}
	// 统计为该成员投递的补分配任务数。
	backfills := func() int {
		t.Helper()
		return len(servertest.QueuedInputs(t, testEnqueuer, serviceassignment.BackfillActionName, func(input serviceassignment.BackfillInput) bool {
			return input.IdentityID == member
		}))
	}
	before := backfills()
	addMember()
	// 已在团队中的成员再次加入时不重复补分配。
	addMember()
	require.Equal(t, 1, backfills()-before)

	// 群主接待量更少，但团队队列只分配给团队成员。
	require.Equal(t, member, assigneeOf(f.assign(t, f.currentSession(t, f.conversationID), owner)))
	teamSession := f.newConversation(t)
	_, err = f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).Where("id = ?", teamSession.ID).Exec(ctx)
	require.NoError(t, err)
	teamSession.TeamID = &team.ID
	require.Equal(t, member, assigneeOf(f.assign(t, teamSession, "")))
	events := f.assignedEvents(t, teamSession.ID)
	require.Len(t, events, 1)
	require.Equal(t, domain.ServiceSessionTargetTeam, events[0].Source.Kind)
	require.Equal(t, &team.Name, events[0].Source.TeamName)

	// 成员对客回复结束客户等待。
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	_, err = send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: teamSession.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "您好", Visibility: domain.MessageVisibilityShared,
	})
	require.NoError(t, err)
	require.Nil(t, f.currentSession(t, teamSession.ConversationID).AwaitingReplySince)

	// 转回公共队列清空负责人与接手时间，分配任务排除原负责人。
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), agentrunaction.NewScheduler(testEnqueuer), testEnqueuer)
	_, err = transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: teamSession.ConversationID, TargetKind: domain.ServiceSessionTargetPublicQueue,
	})
	require.NoError(t, err)
	queued := f.currentSession(t, teamSession.ConversationID)
	require.Nil(t, queued.AssigneeIdentityID)
	require.Nil(t, queued.AssigneeAssignedAt)
	require.Nil(t, queued.TeamID)
	require.Equal(t, owner, assigneeOf(f.assign(t, queued, member)))
}

// TestServiceSessionRouteSkipsInactiveMember 验证渠道指定成员不在工作中时入站改走备用路由。
func TestServiceSessionRouteSkipsInactiveMember(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	member := f.member.WorkspaceIdentity.ID
	_, err := channelaction.NewUpdateMessageChannelAction(f.db).ExecuteReception(ctx, f.owner, f.channelID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: member},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	routed := f.newConversation(t)
	require.Equal(t, member, assigneeOf(routed))
	require.NotNil(t, routed.AssigneeAssignedAt)
	f.setWorkStatus(t, f.member, domain.WorkStatusOffDuty)
	require.Nil(t, f.newConversation(t).AssigneeIdentityID)
}

// TestServiceSessionAssignmentConcurrency 验证并发分配按接待量均分，并发补分配在周期被抢走后继续补下一条，补分配排除指定周期。
func TestServiceSessionAssignmentConcurrency(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	owner, member := f.owner.WorkspaceIdentity.ID, f.member.WorkspaceIdentity.ID
	f.assign(t, f.currentSession(t, f.conversationID), "")
	f.assign(t, f.newConversation(t), "")

	// 两条会话同时分配时各分给一名成员。
	for round := range 3 {
		sessions := []servermodels.ServiceSession{f.newConversation(t), f.newConversation(t)}
		errs := make(chan error, len(sessions))
		for _, session := range sessions {
			go func() {
				errs <- f.worker.Assign(ctx, serviceassignment.AssignInput{WorkspaceID: f.owner.Workspace.ID, ServiceSessionID: session.ID})
			}()
		}
		for range sessions {
			require.NoError(t, <-errs)
		}
		first, second := assigneeOf(f.currentSession(t, sessions[0].ConversationID)), assigneeOf(f.currentSession(t, sessions[1].ConversationID))
		require.NotEmpty(t, first, "round %d", round)
		require.NotEmpty(t, second, "round %d", round)
		require.NotEqual(t, first, second, "round %d", round)
	}

	// 两名成员同时补分配时，队列中的会话全部分配。
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	queued := []servermodels.ServiceSession{f.newConversation(t), f.newConversation(t), f.newConversation(t)}
	_, err := f.db.NewUpdate().Table("workspace_identities").Set("work_status = ?", domain.WorkStatusWorking).
		Where("id IN (?)", bun.List([]string{owner, member})).Exec(ctx)
	require.NoError(t, err)
	errs := make(chan error, 2)
	for _, identityID := range []string{owner, member} {
		go func() {
			errs <- f.worker.Backfill(ctx, serviceassignment.BackfillInput{WorkspaceID: f.owner.Workspace.ID, IdentityID: identityID})
		}()
	}
	for range 2 {
		require.NoError(t, <-errs)
	}
	for _, session := range queued {
		require.NotEmpty(t, assigneeOf(f.currentSession(t, session.ConversationID)), "session %s left in queue after concurrent backfill", session.ID)
	}

	// 补分配跳过排除的周期。
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	excluded := f.newConversation(t)
	require.NoError(t, f.worker.Backfill(ctx, serviceassignment.BackfillInput{WorkspaceID: f.owner.Workspace.ID, IdentityID: member, ExcludeServiceSessionID: excluded.ID}))
	require.Nil(t, f.currentSession(t, excluded.ConversationID).AssigneeIdentityID)
}

// TestServiceSessionAssignmentFillsCapacity 验证并发分配时候选在等锁期间满员会改选其他成员，队列会话按容量全部分配。
func TestServiceSessionAssignmentFillsCapacity(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	f.setMaxSessions(t, f.owner, 3)
	f.setMaxSessions(t, f.member, 3)
	sessions := []servermodels.ServiceSession{f.currentSession(t, f.conversationID)}
	for range 5 {
		sessions = append(sessions, f.newConversation(t))
	}
	errs := make(chan error, len(sessions))
	for _, session := range sessions {
		go func() {
			errs <- f.worker.Assign(ctx, serviceassignment.AssignInput{WorkspaceID: f.owner.Workspace.ID, ServiceSessionID: session.ID})
		}()
	}
	for range sessions {
		require.NoError(t, <-errs)
	}
	counts := map[string]int{}
	for _, session := range sessions {
		counts[assigneeOf(f.currentSession(t, session.ConversationID))]++
	}
	require.Equal(t, 3, counts[f.owner.WorkspaceIdentity.ID])
	require.Equal(t, 3, counts[f.member.WorkspaceIdentity.ID])
}
