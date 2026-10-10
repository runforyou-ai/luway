//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	servertest "github.com/runforyou-ai/luway/internal/servertest"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicetimeout"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
)

// timeoutFixture 保存超时测试共用的客服会话环境与超时执行器。
type timeoutFixture struct {
	assignmentFixture
	timeouts *servicetimeout.Worker
}

// newTimeoutFixture 在自动分配夹具上建立超时执行器。
func newTimeoutFixture(t *testing.T) timeoutFixture {
	t.Helper()
	f := newAssignmentFixture(t)
	return timeoutFixture{assignmentFixture: f, timeouts: servicetimeout.NewWorker(f.db, testEnqueuer, agentrunaction.NewScheduler(testEnqueuer))}
}

// age 把周期的客户等待起点与负责人接手时间同时拨回指定分钟数。
func (f timeoutFixture) age(t *testing.T, sessionID string, minutes int) {
	t.Helper()
	_, err := f.db.NewUpdate().Table("service_sessions").
		Set("awaiting_reply_since = now() - make_interval(mins => ?)", minutes).
		Set("assignee_assigned_at = CASE WHEN assignee_identity_id IS NULL THEN NULL ELSE now() - make_interval(mins => ?) END", minutes).
		Where("id = ?", sessionID).Exec(context.Background())
	require.NoError(t, err)
}

// process 执行单个周期的超时处理任务并返回处理后的周期。
func (f timeoutFixture) process(t *testing.T, session servermodels.ServiceSession) servermodels.ServiceSession {
	t.Helper()
	require.NoError(t, f.timeouts.Process(context.Background(), servicetimeout.ProcessInput{WorkspaceID: f.owner.Workspace.ID, ServiceSessionID: session.ID}))
	return f.currentSession(t, session.ConversationID)
}

// attentions 读取并消费已登记的客服处理周期提醒通知任务，按接收成员的用户受众标识。
func (f timeoutFixture) attentions(t *testing.T) []receivedNotification {
	t.Helper()
	runs := testEnqueuer.Take(notificationtask.ServiceAttentionActionName, f.owner.Workspace.ID)
	got := make([]receivedNotification, 0, len(runs))
	for _, run := range runs {
		input := servertest.TaskPayload[notificationtask.ServiceAttentionInput](t, run)
		got = append(got, f.attention(input.UserID, servermodels.ServiceSession{ID: input.ServiceSessionID, ConversationID: input.ConversationID}, input.Reason))
	}
	return got
}

// attention 构造发往指定用户的客服处理周期提醒。
func (f timeoutFixture) attention(userID string, session servermodels.ServiceSession, reason domain.ServiceAttentionReason) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.owner.Workspace.ID, realtime.AudienceUser, userID),
		Kind:    notificationtask.ServiceAttentionActionName, ConversationID: session.ConversationID, ServiceSessionID: session.ID, AttentionReason: string(reason),
	}
}

// scanEnqueued 执行一次超时扫描并返回指定周期是否有未执行的超时任务。
func (f timeoutFixture) scanEnqueued(t *testing.T, sessionID string) bool {
	t.Helper()
	require.NoError(t, f.timeouts.Scan(context.Background(), struct{}{}))
	return timeoutTaskQueued(f.owner.Workspace.ID, sessionID)
}

// timeoutTaskQueued 判断共用登记器中是否有指定周期未执行的超时任务。
func timeoutTaskQueued(workspaceID, sessionID string) bool {
	for _, task := range testEnqueuer.Queued(servicetimeout.ProcessActionName, workspaceID) {
		if task.Options.IdempotencyKey == "service-timeout:"+sessionID {
			return true
		}
	}
	return false
}

