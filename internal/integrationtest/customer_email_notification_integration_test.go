//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
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
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// handoffEmailRequest 是渠道语言 zh-CN 下转人工话术追加的留邮箱请求。
const handoffEmailRequest = "如需离开，可以在这里留下邮箱，客服回复后我们会发邮件通知您。"

// TestCustomerEmailNotification 验证转人工后收集访客邮箱、访客未读时合并发送邮件通知，以及凭回访令牌回到原会话。
func TestCustomerEmailNotification(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	f := handoffFixture{db: db, identity: identity, tasks: tasks, providerID: providerID, modelID: modelID}
	disableAutoAssignment(t, db, identity.Workspace.ID)
	sender := &servertest.RecordingMailSender{}
	agent := f.newAgent(t, "邮件通知客服")
	channelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	input := visitorInput(channelID, "")
	receive := func(body string) customerchataction.ReceiveWebsiteCustomerMessageResult {
		t.Helper()
		input.ClientMessageID, input.Body = uuid.NewV7().String(), body
		result, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, agentrunaction.NewScheduler(tasks), testEnqueuer, sender).Execute(ctx, input)
		require.NoError(t, err)
		input.ConversationID = &result.Conversation.ID
		return result
	}

	// 转人工进入公共队列时，话术末尾请访客留下邮箱。
	first := receive("我要退款")
	conversationID := first.Conversation.ID
	run := f.queuedRun(t, conversationID)
	require.NoError(t, newTestAgentRun(db, tasks, handoffRuntime("客户要求退款", nil), testModelInvoker(db), testAttachmentReader(db), nil, sender, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	require.Equal(t, handoffQueuedNotice+"\n\n"+handoffEmailRequest, handoffNotice(t, db, "agent:"+run.ID))

	// 联系方式达到数量上限时邮箱未写入，也不追加留邮箱事件。
	var contactID string
	require.NoError(t, db.NewSelect().TableExpr("channel_identities AS ci").Column("ci.contact_id").
		Where("ci.workspace_id = ? AND ci.external_id = ?", identity.Workspace.ID, input.ExternalID).Scan(ctx, &contactID))
	for index := range domain.ContactMethodsMaxCount {
		phone := "+86 138 0000 " + strings.Repeat("0", 3-len(strconv.Itoa(index))) + strconv.Itoa(index)
		_, err := db.NewInsert().Model(&servermodels.ContactMethod{
			ID: uuid.NewV7().String(), WorkspaceID: identity.Workspace.ID, ContactID: contactID,
			Type: string(domain.ContactMethodTypePhone), Value: phone, NormalizedValue: phone, IsPrimary: index == 0,
		}).Exec(ctx)
		require.NoError(t, err)
	}
	receive("我的邮箱是 limit@example.com")
	limitCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).
		Where("msg.conversation_id = ? AND msg.system_event_type = ?", conversationID, domain.ConversationSystemEventServiceSessionEmailCollected).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, limitCount, "email collected events at method limit")
	_, err = db.NewDelete().Model((*servermodels.ContactMethod)(nil)).Where("contact_id = ?", contactID).Exec(ctx)
	require.NoError(t, err)

	// 转人工后访客消息中的唯一邮箱写入联系人，并向访客展示留邮箱事件，客服侧按参与方变化重读客户资料；已有邮箱时不重复收集。
	feed := startRealtimeFeed(t, identity.Workspace.ID)
	receive("我的邮箱是 Visitor@Example.com。")
	feed.expectCustomerInboxChanges(t, conversationID, domain.ConversationChangeTimeline|domain.ConversationChangeService|domain.ConversationChangeParticipants)
	receive("备用邮箱 other@example.com")
	var emails []string
	require.NoError(t, db.NewSelect().TableExpr("contact_methods AS cm").ColumnExpr("cm.normalized_value").
		Join("JOIN channel_identities AS ci ON ci.contact_id = cm.contact_id").
		Where("ci.external_id = ? AND cm.type = ? AND cm.is_primary", input.ExternalID, domain.ContactMethodTypeEmail).
		Scan(ctx, &emails))
	require.Equal(t, []string{"visitor@example.com"}, emails)
	history, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(ctx, customerchataction.MessageHistoryInput{ChannelID: channelID, ExternalID: input.ExternalID, ConversationID: conversationID})
	require.NoError(t, err)
	collected := 0
	for _, message := range history.Messages {
		if message.Event != nil && message.Event.Type == customerchataction.VisitorEventEmailCollected {
			collected++
			require.Equal(t, "visitor@example.com", message.Event.Email)
		}
	}
	require.Equal(t, 1, collected, "email collected events")

	// 真人连续回复只由第一条起算一次检查时间；扫描到期会话时同一会话只投递一个任务。
	send := servicesessionaction.NewSendServiceTextMessageAction(db, testEnqueuer)
	reply := func(body string) conversationaction.ConversationMessage {
		t.Helper()
		message, err := send.Execute(ctx, identity, servicesessionaction.ServiceTextMessageInput{ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: body})
		require.NoError(t, err)
		return message
	}
	firstReply := reply("已收到您的退款申请")
	scheduled := customerPositions(t, db, conversationID).ContactNotifyDueAt
	secondReply := reply("退款将在三个工作日内到账")
	require.NotNil(t, scheduled)
	require.WithinDuration(t, firstReply.OriginatedAt.Add(3*time.Minute), *scheduled, time.Millisecond)
	require.True(t, customerPositions(t, db, conversationID).ContactNotifyDueAt.Equal(*scheduled))
	scanTasks := servertest.NewTasks()
	worker := customernotify.NewWorker(db, scanTasks, sender, func() string { return servertest.PublicURL })
	notification := customernotify.NotifyInput{WorkspaceID: identity.Workspace.ID, ConversationID: conversationID}
	// 未到检查时间时既不投递也不发信。
	require.NoError(t, worker.Scan(ctx, struct{}{}))
	require.NoError(t, worker.Execute(ctx, notification))
	require.Empty(t, notificationTasks(t, scanTasks, conversationID))
	require.Empty(t, sender.Sent())
	makeNotificationDue(t, db, conversationID)
	for range 2 {
		require.NoError(t, worker.Scan(ctx, struct{}{}))
	}
	require.Len(t, notificationTasks(t, scanTasks, conversationID), 1)

	// 访客读到第一条后，邮件只包含之后的回复；已读位置不超过会话最新消息。
	mark := customerchataction.NewMarkWebsiteConversationReadAction(db)
	require.NoError(t, mark.Execute(ctx, channelID, input.ExternalID, conversationID, firstReply.MessageSeq))
	require.NoError(t, worker.Execute(ctx, notification))
	sent := sender.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, "visitor@example.com", sent[0].To)
	require.Contains(t, sent[0].Text, "退款将在三个工作日内到账")
	require.NotContains(t, sent[0].Text, "已收到您的退款申请")
	notified := customerPositions(t, db, conversationID)
	require.Equal(t, firstReply.MessageSeq, notified.ContactReadSeq)
	require.Equal(t, secondReply.MessageSeq, notified.ContactNotifiedSeq)
	require.Nil(t, notified.ContactNotifyDueAt)
	require.Equal(t, 1, emailNotifiedEvents(t, db, conversationID), "email notified events")
	require.NoError(t, mark.Execute(ctx, channelID, input.ExternalID, conversationID, secondReply.MessageSeq+1000))
	read := customerPositions(t, db, conversationID).ContactReadSeq
	require.Greater(t, read, secondReply.MessageSeq, "clamped read position")
	require.Less(t, read, secondReply.MessageSeq+1000, "clamped read position")

	// 回访令牌可重复换取原访客令牌与会话摘要，错误令牌与其他渠道不可用。
	resumeURL := resumeLink(t, sent[0].Text)
	token := resumeURL.Query().Get("resume")
	require.Equal(t, servertest.PublicURL, resumeURL.Scheme+"://"+resumeURL.Host)
	require.Equal(t, "/chat/"+channelID, resumeURL.Path)
	require.NotEmpty(t, token)
	resume := customerchataction.NewResumeWebsiteVisitorQuery(db)
	for range 2 {
		resumed, err := resume.Execute(ctx, channelID, token)
		require.NoError(t, err)
		require.Equal(t, input.ExternalID, "web-session:"+resumed.VisitorToken)
		require.Equal(t, conversationID, resumed.Conversation.ID)
	}
	_, err = resume.Execute(ctx, channelID, strings.Repeat("0", 64))
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "invalid token")
	otherChannelID := f.newChannel(t, agent.IdentityID, channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	_, err = resume.Execute(ctx, otherChannelID, token)
	require.ErrorIs(t, err, conversationaction.ErrConversationNotFound, "other channel token")

	// 发信失败时保留检查时间与水位，恢复后重试发送。
	thirdReply := reply("请确认收款账户")
	makeNotificationDue(t, db, conversationID)
	sender.Fail = errors.New("smtp unavailable")
	require.Error(t, worker.Execute(ctx, notification), "notification succeeded while smtp is unavailable")
	positions := customerPositions(t, db, conversationID)
	require.Equal(t, secondReply.MessageSeq, positions.ContactNotifiedSeq)
	require.NotNil(t, positions.ContactNotifyDueAt)
	require.Equal(t, 1, emailNotifiedEvents(t, db, conversationID))
	sender.Fail = nil
	require.NoError(t, worker.Execute(ctx, notification))
	sent = sender.Sent()
	require.Len(t, sent, 2)
	require.Contains(t, sent[1].Text, "请确认收款账户")
	require.Equal(t, thirdReply.MessageSeq, customerPositions(t, db, conversationID).ContactNotifiedSeq)
	// 访客已读完时收尾清除检查时间；重试耗尽后放弃本批回复并清除检查时间。
	fourthReply := reply("还有其他问题吗")
	require.NoError(t, mark.Execute(ctx, channelID, input.ExternalID, conversationID, fourthReply.MessageSeq))
	makeNotificationDue(t, db, conversationID)
	require.NoError(t, worker.Execute(ctx, notification))
	require.Nil(t, customerPositions(t, db, conversationID).ContactNotifyDueAt)
	require.Len(t, sender.Sent(), 2)
	reply("稍后给您回电")
	require.NoError(t, worker.FinalizeFailure(ctx, notification, errors.New("mailbox unavailable")))
	require.Nil(t, customerPositions(t, db, conversationID).ContactNotifyDueAt)
	var eventTimes []time.Time
	require.NoError(t, db.NewSelect().Model((*servermodels.Message)(nil)).Column("msg.originated_at").
		Where("msg.conversation_id = ? AND msg.system_event_type IN (?)", conversationID, bun.List([]domain.ConversationSystemEventType{
			domain.ConversationSystemEventServiceSessionEmailCollected, domain.ConversationSystemEventServiceSessionEmailNotified,
		})).Scan(ctx, &eventTimes))
	for _, at := range eventTimes {
		require.GreaterOrEqual(t, at.Year(), 2000, "email event originated at %v", at)
	}
}

