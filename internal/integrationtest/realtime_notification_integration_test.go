//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/einorun"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/luway/pkg/pglisten"
	"github.com/runforyou-ai/support/arr"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// receivedNotification 表示订阅端收到的一条实时通知。
type receivedNotification struct {
	Subject         string
	Kind            string
	ConversationID  string
	Version         string
	SenderSubjectID string
	Active          bool
	// ServiceSessionID 与 AttentionReason 只由客服处理周期提醒通知任务携带。
	ServiceSessionID string
	AttentionReason  string
	// Changes 是会话变更通知的变化类别，checkChanges 为真时参与比较。
	Changes      domain.ConversationChanges
	checkChanges bool
}

// withChanges 返回要求会话变化类别精确匹配的期望通知。
func (n receivedNotification) withChanges(changes domain.ConversationChanges) receivedNotification {
	n.Changes, n.checkChanges = changes, true
	return n
}

// realtimeFeed 旁听单个测试企业的全部受众通知。
type realtimeFeed struct {
	workspaceID string
	broadcasts  <-chan tappedBroadcast
}

// realtimePublisherLock 串行化启动实时发布器的测试；进程内只有一个活动发布器接收已提交通知。
var realtimePublisherLock sync.Mutex

// openRealtimeDB 打开实时测试独占的数据库连接池，测试结束时关闭。
func openRealtimeDB(t *testing.T) *bun.DB {
	t.Helper()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store.DB()
}

// startTestPublisher 在测试独占的总线频道上启动进程内活动发布器。
func startTestPublisher(t *testing.T) *realtime.Publisher {
	t.Helper()
	return startTestPublisherOn(t, openRealtimeDB(t), servertest.BusChannel())
}

// startTestPublisherOn 独占进程内活动发布器，以新的实例编号加入指定总线频道并启动发布器，测试结束时停止发布器、离开总线后释放独占。
func startTestPublisherOn(t *testing.T, db *bun.DB, channel string) *realtime.Publisher {
	t.Helper()
	realtimePublisherLock.Lock()
	t.Cleanup(realtimePublisherLock.Unlock)
	return startPublisher(t, servertest.StartBus(t, db, channel, uuid.NewV7().String()), db)
}

// startPublisher 在已启动的总线上启动发布器，测试结束时停止。
func startPublisher(t *testing.T, bus *clusterbus.Bus, db *bun.DB) *realtime.Publisher {
	t.Helper()
	publisher := realtime.NewPublisher(bus)
	require.NoError(t, publisher.Start())
	t.Cleanup(func() { _ = publisher.Stop() })
	return publisher
}

// tappedBroadcast 是旁听到的一条广播。
type tappedBroadcast struct {
	topic string
	data  []byte
}

// tapBroadcasts 监听总线广播频道，把主题以 prefix 开头的广播送入通道；载荷按总线传输层的 JSON 编码解析，测试通知不超过单条 NOTIFY 上限。
func tapBroadcasts(t *testing.T, channel, prefix string) chan tappedBroadcast {
	t.Helper()
	listenConfig, err := serverstorage.ConnConfig(servertest.DatabaseConfig(t), "")
	require.NoError(t, err)
	broadcasts := make(chan tappedBroadcast, 256)
	listener, err := pglisten.Listen(context.Background(), listenConfig, []string{channel}, func(notification pglisten.Notification) {
		var message struct {
			Topic string `json:"o"`
			Data  []byte `json:"d"`
		}
		if json.Unmarshal([]byte(notification.Payload), &message) != nil || !strings.HasPrefix(message.Topic, prefix) {
			return
		}
		if strings.Contains(message.Topic, ".user.") || strings.Contains(message.Topic, ".customer_inbox.") {
			return
		}
		select {
		case broadcasts <- tappedBroadcast{topic: message.Topic, data: message.Data}:
		default:
		}
	})
	require.NoError(t, err)
	t.Cleanup(listener.Close)
	return broadcasts
}

// startRealtimeFeed 在独立总线频道上启动实时发布器，并旁听指定企业的全部受众通知。
func startRealtimeFeed(t *testing.T, workspaceID string) *realtimeFeed {
	t.Helper()
	db := openRealtimeDB(t)
	channel := servertest.BusChannel()
	broadcasts := tapBroadcasts(t, channel, "realtime."+workspaceID+".")
	publisher := startTestPublisherOn(t, db, channel)
	connection := servertest.StartRealtimeBroker(t)
	backend := newAccountTestBackend(t, db)
	service, err := members.New(connection, backend, db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, service.Start(context.Background()))
	t.Cleanup(func() { _ = service.Stop() })
	publisher.SetMembers(service)
	// 应用账户旁听真实 jetcast 频道，沿用业务受众与载荷的断言。
	sub, err := connection.Conn.Subscribe("app_realtime.ev.prv.w."+workspaceID+".>", func(message *nats.Msg) {
		parts := strings.Split(message.Subject, ".")
		var envelope struct {
			Type string                     `json:"type"`
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(message.Data, &envelope) != nil {
			return
		}
		envelope.Data["kind"], _ = json.Marshal(envelope.Type)
		if envelope.Type == "user_notification" {
			envelope.Data["notificationId"] = envelope.Data["id"]
			delete(envelope.Data, "id")
		}
		data, _ := json.Marshal(envelope.Data)
		topic := realtime.Topic(workspaceID, realtime.AudienceCustomerInbox, workspaceID)
		if len(parts) > 6 && parts[5] == "u" {
			topic = realtime.Topic(workspaceID, realtime.AudienceUser, parts[6])
		}
		if len(parts) > 6 && parts[5] == "v" {
			topic = realtime.Topic(workspaceID, realtime.AudienceVisitorDirectory, parts[6])
		}
		if len(parts) > 6 && parts[5] == "c" {
			topic = realtime.Topic(workspaceID, realtime.AudienceComputer, parts[6])
		}
		if parts[5] == "visitors" {
			topic = realtime.Topic(workspaceID, realtime.AudienceWebsiteVisitors, workspaceID)
		}
		select {
		case broadcasts <- tappedBroadcast{topic: topic, data: data}:
		case <-t.Context().Done():
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	require.NoError(t, connection.Conn.Flush())
	return &realtimeFeed{workspaceID: workspaceID, broadcasts: broadcasts}
}

// notice 构造发往指定用户受众的期望通知。
func (f *realtimeFeed) notice(userID string, kind realtime.Kind, conversationID string, version int64) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceUser, userID),
		Kind:    string(kind), ConversationID: conversationID, Version: strconv.FormatInt(version, 10),
	}
}

// userTyping 构造发往指定用户受众的会话输入状态。
func (f *realtimeFeed) userTyping(userID, conversationID, senderSubjectID string, active bool) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceUser, userID),
		Kind:    string(realtime.KindConversationTyping), ConversationID: conversationID, SenderSubjectID: senderSubjectID, Active: active,
	}
}

