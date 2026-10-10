//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	servertest "github.com/runforyou-ai/luway/internal/servertest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/usernotification"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// userNotice 是测试读取到的一条用户通知，UserID 取自接收成员的用户受众。
type userNotice struct {
	UserID         string
	ID             string
	ConversationID string
	View           domain.NotificationView
	Title          string
	Body           string
}

// userNotices 读取已发布的用户通知，片刻内没有新通知时返回，忽略其他种类的通知。
func (f *realtimeFeed) userNotices(t *testing.T) []userNotice {
	t.Helper()
	got := make([]userNotice, 0)
	for {
		var message tappedBroadcast
		select {
		case message = <-f.broadcasts:
		case <-time.After(300 * time.Millisecond):
			return got
		}
		var payload realtime.Payload
		require.NoError(t, json.Unmarshal(message.data, &payload))
		if payload.Kind != realtime.KindUserNotification {
			continue
		}
		got = append(got, userNotice{
			UserID: message.topic[strings.LastIndexByte(message.topic, '.')+1:], ID: payload.NotificationID, ConversationID: payload.ConversationID,
			View: payload.View, Title: payload.Title, Body: payload.Body,
		})
	}
}

// compareUserNotices 按接收成员比较用户通知。
func compareUserNotices(t *testing.T, got, want []userNotice) {
	t.Helper()
	compare := func(a, b userNotice) int { return strings.Compare(a.UserID+a.ID, b.UserID+b.ID) }
	slices.SortFunc(got, compare)
	slices.SortFunc(want, compare)
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("用户通知不符 (-want +got):\n%s", diff)
	}
}

// deliverMessage 执行一条消息的用户通知任务。
func deliverMessage(t *testing.T, deliverer *usernotification.Deliverer, workspaceID, conversationID, messageID string) {
	t.Helper()
	require.NoError(t, deliverer.DeliverMessage(context.Background(), notificationtask.MessageInput{WorkspaceID: workspaceID, ConversationID: conversationID, MessageID: messageID}))
}

// updateMember 修改成员用户行或成员身份行的单个字段。
func updateMember(t *testing.T, db *bun.DB, table, set string, identity *servermodels.Identity) {
	t.Helper()
	id := identity.User.ID
	if table == "workspace_identities" {
		id = identity.WorkspaceIdentity.ID
	}
	_, err := db.NewUpdate().Table(table).Set(set).Where("workspace_id = ? AND id = ?", identity.Workspace.ID, id).Exec(context.Background())
	require.NoError(t, err)
}

// setAccountLocale 修改成员账号的界面语言。
func setAccountLocale(t *testing.T, db *bun.DB, identity *servermodels.Identity, locale domain.Locale) {
	t.Helper()
	_, err := db.NewUpdate().Table("accounts").Set("locale = ?", locale).Where("id = ?", identity.User.AccountID).Exec(context.Background())
	require.NoError(t, err)
}

// TestUserNotificationGroup 验证群聊新消息按接收人语言生成通知：已读确认窗口内读到的消息、静音后的普通消息、关闭通知或不在工作中的成员不再通知，加入多个工作区时标题标明工作区。
func TestUserNotificationGroup(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	setAccountLocale(t, f.db, f.member, domain.LocaleChineseSimplified)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	deliverer := usernotification.NewDeliverer(f.db, testEnqueuer)
	notice := func(message conversationaction.ConversationMessage, title, body string) userNotice {
		return userNotice{UserID: f.member.User.ID, ID: "message:" + message.ID, ConversationID: f.groupID, View: domain.NotificationViewGroup, Title: title, Body: body}
	}

	plain := f.send(t, f.owner, "普通消息", false)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, plain.ID)
	compareUserNotices(t, feed.userNotices(t), []userNotice{notice(plain, "导航测试群", "群主：普通消息")})

	// 已读确认窗口内在任一设备读到的消息不再通知。
	read := f.send(t, f.owner, "已读消息", false)
	_, err := conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.groupID, read.ID, false)
	require.NoError(t, err)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, read.ID)
	compareUserNotices(t, feed.userNotices(t), nil)

	// 静音后只通知提醒本人的消息。
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.member, f.groupID, true)
	require.NoError(t, err)
	muted := f.send(t, f.owner, "静音后普通消息", false)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, muted.ID)
	mentioned := f.send(t, f.owner, "提醒成员", false, f.subjectID)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, mentioned.ID)
	compareUserNotices(t, feed.userNotices(t), []userNotice{notice(mentioned, "导航测试群", "群主：提醒成员")})

	// 关闭消息通知或不在工作中的成员不接收通知。
	updateMember(t, f.db, "users", "message_notifications_enabled = false", f.member)
	disabled := f.send(t, f.owner, "关闭通知后", true)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, disabled.ID)
	updateMember(t, f.db, "users", "message_notifications_enabled = true", f.member)
	updateMember(t, f.db, "workspace_identities", "work_status = 'away'", f.member)
	away := f.send(t, f.owner, "离开后", true)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, away.ID)
	compareUserNotices(t, feed.userNotices(t), nil)
	updateMember(t, f.db, "workspace_identities", "work_status = 'working'", f.member)

	// 成员账号加入第二个工作区后，标题前标明通知所在的工作区。
	require.NoError(t, f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := workspaceaction.Create(ctx, tx, workspaceaction.CreateInput{Name: servertest.UniqueWorkspaceName("第二工作区"), Account: &f.member.Account, AdminDisplayName: "成员"})
		return err
	}))
	second := f.send(t, f.owner, "多工作区", true)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.groupID, second.ID)
	compareUserNotices(t, feed.userNotices(t), []userNotice{notice(second, f.owner.Workspace.Name+" · 导航测试群", "群主：多工作区")})
}

