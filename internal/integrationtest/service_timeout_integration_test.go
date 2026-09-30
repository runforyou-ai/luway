//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	"github.com/runforyou-ai/cervi/internal/actions/customerservice"
	"github.com/runforyou-ai/cervi/internal/actions/serviceassignment"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/actions/servicetimeout"
	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// timeoutFixture 保存超时测试共用的客服会话环境、超时执行器与实时通知订阅。
type timeoutFixture struct {
	assignmentFixture
	timeouts *servicetimeout.Worker
	feed     *realtimeFeed
}

// newTimeoutFixture 在自动分配夹具上建立超时执行器并订阅企业实时通知。
func newTimeoutFixture(t *testing.T) timeoutFixture {
	t.Helper()
	f := newAssignmentFixture(t)
	return timeoutFixture{assignmentFixture: f, timeouts: servicetimeout.NewWorker(f.db, newTestTasks(f.db), agentrunaction.NewScheduler(newTestTasks(f.db))), feed: startRealtimeFeed(t, f.owner.Organization.ID)}
}

// age 把周期的客户等待起点与负责人接手时间同时拨回指定分钟数。
func (f timeoutFixture) age(t *testing.T, sessionID string, minutes int) {
	t.Helper()
	if _, err := f.db.NewUpdate().Table("service_sessions").
		Set("awaiting_reply_since = now() - make_interval(mins => ?)", minutes).
		Set("assignee_assigned_at = CASE WHEN assignee_identity_id IS NULL THEN NULL ELSE now() - make_interval(mins => ?) END", minutes).
		Where("id = ?", sessionID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// process 执行单个周期的超时处理任务并返回处理后的周期。
func (f timeoutFixture) process(t *testing.T, session servermodels.ServiceSession) servermodels.ServiceSession {
	t.Helper()
	if err := f.timeouts.Process(context.Background(), servicetimeout.ProcessInput{OrganizationID: f.owner.Organization.ID, ServiceSessionID: session.ID}); err != nil {
		t.Fatal(err)
	}
	return f.currentSession(t, session.ConversationID)
}

// attentions 读取当前已发布的客服处理周期提醒，忽略其他种类的通知。
func (f timeoutFixture) attentions(t *testing.T) []receivedNotification {
	t.Helper()
	got := make([]receivedNotification, 0)
	for {
		message, err := f.feed.subscription.NextMsg(300 * time.Millisecond)
		if errors.Is(err, nats.ErrTimeout) {
			return got
		}
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		// 版本号等字段的类型不影响提醒判断，只读取字符串字段。
		_ = json.Unmarshal(message.Data, &fields)
		if fields["kind"] == string(realtime.KindServiceAttention) {
			got = append(got, receivedNotification{Subject: message.Subject, Kind: fields["kind"], ConversationID: fields["conversationId"], ServiceSessionID: fields["serviceSessionId"], AttentionReason: fields["attentionReason"]})
		}
	}
}

// attention 构造发往指定用户的客服处理周期提醒。
func (f timeoutFixture) attention(userID string, session servermodels.ServiceSession, reason domain.ServiceAttentionReason) receivedNotification {
	return receivedNotification{
		Subject: realtime.Subject(f.feed.namespace, f.feed.organizationID, realtime.AudienceUser, userID),
		Kind:    string(realtime.KindServiceAttention), ConversationID: session.ConversationID, ServiceSessionID: session.ID, AttentionReason: string(reason),
	}
}

// scanEnqueued 执行一次超时扫描并返回指定周期是否有排队或执行中的超时任务。
func (f timeoutFixture) scanEnqueued(t *testing.T, sessionID string) bool {
	t.Helper()
	ctx := context.Background()
	if err := f.timeouts.Scan(ctx, struct{}{}); err != nil {
		t.Fatal(err)
	}
	exists, err := f.db.NewSelect().TableExpr("task_runs").
		Where("action_name = ? AND idempotency_key = ?", servicetimeout.ProcessActionName, "service-timeout:"+sessionID).Exists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

// returnedEvents 读取客服周期的退回队列事件。
func (f timeoutFixture) returnedEvents(t *testing.T, sessionID string) []domain.ServiceSessionReturnedEvent {
	t.Helper()
	var messages []servermodels.Message
	if err := f.db.NewSelect().Model(&messages).
		Where("msg.service_session_id = ? AND msg.system_event_type = ?", sessionID, domain.ConversationSystemEventServiceSessionReturned).
		Order("msg.message_seq").Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := make([]domain.ServiceSessionReturnedEvent, 0, len(messages))
	for _, message := range messages {
		event := domain.ServiceSessionReturnedEvent{}
		if err := json.Unmarshal(message.SystemEventPayload, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

// TestServiceSessionResponseTimeout 验证负责人超时未回复先提醒一次、再回收并分给其他成员，回收后原负责人收到退回提醒；客户继续发消息不重复提醒，回复后结束计时。
func TestServiceSessionResponseTimeout(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	owner, member := f.owner.OrganizationIdentity.ID, f.member.OrganizationIdentity.ID

	session := f.assign(t, f.currentSession(t, f.conversationID), member)
	if assigneeOf(session) != owner {
		t.Fatalf("assignee = %q", assigneeOf(session))
	}
	f.attentions(t)

	// 未到提醒时长不处理。
	f.age(t, session.ID, 4)
	if got := f.process(t, session); got.RemindedAt != nil {
		t.Fatalf("reminded too early: %+v", got)
	}
	f.age(t, session.ID, 6)
	reminded := f.process(t, session)
	if reminded.RemindedAt == nil || assigneeOf(reminded) != owner {
		t.Fatalf("reminded session = %+v", reminded)
	}
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.owner.User.ID, session, domain.ServiceAttentionResponseOverdue)})

	// 同一轮等待只提醒一次，客户继续发消息不开始新一轮。
	if again := f.process(t, session); !again.RemindedAt.Equal(*reminded.RemindedAt) {
		t.Fatalf("reminder repeated: %+v", again)
	}
	if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
		ClientMessageID: uuid.NewV7().String(), Body: "还在吗",
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.process(t, session); got.RemindedAt == nil || got.AwaitingReplySince == nil {
		t.Fatalf("follow-up message reset reminder: %+v", got)
	}
	if got := f.attentions(t); len(got) != 0 {
		t.Fatalf("unexpected attentions = %+v", got)
	}

	// 超过回收时长退回公共队列并分给原负责人以外的成员。
	f.age(t, session.ID, 16)
	reclaimed := f.process(t, session)
	if assigneeOf(reclaimed) != member || reclaimed.RemindedAt != nil || reclaimed.AwaitingReplySince == nil {
		t.Fatalf("reclaimed session = %+v", reclaimed)
	}
	events := f.returnedEvents(t, session.ID)
	if len(events) != 1 || events[0].Reason != domain.ServiceSessionReturnResponseTimeout || events[0].FromIdentityID != owner ||
		events[0].Target.Kind != domain.ServiceSessionTargetPublicQueue {
		t.Fatalf("returned events = %+v", events)
	}
	compareNotifications(t, f.attentions(t), []receivedNotification{
		f.attention(f.owner.User.ID, session, domain.ServiceAttentionReturned),
		f.attention(f.member.User.ID, session, domain.ServiceAttentionAssigned),
	})

	// 新负责人回复后结束等待，不再提醒或回收。
	if _, err := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil).Execute(ctx, f.member, servicesessionaction.ServiceTextMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "您好",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("service_sessions").Set("assignee_assigned_at = now() - interval '30 minutes'").Where("id = ?", session.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.process(t, session); assigneeOf(got) != member || got.AwaitingReplySince != nil || got.RemindedAt != nil {
		t.Fatalf("replied session = %+v", got)
	}
	if got := f.attentions(t); len(got) != 0 {
		t.Fatalf("unexpected attentions = %+v", got)
	}
}

// TestServiceSessionQueueWaitingReminder 验证队列等待超时只提醒一次工作中的客服，且扫描为到期周期投递单条处理任务。
func TestServiceSessionQueueWaitingReminder(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	session := f.newConversation(t)
	if session.AssigneeIdentityID != nil {
		t.Fatalf("queued session = %+v", session)
	}
	f.attentions(t)

	f.age(t, session.ID, 6)
	if err := f.timeouts.Scan(ctx, struct{}{}); err != nil {
		t.Fatal(err)
	}
	exists, err := f.db.NewSelect().TableExpr("task_runs").
		Where("action_name = ? AND idempotency_key = ?", servicetimeout.ProcessActionName, "service-timeout:"+session.ID).Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("timeout task enqueued = %v, %v", exists, err)
	}

	reminded := f.process(t, session)
	if reminded.RemindedAt == nil || reminded.AssigneeIdentityID != nil {
		t.Fatalf("reminded queued session = %+v", reminded)
	}
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.owner.User.ID, session, domain.ServiceAttentionQueueWaiting)})
	f.process(t, session)
	if got := f.attentions(t); len(got) != 0 {
		t.Fatalf("queue reminder repeated: %+v", got)
	}
}