// loadIdentitySubjectID 读取企业身份的聊天主体编号。
func loadIdentitySubjectID(t *testing.T, db *bun.DB, workspaceID, identityID string) string {
	t.Helper()
	var subjectID string
	require.NoError(t, db.NewSelect().Table("chat_subjects").Column("id").
		Where("workspace_id = ? AND kind = ? AND source_id = ?", workspaceID, domain.ChatSubjectKindWorkspaceIdentity, identityID).
		Scan(context.Background(), &subjectID))
	return subjectID
}

// removed 构造发往指定用户受众的会话失权通知。
func (f *realtimeFeed) removed(userID, conversationID string) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceUser, userID),
		Kind:    string(realtime.KindConversationRemoved), ConversationID: conversationID,
	}
}

// customerInbox 构造发往企业客服共享受众的客户会话变更通知。
func (f *realtimeFeed) customerInbox(conversationID string, version int64) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceCustomerInbox, f.workspaceID),
		Kind:    string(realtime.KindConversationChanged), ConversationID: conversationID, Version: strconv.FormatInt(version, 10),
	}
}

// visitorDirectory 构造发往网站渠道身份受众的客户线程变更通知。
func (f *realtimeFeed) visitorDirectory(channelIdentityID, conversationID string, version int64) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceVisitorDirectory, channelIdentityID),
		Kind:    string(realtime.KindConversationChanged), ConversationID: conversationID, Version: strconv.FormatInt(version, 10),
	}
}

// aiPerformance 构造发往企业客服共享受众的 AI 表现变化通知。
func (f *realtimeFeed) aiPerformance() receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceCustomerInbox, f.workspaceID),
		Kind:    string(realtime.KindServiceReportsChanged),
	}
}

// reception 构造发往企业全部网站访客的接待状态变化通知。
func (f *realtimeFeed) reception() receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceWebsiteVisitors, f.workspaceID),
		Kind:    string(realtime.KindReceptionChanged),
	}
}

// customerInboxTyping 构造发往企业客服共享受众的客户会话输入状态。
func (f *realtimeFeed) customerInboxTyping(conversationID, senderSubjectID string, active bool) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceCustomerInbox, f.workspaceID),
		Kind:    string(realtime.KindConversationTyping), ConversationID: conversationID, SenderSubjectID: senderSubjectID, Active: active,
	}
}

// visitorTyping 构造发往网站渠道身份受众的访客可见输入状态。
func (f *realtimeFeed) visitorTyping(channelIdentityID, conversationID string, active bool) receivedNotification {
	return receivedNotification{
		Subject: realtime.Topic(f.workspaceID, realtime.AudienceVisitorDirectory, channelIdentityID),
		Kind:    string(realtime.KindVisitorTyping), ConversationID: conversationID, Active: active,
	}
}

