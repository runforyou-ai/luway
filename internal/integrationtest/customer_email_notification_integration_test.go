//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/uptrace/bun"
)

// handoffEmailRequest 是渠道语言 zh-CN 下转人工话术追加的留邮箱请求。
const handoffEmailRequest = "如需离开，可以在这里留下邮箱，客服回复后我们会发邮件通知您。"

// recordingMailSender 记录发出的邮件，fail 非空时返回该错误。
type recordingMailSender struct {
	mu       sync.Mutex
	messages []mail.Message
	fail     error
}

// Send 记录邮件或返回预设错误。
func (s *recordingMailSender) Send(_ context.Context, message mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.messages = append(s.messages, message)
	return nil
}

// sent 返回已记录的邮件。
func (s *recordingMailSender) sent() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.messages...)
}

// TestCustomerEmailNotification 验证转人工后收集访客邮箱、访客未读时合并发送邮件通知，以及凭回访令牌回到原会话。
func TestCustomerEmailNotification(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := newTestTasks(db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Organization.ID)
	sender := &recordingMailSender{}
	agent := f.newAgent(t, "邮件通知客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	receive := func(body string) customerchataction.ReceiveWebsiteCustomerMessageResult {
		t.Helper()
		input.ClientMessageID, input.Body = uuid.NewV7().String(), body
		result, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), newTestTasks(db), sender).Execute(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		input.ConversationID = &result.Conversation.ID
		return result
	}

	// 转人工进入公共队列时，话术末尾请访客留下邮箱。
	first := receive("我要退款")
	conversationID := first.Conversation.ID
	run := f.queuedRun(t, conversationID)
	if err := agentrunaction.NewExecuteAction(db, tasks, handoffRuntime("客户要求退款", nil), testAttachmentReader(db), nil, sender).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if notice := handoffNotice(t, db, "agent:"+run.ID); notice != handoffQueuedNotice+"\n\n"+handoffEmailRequest {
		t.Fatalf("handoff notice = %q", notice)
	}

	// 联系方式达到数量上限时邮箱未写入，也不追加留邮箱事件。
	var contactID string
	if err := db.NewSelect().TableExpr("contact_channel_identities AS cci").Column("cci.contact_id").
		Where("cci.organization_id = ? AND cci.external_id = ?", identity.Organization.ID, input.ExternalID).Scan(ctx, &contactID); err != nil {
		t.Fatal(err)
	}
	for index := range domain.ContactMethodsMaxCount {
		phone := "+86 138 0000 " + strings.Repeat("0", 3-len(strconv.Itoa(index))) + strconv.Itoa(index)
		if _, err := db.NewInsert().Model(&servermodels.ContactMethod{
			ID: uuid.NewV7().String(), OrganizationID: identity.Organization.ID, ContactID: contactID,
			Type: string(domain.ContactMethodTypePhone), Value: phone, NormalizedValue: phone, IsPrimary: index == 0,
		}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	receive("我的邮箱是 limit@example.com")
	if count, err := db.NewSelect().Model((*servermodels.Message)(nil)).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionEmailCollected).
		Count(ctx); err != nil || count != 0 {
		t.Fatalf("email collected events at method limit = %d, error = %v", count, err)
	}
	if _, err := db.NewDelete().Model((*servermodels.ContactMethod)(nil)).Where("contact_id = ?", contactID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	// 转人工后访客消息中的唯一邮箱写入联系人，并向访客展示留邮箱事件，客服侧按参与方变化重读客户资料；已有邮箱时不重复收集。
	feed := startRealtimeFeed(t, identity.Organization.ID)
	receive("我的邮箱是 Visitor@Example.com。")
	feed.expectCustomerInboxChanges(t, conversationID, domain.ConversationChangeTimeline|domain.ConversationChangeService|domain.ConversationChangeParticipants)
	receive("备用邮箱 other@example.com")
	var emails []string
	if err := db.NewSelect().TableExpr("contact_methods AS cm").ColumnExpr("cm.normalized_value").
		Join("JOIN contact_channel_identities AS cci ON cci.contact_id = cm.contact_id").
		Where("cci.external_id = ? AND cm.type = ? AND cm.is_primary", input.ExternalID, domain.ContactMethodTypeEmail).
		Scan(ctx, &emails); err != nil || len(emails) != 1 || emails[0] != "visitor@example.com" {
		t.Fatalf("contact emails = %v, error = %v", emails, err)
	}
	history, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channelID, ExternalID: input.ExternalID, ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	collected := 0
	for _, message := range history.Messages {
		if message.Event != nil && message.Event.Type == customerchataction.VisitorEventEmailCollected {
			collected++
			if message.Event.Email != "visitor@example.com" {
				t.Fatalf("email collected event = %+v", message.Event)
			}
		}
	}
	if collected != 1 {
		t.Fatalf("email collected events = %d", collected)
	}

	// 真人连续回复只由第一条起算一次检查时间；扫描到期会话时同一会话只投递一个任务。
	send := servicesessionaction.NewSendServiceTextMessageAction(db, nil)
	reply := func(body string) conversationaction.ConversationMessage {
		t.Helper()
		message, err := send.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body})
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	firstReply := reply("已收到您的退款申请")
	scheduled := customerPositions(t, db, conversationID).ContactNotifyDueAt
	secondReply := reply("退款将在三个工作日内到账")
	if scheduled == nil || scheduled.Sub(firstReply.OriginatedAt.Add(3*time.Minute)).Abs() > time.Millisecond || !customerPositions(t, db, conversationID).ContactNotifyDueAt.Equal(*scheduled) {
		t.Fatalf("notification due = %v, first reply = %v", scheduled, firstReply.OriginatedAt)
	}
	scanTasks := newTestTasks(db)
	if err := scanTasks.Registry().RegisterJSON(customernotify.NotifyActionName, func(context.Context, customernotify.NotifyInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	worker := customernotify.NewWorker(db, scanTasks, sender, testPublicURL)
	notification := customernotify.NotifyInput{OrganizationID: identity.Organization.ID, ConversationID: conversationID}
	// 未到检查时间时既不投递也不发信。
	if err := worker.Scan(ctx, struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err := worker.Execute(ctx, notification); err != nil || len(notificationTasks(t, db, conversationID)) != 0 || len(sender.sent()) != 0 {
		t.Fatalf("early notification tasks = %d, sent = %d, error = %v", len(notificationTasks(t, db, conversationID)), len(sender.sent()), err)
	}
	makeNotificationDue(t, db, conversationID)
	for range 2 {
		if err := worker.Scan(ctx, struct{}{}); err != nil {
			t.Fatal(err)
		}
	}
	if tasks := notificationTasks(t, db, conversationID); len(tasks) != 1 {
		t.Fatalf("notification tasks = %d", len(tasks))
	}

	// 访客读到第一条后，邮件只包含之后的回复；已读位置不超过会话最新消息。
	mark := customerchataction.NewMarkWebsiteConversationReadAction(db)
	if err := mark.Execute(ctx, channelID, input.ExternalID, conversationID, firstReply.MessageSeq); err != nil {
		t.Fatal(err)
	}
	if err := worker.Execute(ctx, notification); err != nil {
		t.Fatal(err)
	}
	sent := sender.sent()
	if len(sent) != 1 || sent[0].To != "visitor@example.com" || !strings.Contains(sent[0].Text, "退款将在三个工作日内到账") || strings.Contains(sent[0].Text, "已收到您的退款申请") {
		t.Fatalf("sent emails = %+v", sent)
	}
	notified := customerPositions(t, db, conversationID)
	if notified.ContactReadSeq != firstReply.MessageSeq || notified.ContactNotifiedSeq != secondReply.MessageSeq || notified.ContactNotifyDueAt != nil {
		t.Fatalf("customer positions = %+v", notified)
	}
	if count := emailNotifiedEvents(t, db, conversationID); count != 1 {
		t.Fatalf("email notified events = %d", count)
	}
	if err := mark.Execute(ctx, channelID, input.ExternalID, conversationID, secondReply.MessageSeq+1000); err != nil {
		t.Fatal(err)
	}
	if read := customerPositions(t, db, conversationID).ContactReadSeq; read <= secondReply.MessageSeq || read >= secondReply.MessageSeq+1000 {
		t.Fatalf("clamped read position = %d", read)
	}

	// 回访令牌可重复换取原访客令牌与会话摘要，错误令牌与其他渠道不可用。
	resumeURL := resumeLink(t, sent[0].Text)
	token := resumeURL.Query().Get("resume")
	if resumeURL.Scheme+"://"+resumeURL.Host != testPublicURL || resumeURL.Path != "/chat/"+channelID || token == "" {
		t.Fatalf("resume url = %s", resumeURL)
	}
	resume := customerchataction.NewResumeWebsiteVisitorQuery(db)
	for range 2 {
		resumed, err := resume.Execute(ctx, channelID, token)
		if err != nil || "web-session:"+resumed.VisitorToken != input.ExternalID || resumed.Conversation.ID != conversationID {
			t.Fatalf("resumed = %+v, error = %v", resumed, err)
		}
	}
	if _, err := resume.Execute(ctx, channelID, strings.Repeat("0", 64)); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("invalid token error = %v", err)
	}
	otherChannelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	if _, err := resume.Execute(ctx, otherChannelID, token); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("other channel token error = %v", err)
	}

	// 发信失败时保留检查时间与水位，恢复后重试发送。
	thirdReply := reply("请确认收款账户")
	makeNotificationDue(t, db, conversationID)
	sender.fail = errors.New("smtp unavailable")
	if err := worker.Execute(ctx, notification); err == nil {
		t.Fatal("notification succeeded while smtp is unavailable")
	}
	if positions := customerPositions(t, db, conversationID); positions.ContactNotifiedSeq != secondReply.MessageSeq || positions.ContactNotifyDueAt == nil || emailNotifiedEvents(t, db, conversationID) != 1 {
		t.Fatalf("positions after failure = %+v", positions)
	}
	sender.fail = nil
	if err := worker.Execute(ctx, notification); err != nil {
		t.Fatal(err)
	}
	if sent := sender.sent(); len(sent) != 2 || !strings.Contains(sent[1].Text, "请确认收款账户") || customerPositions(t, db, conversationID).ContactNotifiedSeq != thirdReply.MessageSeq {
		t.Fatalf("sent after retry = %+v", sent)
	}
	// 访客已读完时收尾清除检查时间；重试耗尽后放弃本批回复并清除检查时间。
	fourthReply := reply("还有其他问题吗")
	if err := mark.Execute(ctx, channelID, input.ExternalID, conversationID, fourthReply.MessageSeq); err != nil {
		t.Fatal(err)
	}
	makeNotificationDue(t, db, conversationID)
	if err := worker.Execute(ctx, notification); err != nil || customerPositions(t, db, conversationID).ContactNotifyDueAt != nil || len(sender.sent()) != 2 {
		t.Fatalf("read notification due = %v, sent = %d, error = %v", customerPositions(t, db, conversationID).ContactNotifyDueAt, len(sender.sent()), err)
	}
	reply("稍后给您回电")
	if err := worker.FinalizeFailure(ctx, notification, errors.New("mailbox unavailable")); err != nil || customerPositions(t, db, conversationID).ContactNotifyDueAt != nil {
		t.Fatalf("finalized notification due = %v, error = %v", customerPositions(t, db, conversationID).ContactNotifyDueAt, err)
	}
	var eventTimes []time.Time
	if err := db.NewSelect().Model((*servermodels.Message)(nil)).Column("msg.originated_at").
		Where("msg.conversation_id = ? AND msg.system_event_type IN (?)", conversationID, bun.In([]domain.ConversationSystemEventType{
			domain.ConversationSystemEventServiceSessionEmailCollected, domain.ConversationSystemEventServiceSessionEmailNotified,
		})).Scan(ctx, &eventTimes); err != nil {
		t.Fatal(err)
	}
	for _, at := range eventTimes {
		if at.Year() < 2000 {
			t.Fatalf("email event originated at %v", at)
		}
	}
}

// makeNotificationDue 把客户会话的邮件通知检查时间提前到当前时间之前。
func makeNotificationDue(t *testing.T, db *bun.DB, conversationID string) {
	t.Helper()
	if _, err := db.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
		Set("contact_notify_due_at = now() - interval '1 second'").
		Where("conversation_id = ?", conversationID).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// notificationTasks 读取会话已投递的邮件通知任务。
func notificationTasks(t *testing.T, db *bun.DB, conversationID string) []servermodels.TaskRun {
	t.Helper()
	var runs []servermodels.TaskRun
	if err := db.NewSelect().Model(&runs).
		Where("tr.action_name = ? AND tr.payload->>'conversationId' = ?", customernotify.NotifyActionName, conversationID).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runs
}

// customerPositions 读取客户已读位置与已通知位置。
func customerPositions(t *testing.T, db *bun.DB, conversationID string) servermodels.ChannelConversation {
	t.Helper()
	customer := servermodels.ChannelConversation{}
	if err := db.NewSelect().Model(&customer).Where("cc.conversation_id = ?", conversationID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return customer
}

// emailNotifiedEvents 统计会话中只对成员可见的邮件通知事件。
func emailNotifiedEvents(t *testing.T, db *bun.DB, conversationID string) int {
	t.Helper()
	count, err := db.NewSelect().Model((*servermodels.Message)(nil)).
		Where("msg.conversation_id = ? AND msg.system_event_type = ? AND msg.visibility = ?", conversationID,
			domain.ConversationSystemEventServiceSessionEmailNotified, domain.MessageVisibilityInternal).
		Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// resumeLink 从纯文本邮件中取出「继续对话」链接。
func resumeLink(t *testing.T, text string) *url.URL {
	t.Helper()
	for _, field := range strings.Fields(text) {
		if strings.HasPrefix(field, "https://") {
			parsed, err := url.Parse(field)
			if err != nil {
				t.Fatal(err)
			}
			return parsed
		}
	}
	t.Fatalf("resume link missing in %q", text)
	return nil
}