// TestServiceTimeoutsSettings 验证新工作区的超时时长默认值、保存结果与回收时长必须大于提醒时长的校验。
func TestServiceTimeoutsSettings(t *testing.T) {
	t.Parallel()
	f := newAssignmentFixture(t)
	ctx := context.Background()
	loaded, err := customerservice.NewGetServiceTimeoutsQuery(f.db).Execute(ctx, f.owner)
	if err != nil || loaded != (domain.ServiceTimeouts{ResponseReminderMinutes: 5, ResponseReclaimMinutes: 15, QueueReminderMinutes: 5, AIFollowUpMinutes: 10, AICloseMinutes: 30}) {
		t.Fatalf("default timeouts = %+v, %v", loaded, err)
	}
	update := customerservice.NewUpdateServiceTimeoutsAction(f.db)
	_, err = update.Execute(ctx, f.owner, domain.ServiceTimeouts{ResponseReminderMinutes: 10, ResponseReclaimMinutes: 10, QueueReminderMinutes: 0, AIFollowUpMinutes: 0, AICloseMinutes: 5})
	validation, ok := errors.AsType[*common.FieldError](err)
	if !ok || validation.Fields["responseReclaimMinutes"] != customerservice.ValidationReclaimNotAfterRemind ||
		validation.Fields["queueReminderMinutes"] != customerservice.ValidationTimeoutMinutesInvalid ||
		validation.Fields["aiFollowUpMinutes"] != customerservice.ValidationTimeoutMinutesInvalid || len(validation.Fields) != 3 {
		t.Fatalf("validation error = %v", err)
	}
	want := domain.ServiceTimeouts{ResponseReminderMinutes: 3, ResponseReclaimMinutes: 8, QueueReminderMinutes: 2, AIFollowUpMinutes: 4, AICloseMinutes: 12}
	if _, err := update.Execute(ctx, f.owner, want); err != nil {
		t.Fatal(err)
	}
	loaded, err = customerservice.LoadServiceTimeouts(ctx, f.db, f.owner.Organization.ID)
	if err != nil || loaded != want {
		t.Fatalf("saved timeouts = %+v, %v", loaded, err)
	}
	hours, err := customerservice.LoadBusinessHours(ctx, f.db, f.owner.Organization.ID)
	if err != nil || hours.TimeZone != "Asia/Shanghai" {
		t.Fatalf("business hours after timeouts saved = %+v, %v", hours, err)
	}
	// 设置行缺失时保存返回错误。
	if _, err := f.db.NewDelete().Model((*servermodels.CustomerServiceSetting)(nil)).Where("organization_id = ?", f.owner.Organization.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Execute(ctx, f.owner, want); err == nil {
		t.Fatal("saving timeouts without settings row succeeded")
	}
}

// TestServiceSessionReclaimWithoutCandidate 验证只有原负责人可接待时回收后周期留在队列，并投递排除原负责人的分配任务，其他成员恢复工作后由该任务接手。
func TestServiceSessionReclaimWithoutCandidate(t *testing.T) {
	t.Parallel()
	f := newTimeoutFixture(t)
	ctx := context.Background()
	owner := f.owner.OrganizationIdentity.ID
	f.setWorkStatus(t, f.member, domain.WorkStatusAway)
	session := f.assign(t, f.currentSession(t, f.conversationID), "")
	if assigneeOf(session) != owner {
		t.Fatalf("assignee = %q", assigneeOf(session))
	}
	f.attentions(t)

	f.age(t, session.ID, 16)
	reclaimed := f.process(t, session)
	if reclaimed.AssigneeIdentityID != nil || len(f.returnedEvents(t, session.ID)) != 1 {
		t.Fatalf("reclaimed session = %+v", reclaimed)
	}
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.owner.User.ID, session, domain.ServiceAttentionReturned)})
	var runs []servermodels.TaskRun
	if err := f.db.NewSelect().Model(&runs).
		Where("tr.action_name = ? AND tr.payload->>'serviceSessionId' = ? AND tr.payload->>'excludeIdentityId' = ?", serviceassignment.AssignActionName, session.ID, owner).
		Scan(ctx); err != nil || len(runs) != 1 {
		t.Fatalf("exclusive assign tasks = %d, %v", len(runs), err)
	}
	input := serviceassignment.AssignInput{}
	if err := json.Unmarshal(runs[0].Payload, &input); err != nil {
		t.Fatal(err)
	}
	if again := f.assign(t, reclaimed, input.ExcludeIdentityID); again.AssigneeIdentityID != nil {
		t.Fatalf("assigned back to previous assignee: %+v", again)
	}
	f.setWorkStatus(t, f.member, domain.WorkStatusWorking)
	if assigned := f.assign(t, reclaimed, input.ExcludeIdentityID); assigneeOf(assigned) != f.member.OrganizationIdentity.ID {
		t.Fatalf("assigned after member returns = %+v", assigned)
	}
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
	if got := f.process(t, session); got.RemindedAt != nil {
		t.Fatalf("reminder recorded without recipients: %+v", got)
	}
	if got := f.attentions(t); len(got) != 0 {
		t.Fatalf("unexpected attentions = %+v", got)
	}
	if f.scanEnqueued(t, session.ID) {
		t.Fatal("scan selected queue reminder without recipients")
	}

	// 周期转入只有第二成员的团队队列，两人都工作时只提醒团队成员。
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "超时提醒团队"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := teamaction.NewAddMembersAction(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, team.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: f.member.OrganizationIdentity.ID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).Where("id = ?", session.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	f.setWorkStatus(t, f.owner, domain.WorkStatusWorking)
	if f.scanEnqueued(t, session.ID) {
		t.Fatal("scan selected team queue reminder without working team members")
	}
	f.setWorkStatus(t, f.member, domain.WorkStatusWorking)
	if !f.scanEnqueued(t, session.ID) {
		t.Fatal("scan skipped team queue reminder with working team member")
	}
	f.attentions(t)
	if got := f.process(t, session); got.RemindedAt == nil {
		t.Fatalf("team queue reminder not recorded: %+v", got)
	}
	compareNotifications(t, f.attentions(t), []receivedNotification{f.attention(f.member.User.ID, session, domain.ServiceAttentionQueueWaiting)})
}
