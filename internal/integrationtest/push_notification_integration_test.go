//go:build server

package integrationtest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"uuid"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/actions/usernotification"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// testPushApp 是测试中客户端上报的推送应用标识。
const testPushApp = "net.luway.app"

// setPushDevice 把推送目标记到成员身份所用的登录会话。
func setPushDevice(t *testing.T, db *bun.DB, identity *servermodels.Identity, platform domain.PushPlatform, deviceID string) {
	t.Helper()
	account := &servermodels.AccountIdentity{Account: identity.Account, Session: identity.Session}
	require.NoError(t, authaction.NewSetPushDeviceAction(db).Execute(context.Background(), account, authaction.PushDeviceInput{
		App: testPushApp, Platform: platform, DeviceID: deviceID,
	}))
}

// sessionPushDevice 返回登录会话上登记的推送设备编号，未登记时为空。
func sessionPushDevice(t *testing.T, db *bun.DB, sessionID string) string {
	t.Helper()
	var session servermodels.AccountSession
	require.NoError(t, db.NewSelect().Model(&session).Column("push_device_id").Where("id = ?", sessionID).Scan(context.Background()))
	if session.PushDeviceID == nil {
		return ""
	}
	return *session.PushDeviceID
}

// TestSetPushDevice 验证推送设备记在当前登录会话上：无效输入被拒绝，同一设备换会话或换账号登记后只保留在最近登记的会话上。
func TestSetPushDevice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	ownerEmail := servertest.UniqueEmail("push-owner")
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "推送设备", DisplayName: "负责人", Email: ownerEmail, Password: "password123"}).Identity
	memberEmail := servertest.UniqueEmail("push-member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123").Identity
	ownerAgain := servertest.LoginMember(t, db, owner.Workspace.ID, ownerEmail, "password123").Identity

	account := &servermodels.AccountIdentity{Account: owner.Account, Session: owner.Session}
	for name, input := range map[string]authaction.PushDeviceInput{
		"missing app":      {Platform: domain.PushPlatformAndroid, DeviceID: "device"},
		"unknown platform": {App: testPushApp, Platform: "web", DeviceID: "device"},
		"comma in device":  {App: testPushApp, Platform: domain.PushPlatformAndroid, DeviceID: "a,b"},
	} {
		require.ErrorIs(t, authaction.NewSetPushDeviceAction(db).Execute(ctx, account, input), authaction.ErrPushDeviceInvalid, name)
	}

	deviceID := "device-" + uuid.NewV7().String()
	setPushDevice(t, db, owner, domain.PushPlatformAndroid, deviceID)
	require.Equal(t, deviceID, sessionPushDevice(t, db, owner.Session.ID))
	// 同一设备在同一账号的新会话登记后，只保留在新会话上。
	setPushDevice(t, db, ownerAgain, domain.PushPlatformAndroid, deviceID)
	require.Empty(t, sessionPushDevice(t, db, owner.Session.ID))
	require.Equal(t, deviceID, sessionPushDevice(t, db, ownerAgain.Session.ID))
	// 同一设备换账号登录后，只推送给新账号。
	setPushDevice(t, db, member, domain.PushPlatformAndroid, deviceID)
	require.Empty(t, sessionPushDevice(t, db, ownerAgain.Session.ID))
	require.Equal(t, deviceID, sessionPushDevice(t, db, member.Session.ID))
}

// fakePushRelay 模拟 control 的推送接口，记录收到的推送并按平台返回预设的错误码。
type fakePushRelay struct {
	mu       sync.Mutex
	requests []control.PushRequest
	failures map[string]string
}

