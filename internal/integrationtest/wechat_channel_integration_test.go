//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
)

// randomWechatAppID 生成测试使用的公众号 AppID。
func randomWechatAppID() string {
	return "wx" + random.Hex(8)
}

// createWechatChannel 在身份所属工作区创建 channelType 类型的公众号渠道。
func createWechatChannel(t *testing.T, ctx context.Context, service appservice.Backend, meta appservice.RequestMeta, channelType appservice.ChannelType, name string) (string, error) {
	t.Helper()
	channel, err := service.CreateMessageChannel(ctx, meta, appservice.CreateMessageChannelInput{
		Type: channelType,
		MessageChannelInput: appservice.MessageChannelInput{
			Name: name, DefaultLocale: domain.CustomerLocaleChineseSimplified,
			NewConversationTarget: appservice.ChannelRoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			FallbackTarget:        appservice.ChannelRoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		},
	})
	return channel.ID, err
}

// createKeyChannel 在身份所属工作区创建密钥接入公众号渠道。
func createKeyChannel(t *testing.T, ctx context.Context, service appservice.Backend, meta appservice.RequestMeta, name string) string {
	t.Helper()
	channelID, err := createWechatChannel(t, ctx, service, meta, domain.ChannelTypeWechatKey, name)
	if err != nil {
		t.Fatal(err)
	}
	return channelID
}