// returnedEvents 读取客服周期的退回队列事件。
func (f timeoutFixture) returnedEvents(t *testing.T, sessionID string) []domain.ServiceSessionReturnedEvent {
	t.Helper()
	var messages []servermodels.Message
	require.NoError(t, f.db.NewSelect().Model(&messages).
		Where("msg.service_session_id = ? AND msg.system_event_type = ?", sessionID, domain.ConversationSystemEventServiceSessionReturned).
		Order("msg.message_seq").Scan(context.Background()))
	events := make([]domain.ServiceSessionReturnedEvent, 0, len(messages))
	for _, message := range messages {
		event := domain.ServiceSessionReturnedEvent{}
		require.NoError(t, json.Unmarshal(message.SystemEventPayload, &event))
		events = append(events, event)
	}
	return events
}

// TestServiceSessionResponseTimeout 验证负责人超时未回复先提醒一次、再回收并分给其他成员，回收后原负责人收到退回提醒；客户继续发消息不重复提醒，回复后结束计时。
func TestServiceSessionResponseTimeout(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	owner, member := f.owner.WorkspaceIdentity.ID, f.member.WorkspaceIdentity.ID

	session := f.assign(t, f.currentSession(t, f.conversationID), member)
	require.Equal(t, owner, assigneeOf(session), "assignee")
	f.attentions(t)

	// 未到提醒时长不处理。
	f.age(t, session.ID, 4)
	require.Nil(t, f.process(t, session).RemindedAt, "reminded too early")
	f.age(t, session.ID, 6)
	reminded := f.process(t, session)
	require.NotNil(t, reminded.RemindedAt, "reminded session")
	require.Equal(t, owner, assigneeOf(reminded), "reminded session")
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.owner.User.ID, session, domain.ServiceAttentionResponseOverdue)})

	// 同一轮等待只提醒一次，客户继续发消息不开始新一轮。
	again := f.process(t, session)
	require.True(t, again.RemindedAt.Equal(*reminded.RemindedAt), "reminder repeated: %+v", again)
	_, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "还在吗",
	})
	require.NoError(t, err)
	followUp := f.process(t, session)
	require.NotNil(t, followUp.RemindedAt, "follow-up message reset reminder")
	require.NotNil(t, followUp.AwaitingReplySince, "follow-up message reset reminder")
	require.Empty(t, f.attentions(t), "unexpected attentions")

	// 超过回收时长退回公共队列并分给原负责人以外的成员。
	f.age(t, session.ID, 16)
	reclaimed := f.process(t, session)
	require.Equal(t, member, assigneeOf(reclaimed), "reclaimed session")
	require.Nil(t, reclaimed.RemindedAt, "reclaimed session")
	require.NotNil(t, reclaimed.AwaitingReplySince, "reclaimed session")
	events := f.returnedEvents(t, session.ID)
	require.Len(t, events, 1, "returned events")
	require.Equal(t, domain.ServiceSessionReturnResponseTimeout, events[0].Reason, "returned events")
	require.Equal(t, owner, events[0].FromIdentityID, "returned events")
	require.Equal(t, domain.ServiceSessionTargetPublicQueue, events[0].Target.Kind, "returned events")
	compareNotifications(t, f.attentions(t), []receivedNotification{
		f.attention(f.owner.User.ID, session, domain.ServiceAttentionReturned),
		f.attention(f.member.User.ID, session, domain.ServiceAttentionAssigned),
	})

	// 新负责人回复后结束等待，不再提醒或回收。
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "您好",
	})
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Table("service_sessions").Set("assignee_assigned_at = now() - interval '30 minutes'").Where("id = ?", session.ID).Exec(ctx)
	require.NoError(t, err)
	replied := f.process(t, session)
	require.Equal(t, member, assigneeOf(replied), "replied session")
	require.Nil(t, replied.AwaitingReplySince, "replied session")
	require.Nil(t, replied.RemindedAt, "replied session")
	require.Empty(t, f.attentions(t), "unexpected attentions")
}