// next 读取并解析下一条实时通知，跳过只在服务端实例之间传递的运行结束通知与唤醒信号。
func (f *realtimeFeed) next(t *testing.T) (receivedNotification, error) {
	t.Helper()
	var message tappedBroadcast
	for {
		select {
		case message = <-f.broadcasts:
		case <-time.After(5 * time.Second):
			return receivedNotification{}, errors.New("等待实时通知超时")
		}
		internal := false
		for _, audience := range []realtime.AudienceKind{realtime.AudienceAgentRun, realtime.AudienceAgentLane, realtime.AudienceAgentToolCall} {
			internal = internal || strings.Contains(message.topic, "."+string(audience)+".")
		}
		if !internal {
			break
		}
	}
	var fields map[string]any
	require.NoError(t, json.Unmarshal(message.data, &fields), "解析实时通知 %s", message.data)
	// 载荷只允许种类、会话 ID、会话类型、版本、会话变化类别、发送者主体与输入状态。
	for key := range fields {
		require.Contains(t, []string{"kind", "conversationId", "conversationType", "version", "changes", "senderSubjectId", "active"}, key, "实时通知含业务字段: %s", message.data)
	}
	text := func(key string) string {
		value, _ := fields[key].(string)
		return value
	}
	// 成员受众的会话变更通知必须携带会话类型，访客目录受众不携带。
	if text("kind") == string(realtime.KindConversationChanged) {
		switch domain.ConversationType(text("conversationType")) {
		case domain.ConversationTypeChannel, domain.ConversationTypeCopilot, domain.ConversationTypeDirect, domain.ConversationTypeGroup, domain.ConversationTypeAgent:
			require.NotContains(t, message.topic, "."+string(realtime.AudienceVisitorDirectory)+".", "访客目录通知含会话类型: %s", message.data)
		default:
			require.Contains(t, message.topic, "."+string(realtime.AudienceVisitorDirectory)+".", "会话变更通知缺少会话类型: %s", message.data)
		}
	}
	active, _ := fields["active"].(bool)
	var changes domain.ConversationChanges
	if raw, ok := fields["changes"]; ok {
		data, _ := json.Marshal(raw)
		require.NoError(t, json.Unmarshal(data, &changes), "解析会话变化类别 %s", message.data)
	}
	return receivedNotification{
		Subject: message.topic, Kind: text("kind"), ConversationID: text("conversationId"),
		Version: text("version"), SenderSubjectID: text("senderSubjectId"), Active: active,
		Changes: changes,
	}, nil
}

// expectTyping 只比较输入状态通知，读取期间忽略其他种类的通知。
func (f *realtimeFeed) expectTyping(t *testing.T, want ...receivedNotification) {
	t.Helper()
	got := make([]receivedNotification, 0, len(want))
	for len(got) < len(want) {
		notification, err := f.next(t)
		require.NoError(t, err, "等待输入状态通知，已收到 %+v", got)
		if notification.Kind == string(realtime.KindConversationTyping) || notification.Kind == string(realtime.KindVisitorTyping) {
			got = append(got, notification)
		}
	}
	compareNotifications(t, got, want)
}

// expect 读取与期望数量相同的通知，并与期望集合按任意顺序比较。
func (f *realtimeFeed) expect(t *testing.T, want ...receivedNotification) {
	t.Helper()
	got := make([]receivedNotification, 0, len(want))
	for range want {
		notification, err := f.next(t)
		require.NoError(t, err, "等待实时通知，已收到 %+v", got)
		got = append(got, notification)
	}
	compareNotifications(t, got, want)
}

// expectCustomerInboxChanges 读取到指定客户会话的共享受众变更通知为止，校验其变化类别包含期望类别，期间忽略其他通知。
func (f *realtimeFeed) expectCustomerInboxChanges(t *testing.T, conversationID string, want domain.ConversationChanges) {
	t.Helper()
	subject := realtime.Topic(f.workspaceID, realtime.AudienceCustomerInbox, f.workspaceID)
	for {
		notification, err := f.next(t)
		require.NoError(t, err, "等待客户会话变更通知")
		if notification.Subject != subject || notification.Kind != string(realtime.KindConversationChanged) || notification.ConversationID != conversationID {
			continue
		}
		require.Equal(t, want, notification.Changes&want, "会话变化类别 = %d，缺少 %d", notification.Changes, want)
		return
	}
}

// compareNotifications 按任意顺序比较收到与期望的通知集合。
func compareNotifications(t *testing.T, got, want []receivedNotification) {
	t.Helper()
	compare := func(a, b receivedNotification) int {
		return strings.Compare(
			a.Subject+a.Kind+a.ConversationID+a.Version+a.SenderSubjectID+strconv.FormatBool(a.Active)+a.ServiceSessionID+a.AttentionReason,
			b.Subject+b.Kind+b.ConversationID+b.Version+b.SenderSubjectID+strconv.FormatBool(b.Active)+b.ServiceSessionID+b.AttentionReason,
		)
	}
	slices.SortFunc(got, compare)
	slices.SortFunc(want, compare)
	// 期望未要求变化类别时不比较该字段。
	for index := range got {
		if index < len(want) && !want[index].checkChanges {
			got[index].Changes = 0
		}
		if index < len(want) {
			got[index].checkChanges = want[index].checkChanges
		}
	}
	require.Equal(t, want, got, "实时通知不符")
}

// loadChannelIdentityID 读取客户会话所属的渠道身份记录 ID。
func loadChannelIdentityID(t *testing.T, db *bun.DB, conversationID string) string {
	t.Helper()
	var value string
	require.NoError(t, db.NewSelect().Table("channel_conversations").Column("channel_identity_id").Where("conversation_id = ?", conversationID).Scan(context.Background(), &value))
	return value
}

// loadConversationStateVersion 读取用户个人会话状态版本。
func loadConversationStateVersion(t *testing.T, db *bun.DB, conversationID, userID string) int64 {
	t.Helper()
	var version int64
	require.NoError(t, db.NewSelect().Table("conversation_user_states").Column("version").Where("conversation_id = ? AND user_id = ?", conversationID, userID).Scan(context.Background(), &version))
	return version
}

// loadProfileVersion 读取用户身份资料版本。
func loadProfileVersion(t *testing.T, db *bun.DB, userID string) int64 {
	t.Helper()
	var version int64
	require.NoError(t, db.NewSelect().Table("users").Column("profile_version").Where("id = ?", userID).Scan(context.Background(), &version))
	return version
}