// TestUserNotificationCustomerService 验证客户来信通知待处理该会话的成员，领取后只通知负责人，客服处理周期提醒按原因生成正文。
func TestUserNotificationCustomerService(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	setAccountLocale(t, f.db, f.member, domain.LocaleChineseSimplified)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	deliverer := usernotification.NewDeliverer(f.db, testEnqueuer)

	queued, err := f.visitorMessage(ctx, "有人吗")
	require.NoError(t, err)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.conversationID, queued.Message.ID)
	got := feed.userNotices(t)
	users := make([]string, 0, len(got))
	for _, item := range got {
		users = append(users, item.UserID)
		require.Equal(t, domain.NotificationViewService, item.View, "queued notice=%+v", item)
		require.Equal(t, "有人吗", item.Body, "queued notice=%+v", item)
		// 访客没有名称时以访客编号为标题，编号文案随接收人语言。
		if item.UserID == f.owner.User.ID {
			require.True(t, strings.HasPrefix(item.Title, "Visitor #"), "queued title=%+v", item)
		}
		if item.UserID == f.member.User.ID {
			require.True(t, strings.HasPrefix(item.Title, "访客 #"), "queued title=%+v", item)
		}
	}
	slices.Sort(users)
	want := []string{f.owner.User.ID, f.member.User.ID}
	require.Equal(t, slices.Sorted(slices.Values(want)), users, "queued users")

	_, err = servicesessionaction.NewClaimServiceSessionAction(f.db, nil, testEnqueuer).Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	assigned, err := f.visitorMessage(ctx, "还在吗")
	require.NoError(t, err)
	deliverMessage(t, deliverer, f.owner.Workspace.ID, f.conversationID, assigned.Message.ID)
	got = feed.userNotices(t)
	require.Len(t, got, 1)
	require.Equal(t, f.owner.User.ID, got[0].UserID)
	require.Equal(t, "message:"+assigned.Message.ID, got[0].ID)

	session := assignmentFixture{customerReadFixture: f}.currentSession(t, f.conversationID)
	require.NoError(t, deliverer.DeliverServiceAttention(ctx, notificationtask.ServiceAttentionInput{
		WorkspaceID: f.owner.Workspace.ID, UserID: f.member.User.ID, ConversationID: f.conversationID, ServiceSessionID: session.ID,
		Reason: domain.ServiceAttentionReturned, NotificationID: "service_attention:returned",
	}))
	got = feed.userNotices(t)
	require.Len(t, got, 1)
	if diff := cmp.Diff(userNotice{
		UserID: f.member.User.ID, ID: "service_attention:returned", View: domain.NotificationViewService, Body: "你超时未回复的客户会话已退回队列",
	}, got[0], cmpopts.IgnoreFields(userNotice{}, "ConversationID", "Title")); diff != "" {
		t.Fatalf("attention notice mismatch (-want +got):\n%s", diff)
	}
}

// TestUserNotificationEnqueue 验证计入提醒的消息在写入事务内登记按消息幂等、在已读确认窗口后执行的通知任务，系统事件不登记。
func TestUserNotificationEnqueue(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	message := f.send(t, f.owner, "登记通知", false)
	runs := testEnqueuer.Queued(notificationtask.MessageActionName, f.owner.Workspace.ID)
	require.Len(t, runs, 1, "message notification tasks")
	require.Equal(t, "notification-message:"+message.ID, runs[0].Options.IdempotencyKey)
	require.Equal(t, notificationtask.MessageSettleDelay, runs[0].Options.Delay)
	_, err := groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{ConversationID: f.groupID, Title: "改名后的群"})
	require.NoError(t, err)
	require.Len(t, testEnqueuer.Queued(notificationtask.MessageActionName, f.owner.Workspace.ID), 1, "message notification tasks")
}

// TestUserNotificationRequesterStatus 验证员工在 AI 员工单聊中发起的服务转人工后，发起人按本人 AI 聊天收到服务进度通知，处理方不接收发起人可见的进度。
func TestUserNotificationRequesterStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newDirectServiceFixture(t)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	conversationID := f.startChat(t, f.agent.IdentityID, "电脑连不上 VPN")
	run := (handoffFixture{db: f.db}).queuedRun(t, conversationID)
	require.NoError(t, newTestAgentRun(f.db, f.tasks, handoffRuntime("需要 IT 同事排查", nil), testModelInvoker(f.db), testAttachmentReader(f.db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	var statusID string
	require.NoError(t, f.db.NewSelect().Model((*servermodels.Message)(nil)).Column("id").
		Where("conversation_id = ? AND type = ? AND visibility = ?", conversationID, domain.MessageTypeSystem, domain.MessageVisibilityRequester).
		OrderExpr("message_seq DESC").Limit(1).Scan(ctx, &statusID))
	deliverMessage(t, usernotification.NewDeliverer(f.db, testEnqueuer), f.owner.Workspace.ID, conversationID, statusID)
	got := feed.userNotices(t)
	require.Len(t, got, 1)
	if diff := cmp.Diff(userNotice{
		UserID: f.owner.User.ID, ID: "message:" + statusID, View: domain.NotificationViewAgent, Body: "Request status updated",
	}, got[0], cmpopts.IgnoreFields(userNotice{}, "ConversationID", "Title")); diff != "" {
		t.Fatalf("requester status notice mismatch (-want +got):\n%s", diff)
	}
	require.True(t, strings.HasPrefix(got[0].Title, "IT 服务台 · "), "requester status title=%q", got[0].Title)
}