// TestWechatKeyConnection 验证密钥接入渠道连接：凭据通过微信验证后才保存并登记 AppID，AppID 在部署内唯一且连接后不可更换，明文模式不保存 EncodingAESKey，凭据检测记录失败与恢复。
func TestWechatKeyConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := openSharedTestDatabase(ctx, servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	fake := &fakeWechatPlatform{response: `{"errcode":40164,"errmsg":"invalid ip 203.0.113.7 ipv6 ::ffff:203.0.113.7, not in whitelist"}`}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := wechat.NewClient(server.Client(), wechat.WithBaseURL(server.URL))
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db), Wechat: client},
		nil, nil, nil, testEnqueuer, nil, nil, nil)
	service := backend
	first := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "公众号甲", DisplayName: "甲", Email: servertest.UniqueEmail("wechat-a"), Password: "password123"})
	second := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{Name: "公众号乙", DisplayName: "乙", Email: servertest.UniqueEmail("wechat-b"), Password: "password123"})
	firstMeta := appservice.RequestMeta{Token: first.Token, WorkspaceID: first.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	secondMeta := appservice.RequestMeta{Token: second.Token, WorkspaceID: second.Identity.Workspace.ID, Locale: domain.LocaleChineseSimplified}
	channelID := createKeyChannel(t, ctx, service, firstMeta, "服务号")
	siblingID := createKeyChannel(t, ctx, service, firstMeta, "服务号二")
	otherID := createKeyChannel(t, ctx, service, secondMeta, "其他工作区")
	appID := randomWechatAppID()
	input := appservice.WechatKeyConnectionInput{AppID: appID, AppSecret: "secret-1", Token: "callbacktoken", EncryptionMode: domain.WechatEncryptionSafe, EncodingAESKey: testComponentAESKey}

	// 尚未连接时只有服务器出口。
	channel, err := service.GetWechatKeyChannel(ctx, firstMeta, channelID)
	if err != nil || channel.Connection.AppID != "" || channel.Connection.Servers == nil {
		t.Fatalf("initial channel = %#v, err = %v", channel.Connection, err)
	}
	if _, err := service.CheckWechatKeyChannelConnection(ctx, firstMeta, channelID); err == nil {
		t.Fatal("check unconnected channel succeeded")
	} else {
		servertest.RequireErrorMessage(t, err, i18n.ErrorWechatChannelNotConnected)
	}

	// 格式错误与安全模式缺少密钥时返回字段校验错误，不请求微信。
	_, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, channelID, appservice.WechatKeyConnectionInput{Token: "t", EncryptionMode: domain.WechatEncryptionSafe})
	servertest.RequireErrorMessage(t, err, i18n.ErrorValidationFailed)
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || appErr.Fields["appSecret"] == "" || appErr.Fields["token"] == "" {
		t.Fatalf("validation error = %#v", err)
	}
	_, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, channelID, appservice.WechatKeyConnectionInput{AppID: "wx1", AppSecret: "secret", Token: "token", EncryptionMode: domain.WechatEncryptionSafe})
	servertest.RequireErrorMessage(t, err, i18n.ErrorValidationFailed)
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || appErr.Fields["appId"] == "" || appErr.Fields["encodingAesKey"] == "" {
		t.Fatalf("validation error = %#v", err)
	}
	if fake.calls.Load() != 0 {
		t.Fatalf("wechat calls after validation = %d", fake.calls.Load())
	}

	// 出口 IP 未加入白名单时提示调用方 IP，渠道保持未连接。
	_, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, channelID, input)
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || !strings.Contains(appErr.Message, "203.0.113.7") {
		t.Fatalf("whitelist error = %#v", err)
	}
	if channel, err = service.GetWechatKeyChannel(ctx, firstMeta, channelID); err != nil || channel.Connection.AppID != "" {
		t.Fatalf("channel after rejected save = %#v, err = %v", channel.Connection, err)
	}

	// 凭据通过验证后保存密钥接入凭据、登记 AppID 并写入接口调用凭据。
	fake.set(`{"access_token":"account-token-1","expires_in":7200}`, 0)
	channel, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, channelID, input)
	if err != nil {
		t.Fatal(err)
	}
	connection := channel.Connection
	if connection.AppID != appID ||
		connection.EncryptionMode == nil || *connection.EncryptionMode != domain.WechatEncryptionSafe || connection.EncodingAESKey != testComponentAESKey ||
		connection.AccessTokenExpiresAt == nil || connection.TokenFailure != nil {
		t.Fatalf("connected channel = %#v", connection)
	}
	calls := fake.calls.Load()
	token, err := wechataction.KeyAccessToken(ctx, db, client, appID, false)
	if err != nil || token != "account-token-1" || fake.calls.Load() != calls {
		t.Fatalf("account token = %q, err = %v, calls = %d", token, err, fake.calls.Load()-calls)
	}

	// 同一公众号不能连接到本部署的其他密钥接入渠道，已连接的渠道不能更换 AppID。
	_, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, siblingID, input)
	servertest.RequireErrorMessage(t, err, i18n.ErrorWechatAppIDTaken)
	_, err = service.SaveWechatKeyChannelConnection(ctx, secondMeta, otherID, input)
	servertest.RequireErrorMessage(t, err, i18n.ErrorWechatAppIDTaken)
	if _, err := service.GetWechatKeyChannel(ctx, secondMeta, channelID); err == nil {
		t.Fatal("read channel of another workspace succeeded")
	}
	changed := input
	changed.AppID = randomWechatAppID()
	_, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, channelID, changed)
	servertest.RequireErrorMessage(t, err, i18n.ErrorValidationFailed)
	if appErr, ok := errors.AsType[*appservice.Error](err); !ok || appErr.Fields["appId"] == "" {
		t.Fatalf("immutable app id error = %#v", err)
	}

	// 改为明文模式时不保存 EncodingAESKey。
	plain := input
	plain.EncryptionMode, plain.EncodingAESKey = domain.WechatEncryptionPlain, testComponentAESKey
	if channel, err = service.SaveWechatKeyChannelConnection(ctx, firstMeta, channelID, plain); err != nil ||
		channel.Connection.EncodingAESKey != "" || *channel.Connection.EncryptionMode != domain.WechatEncryptionPlain {
		t.Fatalf("plain channel = %#v, err = %v", channel.Connection, err)
	}

	// 检测时微信拒绝则记录失败原因，恢复后清除失败。
	fake.set(`{"errcode":40125,"errmsg":"invalid appsecret"}`, 0)
	channel, err = service.CheckWechatKeyChannelConnection(ctx, firstMeta, channelID)
	if err != nil || channel.Connection.TokenFailure == nil || *channel.Connection.TokenFailure != domain.WechatTokenFailureRejected ||
		channel.Connection.TokenFailureDetail != "40125 invalid appsecret" {
		t.Fatalf("rejected check = %#v, err = %v", channel.Connection, err)
	}
	fake.set(`{"access_token":"account-token-2","expires_in":7200}`, 0)
	channel, err = service.CheckWechatKeyChannelConnection(ctx, firstMeta, channelID)
	if err != nil || channel.Connection.TokenFailure != nil || channel.Connection.TokenFailedAt != nil {
		t.Fatalf("recovered check = %#v, err = %v", channel.Connection, err)
	}
	if token, err := wechataction.KeyAccessToken(ctx, db, client, appID, false); err != nil || token != "account-token-2" {
		t.Fatalf("recovered token = %q, err = %v", token, err)
	}

	// 删除渠道的密钥凭据后取凭据返回未连接。
	if _, err := db.NewDelete().Model((*servermodels.WechatChannelKey)(nil)).Where("channel_id = ?", channelID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := wechataction.KeyAccessToken(ctx, db, client, appID, true); !errors.Is(err, wechataction.ErrAccountNotConnected) {
		t.Fatalf("token without key err = %v", err)
	}
}