// ServeHTTP 记录推送请求，平台有预设错误码时返回对应的错误，否则受理推送。
func (f *fakePushRelay) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/api/v1/push" || request.Header.Get("Signature") == "" {
		writeProblem(writer, http.StatusNotFound, "not_found")
		return
	}
	var input control.PushRequest
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		writeProblem(writer, http.StatusUnprocessableEntity, "validation_failed")
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, input)
	code := f.failures[input.Platform]
	f.mu.Unlock()
	switch code {
	case "":
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"message_id":"m-1"}`))
	case "service_unavailable":
		writeProblem(writer, http.StatusServiceUnavailable, code)
	case "license_required":
		writeProblem(writer, http.StatusForbidden, code)
	default:
		writeProblem(writer, http.StatusUnprocessableEntity, code)
	}
}

// take 返回并清空已收到的推送，failures 是之后各平台返回的错误码。
func (f *fakePushRelay) take(failures map[string]string) []control.PushRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	got := f.requests
	f.requests, f.failures = nil, failures
	return got
}

// pendingPushes 读取登记器中的离线推送任务输入与幂等键，按平台排序。
func pendingPushes(t *testing.T, tasks *servertest.Tasks) ([]notificationtask.PushInput, []string) {
	t.Helper()
	runs := tasks.Queued(notificationtask.PushActionName, "")
	inputs, keys := make([]notificationtask.PushInput, 0, len(runs)), make([]string, 0, len(runs))
	for _, run := range runs {
		inputs, keys = append(inputs, servertest.TaskPayload[notificationtask.PushInput](t, run)), append(keys, run.Options.IdempotencyKey)
	}
	slices.SortFunc(inputs, func(a, b notificationtask.PushInput) int {
		return strings.Compare(string(a.Platform), string(b.Platform))
	})
	slices.Sort(keys)
	return inputs, keys
}

// TestPushNotification 验证用户通知只在授权含推送中继时按接收账号的应用与平台登记离线推送；推送任务只在成员仍可接收通知、仍能阅读会话时发往仍登记在未过期会话上的设备，无法送达时结束，control 暂时不可用时返回错误重试。
func TestPushNotification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	tasks := servertest.NewTasks()
	ownerEmail := servertest.UniqueEmail("push-owner")
	owner := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "离线推送", DisplayName: "负责人", Email: ownerEmail, Password: "password123"}).Identity
	memberEmail := servertest.UniqueEmail("push-member")
	_, err := newTestMemberCreator(db, tasks).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, owner.Workspace.ID, memberEmail, "password123").Identity

	// 负责人在三台设备上登录，另有一个已过期的会话。
	setPushDevice(t, db, owner, domain.PushPlatformAndroid, "android-1")
	setPushDevice(t, db, servertest.LoginMember(t, db, owner.Workspace.ID, ownerEmail, "password123").Identity, domain.PushPlatformIOS, "ios-1")
	setPushDevice(t, db, servertest.LoginMember(t, db, owner.Workspace.ID, ownerEmail, "password123").Identity, domain.PushPlatformAndroid, "android-2")
	expired := servertest.LoginMember(t, db, owner.Workspace.ID, ownerEmail, "password123").Identity
	setPushDevice(t, db, expired, domain.PushPlatformAndroid, "android-expired")
	_, err = db.NewUpdate().Table("account_sessions").Set("expires_at = now() - interval '1 minute'").Where("id = ?", expired.Session.ID).Exec(ctx)
	require.NoError(t, err)

	deliverer := usernotification.NewDeliverer(db, tasks)
	sent, err := directchataction.NewSendFirstDirectTextMessageAction(db, testEnqueuer).Execute(ctx, member, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: owner.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "授权前",
	})
	require.NoError(t, err)
	conversationID := sent.Conversation.ID
	// 未激活授权时只下发事件流通知，不登记离线推送。
	deliverMessage(t, deliverer, owner.Workspace.ID, conversationID, sent.Message.ID)
	inputs, _ := pendingPushes(t, tasks)
	require.Empty(t, inputs)

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	identity, err := licenseaction.ControlIdentity(ctx, db)
	require.NoError(t, err)
	code := servertest.SignLicense(t, privateKey, identity.ServerID, time.Now().Add(-time.Minute), time.Now().Add(time.Hour), map[string]any{license.CapabilityPushRelay: true})
	_, err = licenseaction.NewActivateLicenseAction(db, license.Keys{servertest.LicenseKID: publicKey}, servertest.NewDeployment(t, db)).Execute(ctx, nil, code)
	require.NoError(t, err)

	// 授权含推送中继后，负责人在每个应用与平台上的设备各登记一个离线推送任务；成员是发送者，不接收通知。
	reply, err := directchataction.NewSendDirectTextMessageAction(db, testEnqueuer).Execute(ctx, member, directchataction.InternalTextMessageInput{
		ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: "授权后",
	})
	require.NoError(t, err)
	deliverMessage(t, deliverer, owner.Workspace.ID, conversationID, reply.ID)
	notification := notificationtask.PushInput{
		AccountID: owner.Account.ID, UserID: owner.User.ID, App: testPushApp, WorkspaceID: owner.Workspace.ID, NotificationID: "message:" + reply.ID,
		ConversationID: conversationID, View: domain.NotificationViewDirect, Title: "成员", Body: "授权后",
	}
	android, ios := notification, notification
	android.Platform, android.DeviceIDs = domain.PushPlatformAndroid, []string{"android-1", "android-2"}
	ios.Platform, ios.DeviceIDs = domain.PushPlatformIOS, []string{"ios-1"}
	keyPrefix := "notification-push:message:" + reply.ID + ":" + owner.Account.ID + ":" + testPushApp + ":"
	inputs, keys := pendingPushes(t, tasks)
	require.Equal(t, []notificationtask.PushInput{android, ios}, inputs)
	require.Equal(t, []string{keyPrefix + "android", keyPrefix + "ios"}, keys)
	// 任务重试再次生成同一通知时不重复登记推送。
	deliverMessage(t, deliverer, owner.Workspace.ID, conversationID, reply.ID)
	inputs, _ = pendingPushes(t, tasks)
	require.Len(t, inputs, 2)

	relay := &fakePushRelay{}
	server := httptest.NewServer(relay)
	defer server.Close()
	pusher := usernotification.NewPusher(db, control.New(server.URL, "test", func(ctx context.Context) (control.Identity, error) {
		return licenseaction.ControlIdentity(ctx, db)
	}))
	// push 执行一个推送任务并返回 control 收到的推送，failures 是之后各平台返回的错误码。
	push := func(input notificationtask.PushInput, failures map[string]string) ([]control.PushRequest, error) {
		err := pusher.Push(ctx, input)
		return relay.take(failures), err
	}
	data := map[string]string{"workspaceId": owner.Workspace.ID, "conversationId": conversationID, "view": "direct"}
	request := func(input notificationtask.PushInput, deviceIDs ...string) control.PushRequest {
		return control.PushRequest{NotificationID: input.NotificationID, App: testPushApp, Platform: string(input.Platform), DeviceIDs: deviceIDs, Title: "成员", Body: "授权后", Data: data}
	}
	got, err := push(android, map[string]string{"ios": "service_unavailable"})
	require.NoError(t, err)
	require.Equal(t, []control.PushRequest{request(android, "android-1", "android-2")}, got)
	// control 暂时不可用时只有该组返回错误由任务重试，已送达的组不受影响。
	got, err = push(ios, map[string]string{"ios": "push_rejected"})
	require.ErrorIs(t, err, control.ErrUnavailable)
	require.Equal(t, []control.PushRequest{request(ios, "ios-1")}, got)
	// 推送被拒绝或授权不含推送中继时记录警告，任务结束。
	got, err = push(ios, map[string]string{"ios": "license_required"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	got, err = push(ios, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	// 退出登录后，推送只发往任务设备中仍登记在未过期会话上的部分。
	require.NoError(t, authaction.NewLogoutAction(db).Execute(ctx, &servermodels.AccountIdentity{Account: owner.Account, Session: owner.Session}))
	got, err = push(android, nil)
	require.NoError(t, err)
	require.Equal(t, []control.PushRequest{request(android, "android-2")}, got)
	// 已退出的会话登记设备时返回会话失效。
	require.ErrorIs(t, authaction.NewSetPushDeviceAction(db).Execute(ctx, &servermodels.AccountIdentity{Account: owner.Account, Session: owner.Session}, authaction.PushDeviceInput{
		App: testPushApp, Platform: domain.PushPlatformAndroid, DeviceID: "android-2",
	}), authaction.ErrIdentityNotFound)
	// 接收成员已无法阅读该会话时不推送。
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, member, groupchataction.GroupConversationInput{
		Title: "推送群", MemberIdentityIDs: []string{owner.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	_, err = groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, newGroupAgentCoordinator(db)).Execute(ctx, member, groupchataction.GroupConversationMemberInput{
		ConversationID: group.ID, MemberIdentityID: owner.WorkspaceIdentity.ID,
	})
	require.NoError(t, err)
	removed := android
	removed.ConversationID, removed.View = group.ID, domain.NotificationViewGroup
	got, err = push(removed, nil)
	require.NoError(t, err)
	require.Empty(t, got)
	// 接收成员已不可接收通知时不推送。
	updateMember(t, db, "workspace_identities", "work_status = 'away'", owner)
	got, err = push(ios, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}