// TestRealtimeConversationNotifications 验证消息通知真人成员、同事务合并多次变化，回滚不发布。
func TestRealtimeConversationNotifications(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	second, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{Title: "第二个群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID}})
	require.NoError(t, err)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)

	// 群消息通知全部真人成员，发送者另收阅读水位推进。
	f.send(t, f.owner, "实时通知", false)
	groupVersion := loadConversationVersion(t, f.db, f.groupID)
	feed.expect(t,
		feed.notice(f.owner.User.ID, realtime.KindConversationChanged, f.groupID, groupVersion),
		feed.notice(f.member.User.ID, realtime.KindConversationChanged, f.groupID, groupVersion),
		feed.notice(f.owner.User.ID, realtime.KindConversationStateChanged, f.groupID, loadConversationStateVersion(t, f.db, f.groupID, f.owner.User.ID)),
	)

	errRollback := errors.New("rollback")
	// appendMessages 在同一事务内按顺序向会话追加消息。
	appendMessages := func(fail bool, conversationIDs ...string) error {
		return realtime.RunInTx(ctx, f.db, func(ctx context.Context, tx bun.Tx) error {
			for _, conversationID := range conversationIDs {
				conversation, err := chatstate.LockConversation(ctx, tx, f.owner.Workspace.ID, conversationID)
				if err != nil {
					return err
				}
				message := &servermodels.Message{ID: uuid.NewV7().String(), WorkspaceID: f.owner.Workspace.ID, ConversationID: conversationID, Type: string(domain.MessageTypeText), Body: "事务通知", OriginatedAt: time.Now().UTC()}
				if _, _, err := chatstate.AppendMessage(ctx, tx, testEnqueuer, conversation, message); err != nil {
					return err
				}
			}
			if fail {
				return errRollback
			}
			return nil
		})
	}
	require.ErrorIs(t, appendMessages(true, f.groupID), errRollback)
	require.Equal(t, groupVersion, loadConversationVersion(t, f.db, f.groupID), "rollback version")
	// 同一事务写两个会话各得一条通知，同会话两次追加只保留最高版本。
	require.NoError(t, appendMessages(false, f.groupID, second.ID, f.groupID))
	groupVersion = loadConversationVersion(t, f.db, f.groupID)
	secondVersion := loadConversationVersion(t, f.db, second.ID)
	feed.expect(t,
		feed.notice(f.owner.User.ID, realtime.KindConversationChanged, f.groupID, groupVersion),
		feed.notice(f.member.User.ID, realtime.KindConversationChanged, f.groupID, groupVersion),
		feed.notice(f.owner.User.ID, realtime.KindConversationChanged, second.ID, secondVersion),
		feed.notice(f.member.User.ID, realtime.KindConversationChanged, second.ID, secondVersion),
	)
	// 以一次本人静音收尾。
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.owner, second.ID, true)
	require.NoError(t, err)
	feed.expect(t, feed.notice(f.owner.User.ID, realtime.KindConversationStateChanged, second.ID, loadConversationStateVersion(t, f.db, second.ID, f.owner.User.ID)))
}

// TestRealtimeConversationStateNotifications 验证已读、静音、手动未读与提及确认只通知本人，重复操作不发布。
func TestRealtimeConversationStateNotifications(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	last := f.send(t, f.owner, "待读", false)
	mention := f.send(t, f.owner, "提及", false, f.subjectID)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	read := conversationaction.NewMarkConversationReadAction(f.db)
	mute := conversationaction.NewUpdateConversationNotificationSettingsAction(f.db)
	mark := conversationaction.NewUpdateConversationUnreadMarkAction(f.db)
	review := conversationaction.NewMarkConversationMentionReviewedAction(f.db)
	for _, step := range []struct {
		name   string
		change func() error
	}{
		{"已读", func() error { _, err := read.Execute(ctx, f.member, f.groupID, last.ID, false); return err }},
		{"静音", func() error { _, err := mute.Execute(ctx, f.member, f.groupID, true); return err }},
		{"手动未读", func() error { return mark.Execute(ctx, f.member, f.groupID, true) }},
		{"标为已读清除未读标记", func() error { _, err := read.Execute(ctx, f.member, f.groupID, mention.ID, true); return err }},
		{"确认提及", func() error { _, err := review.Execute(ctx, f.member, f.groupID, mention.ID); return err }},
		{"取消静音", func() error { _, err := mute.Execute(ctx, f.member, f.groupID, false); return err }},
	} {
		// 首次操作通知本人，重复同一操作不发布。
		for attempt := range 2 {
			require.NoError(t, step.change(), "%s%d", step.name, attempt)
			if attempt == 0 {
				feed.expect(t, feed.notice(f.member.User.ID, realtime.KindConversationStateChanged, f.groupID, loadConversationStateVersion(t, f.db, f.groupID, f.member.User.ID)))
			}
		}
	}
	_, err := mute.Execute(ctx, f.member, f.groupID, true)
	require.NoError(t, err)
	feed.expect(t, feed.notice(f.member.User.ID, realtime.KindConversationStateChanged, f.groupID, loadConversationStateVersion(t, f.db, f.groupID, f.member.User.ID)))
}