// TestServiceSessionQueueWaitingReminder 验证队列等待超时只提醒一次工作中的客服，且扫描为到期周期投递单条处理任务。
func TestServiceSessionQueueWaitingReminder(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	session := f.newConversation(t)
	require.Nil(t, session.AssigneeIdentityID, "queued session")
	f.attentions(t)

	f.age(t, session.ID, 6)
	require.NoError(t, f.timeouts.Scan(ctx, struct{}{}))
	require.True(t, timeoutTaskQueued(f.owner.Workspace.ID, session.ID), "timeout task enqueued")

	reminded := f.process(t, session)
	require.NotNil(t, reminded.RemindedAt, "reminded queued session")
	require.Nil(t, reminded.AssigneeIdentityID, "reminded queued session")
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.owner.User.ID, session, domain.ServiceAttentionQueueWaiting)})
	f.process(t, session)
	require.Empty(t, f.attentions(t), "queue reminder repeated")
}

// TestServiceSessionReclaimWithoutCandidate 验证只有原负责人可接待时回收后周期留在队列，并投递排除原负责人的分配任务，其他成员恢复工作后由该任务接手。
func TestServiceSessionReclaimWithoutCandidate(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	owner := f.owner.WorkspaceIdentity.ID
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	session := f.assign(t, f.currentSession(t, f.conversationID), "")
	require.Equal(t, owner, assigneeOf(session), "assignee")
	f.attentions(t)

	f.age(t, session.ID, 16)
	reclaimed := f.process(t, session)
	require.Nil(t, reclaimed.AssigneeIdentityID, "reclaimed session")
	require.Len(t, f.returnedEvents(t, session.ID), 1, "reclaimed session")
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.owner.User.ID, session, domain.ServiceAttentionReturned)})
	runs := servertest.QueuedInputs(t, testEnqueuer, serviceassignment.AssignActionName, func(input serviceassignment.AssignInput) bool {
		return input.ServiceSessionID == session.ID && input.ExcludeIdentityID == owner
	})
	require.Len(t, runs, 1, "exclusive assign tasks")
	input := runs[0]
	require.Nil(t, f.assign(t, reclaimed, input.ExcludeIdentityID).AssigneeIdentityID, "assigned back to previous assignee")
	f.setWorkStatus(t, f.member, domain.WorkStatusWorking)
	require.Equal(t, f.member.WorkspaceIdentity.ID, assigneeOf(f.assign(t, reclaimed, input.ExcludeIdentityID)), "assigned after member returns")
}

// TestServiceSessionQueueReminderRecipients 验证队列没有工作中的客服时不消耗本轮提醒，团队队列只提醒团队中工作中的客服。
func TestServiceSessionQueueReminderRecipients(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	f.setWorkStatus(t, f.owner, domain.WorkStatusAway)
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	session := f.newConversation(t)
	f.attentions(t)

	f.age(t, session.ID, 6)
	require.Nil(t, f.process(t, session).RemindedAt, "reminder recorded without recipients")
	require.Empty(t, f.attentions(t), "unexpected attentions")
	require.False(t, f.scanEnqueued(t, session.ID), "scan selected queue reminder without recipients")

	// 周期转入只有第二成员的团队队列，两人都工作时只提醒团队成员。
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "超时提醒团队"})
	require.NoError(t, err)
	_, err = teamaction.NewAddMembersAction(f.db, testEnqueuer).Execute(ctx, f.owner, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.WorkspaceIdentityTypeUser, IdentityID: f.member.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	_, err = f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).Where("id = ?", session.ID).Exec(ctx)
	require.NoError(t, err)
	f.setWorkStatus(t, f.owner, domain.WorkStatusWorking)
	require.False(t, f.scanEnqueued(t, session.ID), "scan selected team queue reminder without working team members")
	f.setWorkStatus(t, f.member, domain.WorkStatusWorking)
	require.True(t, f.scanEnqueued(t, session.ID), "scan skipped team queue reminder with working team member")
	f.attentions(t)
	require.NotNil(t, f.process(t, session).RemindedAt, "team queue reminder not recorded")
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.member.User.ID, session, domain.ServiceAttentionQueueWaiting)})
}

