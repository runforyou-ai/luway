//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
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
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
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
	return assignmentFixture{customerReadFixture: f, worker: serviceassignment.NewWorker(f.db)}
}

// newConversation 以新访客身份进线并返回客户会话当前客服周期。
func (f assignmentFixture) newConversation(t *testing.T) servermodels.ServiceSession {
	t.Helper()
	result, err := f.receive.Execute(context.Background(), customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:" + uuid.NewV7().String()[:8] + "0123456789abcdef01234567", ClientMessageID: uuid.NewV7().String(), Body: "需要帮助",
	})
	if err != nil {
		t.Fatal(err)
	}
	return f.currentSession(t, result.Conversation.ID)
}

// currentSession 读取客户会话当前客服周期。
func (f assignmentFixture) currentSession(t *testing.T, conversationID string) servermodels.ServiceSession {
	t.Helper()
	session := servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(&session).
		Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.organization_id = ss.organization_id").
		Where("svc.organization_id = ? AND svc.conversation_id = ?", f.owner.Organization.ID, conversationID).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session
}

// assign 执行单个客服周期的分配任务并返回分配后的周期。
func (f assignmentFixture) assign(t *testing.T, session servermodels.ServiceSession, exclude string) servermodels.ServiceSession {
	t.Helper()
	if err := f.worker.Assign(context.Background(), serviceassignment.AssignInput{OrganizationID: f.owner.Organization.ID, ServiceSessionID: session.ID, ExcludeIdentityID: exclude}); err != nil {
		t.Fatal(err)
	}
	return f.currentSession(t, session.ConversationID)
}