// TestRealtimeIdentityProfileNotifications 验证身份资料与账户偏好实际变化时只通知资料所属用户。
func TestRealtimeIdentityProfileNotifications(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	workStatus := useraction.NewUpdateWorkStatusAction(f.db, testEnqueuer)
	preferences := useraction.NewUpdatePreferencesAction(f.db)
	updateUser := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), testEnqueuer)
	for _, step := range []struct {
		name      string
		reception bool
		change    func() error
	}{
		{"工作状态", true, func() error {
			_, err := workStatus.Execute(ctx, f.member, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusAway})
			return err
		}},
		{"账户偏好", false, func() error {
			_, err := preferences.Execute(ctx, f.member, useraction.PreferencesInput{Locale: domain.Locale(f.member.Account.Locale), TimeZone: "Asia/Shanghai", MessageNotificationsEnabled: f.member.User.MessageNotificationsEnabled})
			return err
		}},
		// 更新输入未开启接待，管理员保存时关闭该成员的接待开关。
		{"管理员关闭接待", true, func() error {
			_, err := updateUser.Execute(ctx, f.owner, f.member.User.ID, useraction.UpdateInput{DisplayName: "成员", RoleID: f.member.User.RoleID})
			return err
		}},
	} {
		// 首次保存通知资料所属用户，工作状态或接待开关变化另通知网站访客重新读取接待状态；重复保存不发布。
		for attempt := range 2 {
			require.NoError(t, step.change(), "%s%d", step.name, attempt)
			if attempt == 0 {
				want := []receivedNotification{feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID))}
				if step.reception {
					want = append(want, feed.reception())
				}
				feed.expect(t, want...)
			}
		}
	}
	_, err := workStatus.Execute(ctx, f.member, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusOffDuty})
	require.NoError(t, err)
	feed.expect(t, feed.notice(f.member.User.ID, realtime.KindIdentityProfileChanged, "", loadProfileVersion(t, f.db, f.member.User.ID)), feed.reception())
}

// TestRealtimeGroupMembershipNotifications 验证群创建、资料与成员关系变化通知变更前后的真人受众，被移出者只收会话失权通知。
func TestRealtimeGroupMembershipNotifications(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	third := newChatLockUser(t, f.db, f.owner)
	coordinator := newGroupAgentCoordinator(f.db)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)

	group, err := groupchataction.NewCreateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationInput{
		Title: "成员变化群", MemberIdentityIDs: []string{f.member.WorkspaceIdentity.ID, third.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	// changed 构造指定成员在群当前版本上的会话变更通知。
	changed := func(members ...*servermodels.Identity) []receivedNotification {
		version := loadConversationVersion(t, f.db, group.ID)
		return arr.Map(members, func(member *servermodels.Identity) receivedNotification {
			return feed.notice(member.User.ID, realtime.KindConversationChanged, group.ID, version)
		})
	}
	// actorRead 构造系统事件推进操作人阅读水位后的本人会话状态通知。
	actorRead := func(actor *servermodels.Identity) receivedNotification {
		return feed.notice(actor.User.ID, realtime.KindConversationStateChanged, group.ID, loadConversationStateVersion(t, f.db, group.ID, actor.User.ID))
	}

	// 建群以初始版本通知全部真人成员。
	require.Equal(t, int64(1), loadConversationVersion(t, f.db, group.ID), "created group version")
	feed.expect(t, changed(f.owner, f.member, third)...)

	// 只改简介不追加系统消息，仍按新版本通知全部成员，只带参与方变化。
	_, err = groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{
		ConversationID: group.ID, Title: "成员变化群", Description: "只改简介",
	})
	require.NoError(t, err)
	profileOnly := changed(f.owner, f.member, third)
	for index := range profileOnly {
		profileOnly[index] = profileOnly[index].withChanges(domain.ConversationChangeParticipants)
	}
	feed.expect(t, profileOnly...)

	// 改名同时推进资料版本并追加系统事件，同一事务的通知合并为最高版本并带上两类变化。
	_, err = groupchataction.NewUpdateGroupConversationAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationProfileInput{
		ConversationID: group.ID, Title: "改名后的群", Description: "只改简介",
	})
	require.NoError(t, err)
	renamed := changed(f.owner, f.member, third)
	for index := range renamed {
		renamed[index] = renamed[index].withChanges(domain.ConversationChangeTimeline | domain.ConversationChangeParticipants)
	}
	feed.expect(t, append(renamed, actorRead(f.owner))...)

	// 移出成员后仍在群内的成员收到变更，被移出者只收到失权通知。
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, coordinator).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
		ConversationID: group.ID, MemberIdentityID: third.WorkspaceIdentity.ID,
	})
	require.NoError(t, err)
	feed.expect(t, append(changed(f.owner, f.member), feed.removed(third.User.ID, group.ID), actorRead(f.owner))...)

	// 重新加入是新的有效关系，重入者与原成员一起收到变更。
	_, err = groupchataction.NewAddGroupConversationMembersAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationMembersInput{
		ConversationID: group.ID, MemberIdentityIDs: []string{third.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	feed.expect(t, append(changed(f.owner, f.member, third), actorRead(f.owner))...)

	// 主动退出的成员收到失权通知，并作为系统事件操作人收到本人阅读水位通知；其余成员收到变更。
	require.NoError(t, groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, third, group.ID))
	feed.expect(t, append(changed(f.owner, f.member), feed.removed(third.User.ID, group.ID), actorRead(third))...)

	// 转让群主通知全部当前成员。
	_, err = groupchataction.NewTransferGroupConversationOwnerAction(f.db).Execute(ctx, f.owner, groupchataction.GroupConversationOwnerInput{
		ConversationID: group.ID, OwnerIdentityID: f.member.WorkspaceIdentity.ID,
	})
	require.NoError(t, err)
	feed.expect(t, append(changed(f.owner, f.member), actorRead(f.owner))...)

	// 失去管理资格的移除失败，不留系统消息也不发布通知。
	messageCount, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", group.ID).Count(ctx)
	require.NoError(t, err)
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(f.db, testEnqueuer, coordinator).Execute(ctx, f.owner, groupchataction.GroupConversationMemberInput{
		ConversationID: group.ID, MemberIdentityID: f.member.WorkspaceIdentity.ID,
	})
	require.Error(t, err, "former owner removed the new owner")
	count, err := f.db.NewSelect().Model((*servermodels.Message)(nil)).Where("conversation_id = ?", group.ID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, messageCount, count, "failed removal messages")

	// 解散保留只读成员关系，通知当前成员而不发送失权通知。
	_, err = groupchataction.NewDissolveGroupConversationAction(f.db, coordinator).Execute(ctx, f.member, group.ID)
	require.NoError(t, err)
	feed.expect(t, append(changed(f.owner, f.member), actorRead(f.member))...)
	// 以一次本人静音收尾，暴露解散后多余的通知。
	_, err = conversationaction.NewUpdateConversationNotificationSettingsAction(f.db).Execute(ctx, f.owner, f.groupID, true)
	require.NoError(t, err)
	feed.expect(t, feed.notice(f.owner.User.ID, realtime.KindConversationStateChanged, f.groupID, loadConversationStateVersion(t, f.db, f.groupID, f.owner.User.ID)))
}