// makeNotificationDue 把客户会话的邮件通知检查时间提前到当前时间之前。
func makeNotificationDue(t *testing.T, db *bun.DB, conversationID string) {
	t.Helper()
	_, err := db.NewUpdate().Model((*servermodels.ChannelConversation)(nil)).
		Set("contact_notify_due_at = now() - interval '1 second'").
		Where("conversation_id = ?", conversationID).Exec(context.Background())
	require.NoError(t, err)
}

// notificationTasks 返回登记器中会话已投递的邮件通知任务输入。
func notificationTasks(t *testing.T, tasks *servertest.Tasks, conversationID string) []customernotify.NotifyInput {
	t.Helper()
	return servertest.QueuedInputs(t, tasks, customernotify.NotifyActionName, func(input customernotify.NotifyInput) bool {
		return input.ConversationID == conversationID
	})
}

// customerPositions 读取客户已读位置与已通知位置。
func customerPositions(t *testing.T, db *bun.DB, conversationID string) servermodels.ChannelConversation {
	t.Helper()
	customer := servermodels.ChannelConversation{}
	require.NoError(t, db.NewSelect().Model(&customer).Where("cc.conversation_id = ?", conversationID).Scan(context.Background()))
	return customer
}

// emailNotifiedEvents 统计会话中只对成员可见的邮件通知事件。
func emailNotifiedEvents(t *testing.T, db *bun.DB, conversationID string) int {
	t.Helper()
	count, err := db.NewSelect().Model((*servermodels.Message)(nil)).
		Where("msg.conversation_id = ? AND msg.system_event_type = ? AND msg.visibility = ?", conversationID,
			domain.ConversationSystemEventServiceSessionEmailNotified, domain.MessageVisibilityInternal).
		Count(context.Background())
	require.NoError(t, err)
	return int(count)
}

// resumeLink 从纯文本邮件中取出「继续对话」链接。
func resumeLink(t *testing.T, text string) *url.URL {
	t.Helper()
	for _, field := range strings.Fields(text) {
		if strings.HasPrefix(field, "https://") {
			parsed, err := url.Parse(field)
			require.NoError(t, err)
			return parsed
		}
	}
	require.Failf(t, "resume link missing", "%q", text)
	return nil
}