// setMaxSessions 直接修改成员最大接待量。
func (f assignmentFixture) setMaxSessions(t *testing.T, identity *servermodels.Identity, max int) {
	t.Helper()
	if _, err := f.db.NewUpdate().Table("users").Set("max_service_sessions = ?", max).Where("id = ?", identity.User.ID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// setWorkStatus 通过工作状态操作切换成员工作状态。
func (f assignmentFixture) setWorkStatus(t *testing.T, identity *servermodels.Identity, status domain.WorkStatus) {
	t.Helper()
	if _, err := useraction.NewUpdateWorkStatusAction(f.db, newTestTasks(f.db)).Execute(context.Background(), identity, useraction.WorkStatusInput{WorkStatus: status}); err != nil {
		t.Fatal(err)
	}
}

// assignedEvents 读取客服周期的自动分配事件。
func (f assignmentFixture) assignedEvents(t *testing.T, sessionID string) []domain.ServiceSessionAssignedEvent {
	t.Helper()
	var messages []servermodels.Message
	if err := f.db.NewSelect().Model(&messages).
		Where("msg.service_session_id = ? AND msg.system_event_type = ?", sessionID, domain.ConversationSystemEventServiceSessionAssigned).
		Order("msg.message_seq").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := make([]domain.ServiceSessionAssignedEvent, 0, len(messages))
	for _, message := range messages {
		event := domain.ServiceSessionAssignedEvent{}
		if err := json.Unmarshal(message.SystemEventPayload, &event); err != nil {
			t.Fatal(err)
		}
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
	owner, member := f.owner.OrganizationIdentity.ID, f.member.OrganizationIdentity.ID

	// 固定夹具首条会话已在公共队列中，入站写入客户等待起点。
	first := f.currentSession(t, f.conversationID)
	if first.AssigneeIdentityID != nil || first.AwaitingReplySince == nil {
		t.Fatalf("queued session = %+v", first)
	}
	first = f.assign(t, first, "")
	firstAssignee := assigneeOf(first)
	if firstAssignee != owner && firstAssignee != member {
		t.Fatalf("first assignee = %q", firstAssignee)
	}
	if first.AssigneeAssignedAt == nil || first.AssignedAt == nil || first.TeamID != nil {
		t.Fatalf("assigned session fields = %+v", first)
	}
	events := f.assignedEvents(t, first.ID)
	if len(events) != 1 || events[0].Target.IdentityID == nil || *events[0].Target.IdentityID != firstAssignee || events[0].Source.Kind != domain.ServiceSessionTargetPublicQueue {
		t.Fatalf("assigned events = %+v", events)
	}
	// 重复执行分配任务不改变结果。
	if again := f.assign(t, first, ""); assigneeOf(again) != firstAssignee || len(f.assignedEvents(t, first.ID)) != 1 {
		t.Fatalf("repeated assignment = %+v", again)
	}

	// 接待量较少的成员优先。
	second := f.assign(t, f.newConversation(t), "")
	if assigneeOf(second) == firstAssignee || assigneeOf(second) == "" {
		t.Fatalf("second assignee = %q, first = %q", assigneeOf(second), firstAssignee)
	}

	// 成员满员后只分配给未满员成员。
	f.setMaxSessions(t, f.member, 1)
	if third := f.assign(t, f.newConversation(t), ""); assigneeOf(third) != owner {
		t.Fatalf("third assignee = %q, want owner", assigneeOf(third))
	}

	// 群主休息中且成员满员时留在队列。
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	waiting := f.assign(t, f.newConversation(t), "")
	if waiting.AssigneeIdentityID != nil {
		t.Fatalf("waiting session assigned to %q", assigneeOf(waiting))
	}

	// 关闭本人负责的周期后补分配最早等待的队列会话。
	memberSession := first
	if firstAssignee != member {
		memberSession = second
	}
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), newTestTasks(f.db))
	if _, err := closeSession.Execute(ctx, f.member, memberSession.ConversationID); err != nil {
		t.Fatal(err)
	}
	if closed := f.currentSession(t, memberSession.ConversationID); closed.AwaitingReplySince != nil {
		t.Fatalf("closed session awaiting = %v", closed.AwaitingReplySince)
	}
	if err := f.worker.Backfill(ctx, serviceassignment.BackfillInput{OrganizationID: f.owner.Organization.ID, IdentityID: member}); err != nil {
		t.Fatal(err)
	}
	if backfilled := f.currentSession(t, waiting.ConversationID); assigneeOf(backfilled) != member {
		t.Fatalf("backfilled assignee = %q", assigneeOf(backfilled))
	}

	// 群主切换回工作中后补分配，直到没有等待中的会话。
	extra := f.newConversation(t)
	f.setWorkStatus(t, f.owner, domain.WorkStatusWorking)
	if err := f.worker.Backfill(ctx, serviceassignment.BackfillInput{OrganizationID: f.owner.Organization.ID, IdentityID: owner}); err != nil {
		t.Fatal(err)
	}
	if backfilled := f.currentSession(t, extra.ConversationID); assigneeOf(backfilled) != owner {
		t.Fatalf("owner backfill assignee = %q", assigneeOf(backfilled))
	}
	var lastAssigned servermodels.User
	if err := f.db.NewSelect().Model(&lastAssigned).Where("u.id = ?", f.owner.User.ID).Scan(ctx); err != nil || lastAssigned.LastServiceAssignedAt == nil {
		t.Fatalf("owner last assigned = %+v, err = %v", lastAssigned.LastServiceAssignedAt, err)
	}
}

// TestServiceSessionAssignmentScope 验证团队队列只分配给团队成员、排除原负责人，以及成员回复结束客户等待。
func TestServiceSessionAssignmentScope(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	owner, member := f.owner.OrganizationIdentity.ID, f.member.OrganizationIdentity.ID
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "售前组"})
	if err != nil {
		t.Fatal(err)
	}
	addMember := func() {
		t.Helper()
		if _, err := teamaction.NewAddMembersAction(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, team.ID, []teamaction.MemberIdentity{
			{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: member},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 统计为该成员投递的补分配任务数。
	backfills := func() int {
		t.Helper()
		count, err := f.db.NewSelect().TableExpr("task_runs").
			Where("action_name = ? AND payload->>'identityId' = ?", serviceassignment.BackfillActionName, member).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := backfills()
	addMember()
	// 已在团队中的成员再次加入时不重复补分配。
	addMember()
	if added := backfills() - before; added != 1 {
		t.Fatalf("team member backfills = %d", added)
	}

	// 群主接待量更少，但团队队列只分配给团队成员。
	if assigned := f.assign(t, f.currentSession(t, f.conversationID), owner); assigneeOf(assigned) != member {
		t.Fatalf("excluded owner assignee = %q", assigneeOf(assigned))
	}
	teamSession := f.newConversation(t)
	if _, err := f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).Where("id = ?", teamSession.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	teamSession.TeamID = &team.ID
	if assigned := f.assign(t, teamSession, ""); assigneeOf(assigned) != member {
		t.Fatalf("team queue assignee = %q", assigneeOf(assigned))
	}
	events := f.assignedEvents(t, teamSession.ID)
	if len(events) != 1 || events[0].Source.Kind != domain.ServiceSessionTargetTeam || events[0].Source.TeamName == nil || *events[0].Source.TeamName != team.Name {
		t.Fatalf("team assigned events = %+v", events)
	}

	// 成员对客回复结束客户等待。
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, newTestTasks(f.db))
	if _, err := send.Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: teamSession.ConversationID, ClientMessageID: uuid.NewV7().String(), Body: "您好", Visibility: domain.MessageVisibilityShared,
	}); err != nil {
		t.Fatal(err)
	}
	if replied := f.currentSession(t, teamSession.ConversationID); replied.AwaitingReplySince != nil {
		t.Fatalf("replied awaiting = %v", replied.AwaitingReplySince)
	}

	// 转回公共队列清空负责人与接手时间，分配任务排除原负责人。
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, newGroupAgentCoordinator(f.db), agentrunaction.NewScheduler(newTestTasks(f.db)), newTestTasks(f.db))
	if _, err := transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: teamSession.ConversationID, TargetKind: domain.ServiceSessionTargetPublicQueue,
	}); err != nil {
		t.Fatal(err)
	}
	queued := f.currentSession(t, teamSession.ConversationID)
	if queued.AssigneeIdentityID != nil || queued.AssigneeAssignedAt != nil || queued.TeamID != nil {
		t.Fatalf("transferred session = %+v", queued)
	}
	if assigned := f.assign(t, queued, member); assigneeOf(assigned) != owner {
		t.Fatalf("reassigned = %q, want owner", assigneeOf(assigned))
	}
}