// testAgentRunNotifications 验证 AI 聊天运行开始、失败结果与崩溃恢复重入的会话变更通知，以及生成期间 AI 员工的输入状态。
func testAgentRunNotifications(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	_, failing := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	_, recovering := createAgentLockChat(t, ctx, db, identity, agentIdentityID, tasks)
	feed := startRealtimeFeed(t, identity.Workspace.ID)
	agentSubjectID := loadIdentitySubjectID(t, db, identity.Workspace.ID, agentIdentityID)

	// 排队运行开始后模型失败，开始与失败结果各推进一次版本。
	var runningVersion int64
	failRuntime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, input einorun.Feed) (agentruntime.RunResult, error) {
		if _, err := input.Claim(ctx, 1); err != nil {
			return agentruntime.RunResult{}, err
		}
		runningVersion = loadConversationVersion(t, db, failing.ConversationID)
		return agentruntime.RunResult{}, errors.New("test model failure")
	}}
	require.Error(t, newTestAgentRun(db, tasks, failRuntime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: failing.ID}), "model failure was not reported")
	feed.expect(t,
		feed.notice(identity.User.ID, realtime.KindConversationChanged, failing.ConversationID, runningVersion),
		feed.userTyping(identity.User.ID, failing.ConversationID, agentSubjectID, true),
		feed.notice(identity.User.ID, realtime.KindConversationChanged, failing.ConversationID, loadConversationVersion(t, db, failing.ConversationID)),
		feed.userTyping(identity.User.ID, failing.ConversationID, agentSubjectID, false),
	)

	// 崩溃恢复重入运行中状态不推进版本，成功结果推进一次。
	_, err := db.NewUpdate().Table("agent_runs").Set("status = ?", domain.AgentRunStatusRunning).Set("started_at = now()").Where("id = ?", recovering.ID).Exec(ctx)
	require.NoError(t, err)
	before := loadConversationVersion(t, db, recovering.ConversationID)
	successRuntime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, input einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := input.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		return agentruntime.RunResult{Content: "恢复后完成", EndSeq: claimed.EndSeq}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, successRuntime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: recovering.ID}))
	after := loadConversationVersion(t, db, recovering.ConversationID)
	require.Equal(t, before+1, after, "recovered run version")
	feed.expect(t,
		feed.userTyping(identity.User.ID, recovering.ConversationID, agentSubjectID, true),
		feed.notice(identity.User.ID, realtime.KindConversationChanged, recovering.ConversationID, after),
		feed.userTyping(identity.User.ID, recovering.ConversationID, agentSubjectID, false),
	)
}