// TestServiceTimeoutScanSelection 验证超时扫描按五种时限只为到期的周期投递处理任务：负责人提醒与回收、有可提醒客服的队列提醒、AI 跟进与 AI 关单。
func TestServiceTimeoutScanSelection(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	settings := domain.ServiceTimeouts{ResponseReminderMinutes: 5, ResponseReclaimMinutes: 15, QueueReminderMinutes: 5, AIFollowUpMinutes: 10, AICloseMinutes: 30}

	// 真人负责与队列中的周期按指定分钟数拨回等待起点，reminded 为 true 时记为本轮已提醒。
	queued := func(minutes int) string {
		session := f.newConversation(t)
		f.age(t, session.ID, minutes)
		return session.ID
	}
	assigned := func(minutes int, reminded bool) string {
		session := f.assign(t, f.newConversation(t), "")
		require.NotNil(t, session.AssigneeIdentityID, "assigned session")
		f.age(t, session.ID, minutes)
		if reminded {
			_, err := f.db.NewUpdate().Table("service_sessions").Set("reminded_at = now()").Where("id = ?", session.ID).Exec(ctx)
			require.NoError(t, err)
		}
		return session.ID
	}
	remindDue, reclaimDue, queueDue := assigned(settings.ResponseReminderMinutes, false), assigned(settings.ResponseReclaimMinutes, true), queued(settings.QueueReminderMinutes)
	// 各时限差 1 分钟到期的周期不投递。
	notDue := []string{
		assigned(settings.ResponseReminderMinutes-1, false),
		assigned(settings.ResponseReclaimMinutes-1, true),
		queued(settings.QueueReminderMinutes - 1),
	}
	// 没有成员的团队队列没有可提醒客服，到期也不投递。
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "空团队"})
	require.NoError(t, err)
	queueWithoutRecipients := queued(settings.QueueReminderMinutes * 2)
	_, err = f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).Where("id = ?", queueWithoutRecipients).Exec(ctx)
	require.NoError(t, err)
	notDue = append(notDue, queueWithoutRecipients)

	identity, providerID, modelID := newAIWorkspaceIn(t, f.db)
	tasks := servertest.NewTasks()
	disableAutoAssignment(t, f.db, identity.Workspace.ID)
	ai := resolutionFixture{handoffFixture: handoffFixture{db: f.db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}, settings: settings}
	agent := ai.newAgent(t, "超时扫描客服")
	channelID := ai.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	// AI 回答一次后按指定分钟数拨回计时起点，confirm 为 true 时回答记为确认请求。
	answered := func(minutes int, confirm bool) string {
		input := visitorInput(channelID, "")
		first := ai.receive(t, &input, "营业时间是几点")
		decision := agentcontract.TerminalDecision{}
		if confirm {
			decision = agentcontract.TerminalDecision{Kind: domain.AgentRunOutcomeAskCustomer, Purpose: domain.AgentAskCustomerPurposeConfirmResolution}
		}
		run := ai.executeQueuedRun(t, first.Conversation.ID, resolutionRuntime("我们每天 9 点营业", decision, nil, nil))
		ai.age(t, run.ScopeID, minutes)
		return run.ScopeID
	}
	followUpDue, closeDue := answered(settings.AIFollowUpMinutes, false), answered(settings.AICloseMinutes, true)
	notDue = append(notDue, answered(settings.AIFollowUpMinutes-1, false), answered(settings.AICloseMinutes-1, true))
	due := []string{remindDue, reclaimDue, queueDue, followUpDue, closeDue}
	// 单次扫描名额由全部工作区共享，重复扫描直到本工作区到期周期全部投递。
	var got []string
	for range 10 {
		require.NoError(t, f.timeouts.Scan(ctx, struct{}{}))
		got = got[:0]
		for _, workspaceID := range []string{f.owner.Workspace.ID, identity.Workspace.ID} {
			for _, task := range testEnqueuer.Queued(servicetimeout.ProcessActionName, workspaceID) {
				got = append(got, servertest.TaskPayload[servicetimeout.ProcessInput](t, task).ServiceSessionID)
			}
		}
		if !slices.ContainsFunc(due, func(id string) bool { return !slices.Contains(got, id) }) {
			break
		}
	}
	require.Subset(t, got, due, "due timeout sessions not enqueued")
	for _, id := range notDue {
		require.NotContains(t, got, id, "not-due timeout session enqueued")
	}
}