// TestServiceSessionRouteSkipsInactiveMember 验证渠道指定成员不在工作中时入站改走备用路由。
func TestServiceSessionRouteSkipsInactiveMember(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	member := f.member.OrganizationIdentity.ID
	if _, err := channelaction.NewUpdateMessageChannelAction(f.db).ExecuteReception(ctx, f.owner, f.channelID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: member},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	}); err != nil {
		t.Fatal(err)
	}
	if routed := f.newConversation(t); assigneeOf(routed) != member || routed.AssigneeAssignedAt == nil {
		t.Fatalf("working member route = %+v", routed)
	}
	f.setWorkStatus(t, f.member, domain.WorkStatusOffDuty)
	if routed := f.newConversation(t); routed.AssigneeIdentityID != nil {
		t.Fatalf("off duty member route assignee = %q", assigneeOf(routed))
	}
}

// TestServiceSessionAssignmentConcurrency 验证并发分配按接待量均分，并发补分配在周期被抢走后继续补下一条，补分配排除指定周期。
func TestServiceSessionAssignmentConcurrency(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	owner, member := f.owner.OrganizationIdentity.ID, f.member.OrganizationIdentity.ID
	f.assign(t, f.currentSession(t, f.conversationID), "")
	f.assign(t, f.newConversation(t), "")

	// 两条会话同时分配时各分给一名成员。
	for round := range 3 {
		sessions := []servermodels.ServiceSession{f.newConversation(t), f.newConversation(t)}
		errs := make(chan error, len(sessions))
		for _, session := range sessions {
			go func() {
				errs <- f.worker.Assign(ctx, serviceassignment.AssignInput{OrganizationID: f.owner.Organization.ID, ServiceSessionID: session.ID})
			}()
		}
		for range sessions {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		first, second := assigneeOf(f.currentSession(t, sessions[0].ConversationID)), assigneeOf(f.currentSession(t, sessions[1].ConversationID))
		if first == "" || second == "" || first == second {
			t.Fatalf("round %d concurrent assignees = %q, %q", round, first, second)
		}
	}

	// 两名成员同时补分配时，队列中的会话全部分配。
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	queued := []servermodels.ServiceSession{f.newConversation(t), f.newConversation(t), f.newConversation(t)}
	if _, err := f.db.NewUpdate().Table("organization_identities").Set("work_status = ?", domain.WorkStatusWorking).
		Where("id IN (?)", bun.In([]string{owner, member})).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for _, identityID := range []string{owner, member} {
		go func() {
			errs <- f.worker.Backfill(ctx, serviceassignment.BackfillInput{OrganizationID: f.owner.Organization.ID, IdentityID: identityID})
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range queued {
		if assigneeOf(f.currentSession(t, session.ConversationID)) == "" {
			t.Fatalf("session %s left in queue after concurrent backfill", session.ID)
		}
	}

	// 补分配跳过排除的周期。
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	excluded := f.newConversation(t)
	if err := f.worker.Backfill(ctx, serviceassignment.BackfillInput{OrganizationID: f.owner.Organization.ID, IdentityID: member, ExcludeServiceSessionID: excluded.ID}); err != nil {
		t.Fatal(err)
	}
	if assigned := f.currentSession(t, excluded.ConversationID); assigned.AssigneeIdentityID != nil {
		t.Fatalf("excluded session assigned to %q", assigneeOf(assigned))
	}
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
			errs <- f.worker.Assign(ctx, serviceassignment.AssignInput{OrganizationID: f.owner.Organization.ID, ServiceSessionID: session.ID})
		}()
	}
	for range sessions {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]int{}
	for _, session := range sessions {
		counts[assigneeOf(f.currentSession(t, session.ConversationID))]++
	}
	if counts[f.owner.OrganizationIdentity.ID] != 3 || counts[f.member.OrganizationIdentity.ID] != 3 {
		t.Fatalf("assignment counts = %v", counts)
	}
}