// TestRealtimeCustomerInboxNotifications 验证客户会话收发与服务周期变化通知企业客服共享受众和访客目录受众，客服已读只通知本人。
func TestRealtimeCustomerInboxNotifications(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	coordinator := newGroupAgentCoordinator(f.db)
	claim := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer)
	reopen := servicesessionaction.NewReopenServiceSessionAction(f.db, testEnqueuer)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	visitorIdentityID := loadChannelIdentityID(t, f.db, f.conversationID)
	// changed 构造客户会话当前版本的共享受众与访客目录受众通知。
	changed := func() []receivedNotification {
		version := loadConversationVersion(t, f.db, f.conversationID)
		return []receivedNotification{feed.customerInbox(f.conversationID, version), feed.visitorDirectory(visitorIdentityID, f.conversationID, version)}
	}

	// 访客消息通知共享受众与访客目录受众，不逐客服扇出。
	_, err := f.visitorMessage(ctx, "访客追问")
	require.NoError(t, err)
	feed.expect(t, changed()...)

	// 访客上下文变化带参与方变化，上下文不变时只带时间线变化。
	visitorContext := func(pageURL string) error {
		_, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: &f.conversationID,
			ClientMessageID: uuid.NewV7().String(), Body: "换了页面", VisitorContext: &domain.VisitorContext{PageURL: pageURL},
		})
		return err
	}
	require.NoError(t, visitorContext("https://shop.example.com/pricing"))
	version := loadConversationVersion(t, f.db, f.conversationID)
	feed.expect(t, feed.customerInbox(f.conversationID, version).withChanges(domain.ConversationChangeTimeline|domain.ConversationChangeParticipants), feed.visitorDirectory(visitorIdentityID, f.conversationID, version))
	require.NoError(t, visitorContext("https://shop.example.com/pricing"))
	version = loadConversationVersion(t, f.db, f.conversationID)
	feed.expect(t, feed.customerInbox(f.conversationID, version).withChanges(domain.ConversationChangeTimeline), feed.visitorDirectory(visitorIdentityID, f.conversationID, version))

	// 领取通知共享受众。
	_, err = claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	feed.expect(t, changed()...)
	// 负责人重复领取没有变化，不推进版本。
	version = loadConversationVersion(t, f.db, f.conversationID)
	_, err = claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	require.Equal(t, version, loadConversationVersion(t, f.db, f.conversationID), "repeated claim version")

	// 负责人回复通知共享受众。
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "客服回复"})
	require.NoError(t, err)
	feed.expect(t, changed()...)

	// 内部备注只通知企业客服共享受众，不登记访客目录受众。
	_, err = servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), Body: "内部备注：等仓库确认", Visibility: domain.MessageVisibilityInternal})
	require.NoError(t, err)
	feed.expect(t, feed.customerInbox(f.conversationID, loadConversationVersion(t, f.db, f.conversationID)))

	// 转交通知共享受众；原负责人随后关闭被拒绝，不留通知。
	_, err = servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, nil, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.member.WorkspaceIdentity.ID})
	require.NoError(t, err)
	feed.expect(t, changed()...)
	_, err = closeSession.Execute(ctx, f.owner, f.conversationID)
	require.Error(t, err, "former assignee closed the transferred session")

	// 关闭与重开通知共享受众，并通知 AI 表现变化。
	_, err = closeSession.Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	feed.expect(t, append(changed(), feed.aiPerformance())...)
	_, err = reopen.Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	feed.expect(t, append(changed(), feed.aiPerformance())...)

	// 关闭后访客再发消息开启新周期并通知共享受众。
	_, err = closeSession.Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	feed.expect(t, append(changed(), feed.aiPerformance())...)
	next, err := f.visitorMessage(ctx, "新周期消息")
	require.NoError(t, err)
	require.True(t, next.OpenedNewServiceSession, "new service session result=%+v", next)
	feed.expect(t, changed()...)

	// 客服已读只通知本人。
	_, err = conversationaction.NewMarkConversationReadAction(f.db).Execute(ctx, f.member, f.conversationID, next.Message.ID, false)
	require.NoError(t, err)
	feed.expect(t, feed.notice(f.member.User.ID, realtime.KindConversationStateChanged, f.conversationID, loadConversationStateVersion(t, f.db, f.conversationID, f.member.User.ID)))
}

// TestRealtimeVisitorDirectoryNotifications 验证网站客户线程按所属渠道身份通知访客目录受众，不同访客身份互不接收。
func TestRealtimeVisitorDirectoryNotifications(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	const visitor = "web-session:0123456789abcdef0123456789abcdef"
	const otherVisitor = "web-session:fedcba9876543210fedcba9876543210"
	visitorIdentityID := loadChannelIdentityID(t, f.db, f.conversationID)

	// 同一访客在另一标签页新建线程，共享受众与本人访客目录受众各收到一条通知。
	second, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: visitor, ClientMessageID: uuid.NewV7().String(), Body: "第二个线程",
	})
	require.NoError(t, err)
	require.True(t, second.CreatedConversation, "second thread=%+v", second)
	secondVersion := loadConversationVersion(t, f.db, second.Conversation.ID)
	feed.expect(t, feed.customerInbox(second.Conversation.ID, secondVersion), feed.visitorDirectory(visitorIdentityID, second.Conversation.ID, secondVersion))

	// 另一访客身份的线程只通知其自身受众。
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: otherVisitor, ClientMessageID: uuid.NewV7().String(), Body: "另一访客的线程",
	})
	require.NoError(t, err)
	otherIdentityID := loadChannelIdentityID(t, f.db, other.Conversation.ID)
	require.NotEqual(t, visitorIdentityID, otherIdentityID, "两个访客共用渠道身份")
	otherVersion := loadConversationVersion(t, f.db, other.Conversation.ID)
	feed.expect(t, feed.customerInbox(other.Conversation.ID, otherVersion), feed.visitorDirectory(otherIdentityID, other.Conversation.ID, otherVersion))
}

// TestRealtimeChannelDeliveryNotifications 验证投递状态变化、渠道启停与更换机器人推进客户会话版本并通知共享受众，无变化的写入不推进。
func TestRealtimeChannelDeliveryNotifications(t *testing.T) {
	t.Parallel()
	f := newChannelDeliveryFixture(t)
	ctx := context.Background()
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	// changed 构造比客户会话当前版本低 offset 的共享受众通知。
	changed := func(offset int64) receivedNotification {
		return feed.customerInbox(f.conversationID, loadConversationVersion(t, f.db, f.conversationID)-offset)
	}
	// unchanged 执行一次投递并断言会话版本不变。
	unchanged := func(id string) {
		version := loadConversationVersion(t, f.db, f.conversationID)
		f.execute(t, id)
		require.Equal(t, version, loadConversationVersion(t, f.db, f.conversationID), "no-op delivery version")
	}

	// 回复与入队同事务通知一次。
	first := f.send(t, "第一条", uuid.NewV7().String())
	feed.expect(t, changed(0))
	second := f.send(t, "第二条", uuid.NewV7().String())
	feed.expect(t, changed(0))

	// 认领与发送结果各推进一次版本。
	f.sender.err = &telegramintegration.SendError{Code: "recipient_unavailable"}
	require.Equal(t, domain.ChannelDeliveryFailed, f.execute(t, first.ID).Status, "delivery status")
	feed.expect(t, changed(1), changed(0))

	// 人工重试通知共享受众。
	f.sender.err = nil
	require.NoError(t, deliveryaction.NewManager(f.db, nil).Resolve(ctx, f.owner, f.conversationID, first.ID, domain.ChannelDeliveryRetry, false))
	feed.expect(t, changed(0))

	// 停用渠道推进有待发送投递的会话版本，重复停用不推进。
	api := &telegramBotAPIFake{bot: telegramintegration.Bot{ID: 456, IsBot: true, FirstName: "新机器人", Username: "new_delivery_bot"}}
	runner := connectiontest.NewRunner(time.Second)
	updateStatus := telegramaction.NewUpdateChannelStatusAction(f.db, runner, api, testEnqueuer)
	_, err := updateStatus.Execute(ctx, f.owner, f.channelID, false)
	require.NoError(t, err)
	feed.expect(t, changed(0))
	version := loadConversationVersion(t, f.db, f.conversationID)
	_, err = updateStatus.Execute(ctx, f.owner, f.channelID, false)
	require.NoError(t, err)
	require.Equal(t, version, loadConversationVersion(t, f.db, f.conversationID), "repeated disable version")
	// 停用期间认领队头不写入投递，不推进版本。
	unchanged(second.ID)
	// 重新启用渠道推进版本。
	_, err = f.db.ExecContext(ctx, "UPDATE telegram_channel_settings SET webhook_base_url = 'https://example.com' WHERE channel_id = ?", f.channelID)
	require.NoError(t, err)
	_, err = updateStatus.Execute(ctx, f.owner, f.channelID, true)
	require.NoError(t, err)
	feed.expect(t, changed(0))

	// 更换机器人推进渠道内客户会话版本并通知一次。
	_, err = telegramaction.NewSaveConnectionAction(f.db, runner, api).Execute(ctx, f.owner, f.channelID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect, BotToken: "456:new_token", WebhookBaseURL: "https://example.com"})
	require.NoError(t, err)
	feed.expect(t, changed(0))
	got := f.load(t, first.ID)
	require.Equal(t, domain.ChannelDeliveryFailed, got.Status, "bot changed delivery=%+v", got)
	require.Equal(t, "account_changed", got.LastError, "bot changed delivery=%+v", got)
	// 没有待发送投递和在途运行的会话同样推进版本，旧机器人消息的回复资格随之刷新。
	version = loadConversationVersion(t, f.db, f.conversationID)
	api.bot = telegramintegration.Bot{ID: 789, IsBot: true, FirstName: "第三个机器人", Username: "third_delivery_bot"}
	_, err = telegramaction.NewSaveConnectionAction(f.db, runner, api).Execute(ctx, f.owner, f.channelID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect, BotToken: "789:third_token", WebhookBaseURL: "https://example.com"})
	require.NoError(t, err)
	feed.expect(t, changed(0))
	require.Equal(t, version+1, loadConversationVersion(t, f.db, f.conversationID), "idle bot change version")
}

// testCustomerAgentRunNotifications 验证客服 Agent 运行开始与最终回复通知企业客服共享受众和访客目录受众，生成期间两类受众都收到 AI 员工正在输入。
func testCustomerAgentRunNotifications(t *testing.T, db *bun.DB, identity *servermodels.Identity, agentIdentityID string, tasks *servertest.Tasks) {
	ctx := context.Background()
	first, _, run := createCustomerLockRun(t, ctx, db, identity, agentIdentityID, tasks)
	feed := startRealtimeFeed(t, identity.Workspace.ID)
	visitorIdentityID := loadChannelIdentityID(t, db, first.Conversation.ID)
	var runningVersion int64
	runtime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, input einorun.Feed) (agentruntime.RunResult, error) {
		claimed, err := input.Claim(ctx, 1)
		if err != nil {
			return agentruntime.RunResult{}, err
		}
		runningVersion = loadConversationVersion(t, db, first.Conversation.ID)
		return agentruntime.RunResult{Content: "AI 最终回复", EndSeq: claimed.EndSeq}, nil
	}}
	require.NoError(t, newTestAgentRun(db, tasks, runtime, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil).Execute(ctx, agentrunaction.RunInput{RunID: run.ID}))
	finalVersion := loadConversationVersion(t, db, first.Conversation.ID)
	// 聊天主体在运行开始时建立，运行结束后读取。
	agentSubjectID := loadIdentitySubjectID(t, db, identity.Workspace.ID, agentIdentityID)
	feed.expect(t,
		feed.customerInbox(first.Conversation.ID, runningVersion),
		feed.visitorDirectory(visitorIdentityID, first.Conversation.ID, runningVersion),
		// AI 生成期间客服与访客看到正在输入，运行结束后收到停止。
		feed.customerInboxTyping(first.Conversation.ID, agentSubjectID, true),
		feed.visitorTyping(visitorIdentityID, first.Conversation.ID, true),
		feed.customerInbox(first.Conversation.ID, finalVersion),
		feed.visitorDirectory(visitorIdentityID, first.Conversation.ID, finalVersion),
		feed.customerInboxTyping(first.Conversation.ID, agentSubjectID, false),
		feed.visitorTyping(visitorIdentityID, first.Conversation.ID, false),
	)
}
