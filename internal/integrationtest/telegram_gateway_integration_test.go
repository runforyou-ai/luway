//go:build server

package integrationtest

import (
	"context"
	"strconv"
	"testing"
	"time"
	"uuid"

	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"

	"github.com/golang-jwt/jwt/v5"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/domain"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/servertest"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// telegramGatewayFixture 保存网关转发测试的工作区、渠道与操作。
type telegramGatewayFixture struct {
	db        *bun.DB
	identity  *servermodels.Identity
	channelID string
	api       *telegramBotAPIFake
	save      *telegramaction.SaveConnectionAction
	status    *telegramaction.UpdateChannelStatusAction
	regen     *telegramaction.RegenerateGatewaySecretAction
	receiver  *telegramWebhook
	updateID  int64
}

// newTelegramGatewayFixture 创建独立工作区与 Telegram 渠道。
func newTelegramGatewayFixture(t *testing.T) *telegramGatewayFixture {
	t.Helper()
	store, err := openSharedTestDatabase(context.Background(), servertest.DatabaseConfig(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	installed := servertest.InstallWorkspace(t, db, servertest.WorkspaceSpec{
		Name: "网关转发企业", DisplayName: "管理员", Email: servertest.UniqueEmail("gateway"), Password: "password123",
		Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	api := &telegramBotAPIFake{bot: telegramintegration.Bot{ID: time.Now().UnixNano(), IsBot: true, FirstName: "Gateway", Username: "gateway_bot"}}
	runner := connectiontest.NewRunner(time.Second)
	return &telegramGatewayFixture{
		db: db, identity: installed.Identity, api: api,
		channelID: newTelegramScopeChannel(t, db, installed.Identity, "Telegram 网关"),
		save:      telegramaction.NewSaveConnectionAction(db, runner, api),
		status:    telegramaction.NewUpdateChannelStatusAction(db, runner, api, testEnqueuer),
		regen:     telegramaction.NewRegenerateGatewaySecretAction(db),
		receiver:  newTelegramWebhook(db, agentrunaction.NewScheduler(testEnqueuer), testEnqueuer),
	}
}

// connect 按指定接入方式保存连接。
func (f *telegramGatewayFixture) connect(t *testing.T, mode domain.TelegramConnectionMode) *telegramaction.ChannelDetail {
	t.Helper()
	detail, err := f.save.Execute(context.Background(), f.identity, f.channelID, telegramaction.ConnectionInput{
		ConnectionMode: mode, BotToken: "123456:gateway_token", WebhookBaseURL: "https://example.com",
	})
	require.NoError(t, err)
	return detail
}

// detail 读取渠道当前连接设置。
func (f *telegramGatewayFixture) detail(t *testing.T) *telegramaction.ChannelDetail {
	t.Helper()
	detail, err := telegramaction.NewGetChannelQuery(f.db).Execute(context.Background(), f.identity, f.channelID)
	require.NoError(t, err)
	return detail
}

// forward 以当前转发密钥转发一条新的 Telegram 私聊消息，customerToken 为空表示未附带签名身份。
func (f *telegramGatewayFixture) forward(t *testing.T, senderID int64, customerToken string) error {
	t.Helper()
	f.updateID++
	return f.forwardMessage(t, senderID, f.updateID, customerToken)
}

// forwardMessage 以当前转发密钥转发指定编号的 Telegram 私聊消息，编号与已转发的消息相同时为重放。
func (f *telegramGatewayFixture) forwardMessage(t *testing.T, senderID, messageID int64, customerToken string) error {
	t.Helper()
	return f.receiver.Execute(context.Background(), f.channelID, telegramUpdate{
		Secret: f.detail(t).Connection.WebhookSecret, CustomerToken: customerToken, UpdateID: messageID,
		Message: &telegramintegration.InboundMessage{
			ChatID: senderID, SenderID: senderID, MessageID: messageID, DisplayName: "TG " + strconv.FormatInt(senderID, 10),
			Body: "查询我的订单", OriginatedAt: time.Now().UTC(),
		},
	})
}

// senderIdentity 读取 Telegram 发送者的渠道身份。
func (f *telegramGatewayFixture) senderIdentity(t *testing.T, senderID int64) *servermodels.ChannelIdentity {
	t.Helper()
	identity := &servermodels.ChannelIdentity{}
	require.NoError(t, f.db.NewSelect().Model(identity).
		Where("ci.channel_id = ? AND ci.external_id = ?", f.channelID, strconv.FormatInt(senderID, 10)).
		Scan(context.Background()))
	return identity
}

// senderProfile 读取 Telegram 发送者会话的客户资料。
func (f *telegramGatewayFixture) senderProfile(t *testing.T, senderID int64) contactaction.CustomerProfile {
	t.Helper()
	var conversationID string
	require.NoError(t, f.db.NewSelect().TableExpr("channel_conversations AS cc").Column("cc.conversation_id").
		Where("cc.channel_identity_id = ?", f.senderIdentity(t, senderID).ID).
		Scan(context.Background(), &conversationID))
	profile, err := contactaction.NewGetCustomerProfileQuery(f.db).Execute(context.Background(), f.identity, conversationID)
	require.NoError(t, err)
	return profile
}

// contact 读取联系人，含已移入回收站的联系人。
func (f *telegramGatewayFixture) contact(t *testing.T, contactID string) *servermodels.Contact {
	t.Helper()
	contact := &servermodels.Contact{}
	require.NoError(t, f.db.NewSelect().Model(contact).Where("ct.id = ?", contactID).Scan(context.Background()))
	return contact
}

// TestTelegramGatewayConnection 验证网关转发不注册或删除 Webhook，转发密钥跨保存与启停保留，重新生成后旧密钥失效。
func TestTelegramGatewayConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newTelegramGatewayFixture(t)

	saved := f.connect(t, domain.TelegramConnectionGateway)
	secret := saved.Connection.WebhookSecret
	require.Equal(t, string(domain.TelegramConnectionGateway), saved.Connection.ConnectionMode, "网关转发连接")
	require.NotEmpty(t, secret, "网关转发连接")
	require.NotEmpty(t, saved.Connection.WebhookURL, "网关转发连接")
	require.NotNil(t, saved.Connection.WebhookStatus, "网关转发连接")
	require.Equal(t, string(domain.TelegramWebhookStatusWaiting), *saved.Connection.WebhookStatus, "网关转发连接")
	require.Empty(t, f.api.webhooks(), "网关转发注册了 Webhook")

	// 首次转发后连接正常；再次保存沿用转发密钥与连接状态。
	require.NoError(t, f.receiver.Execute(ctx, f.channelID, telegramUpdate{Secret: secret, UpdateID: 1, MyChatMember: true}))
	again := f.connect(t, domain.TelegramConnectionGateway)
	require.Equal(t, secret, again.Connection.WebhookSecret, "再次保存后的连接")
	require.NotNil(t, again.Connection.WebhookStatus, "再次保存后的连接")
	require.Equal(t, string(domain.TelegramWebhookStatusNormal), *again.Connection.WebhookStatus, "再次保存后的连接")

	// 停用与启用不删除也不注册 Webhook，转发密钥保持不变。
	_, err := f.status.Execute(ctx, f.identity, f.channelID, false)
	require.NoError(t, err)
	disabled := f.detail(t)
	require.Equal(t, secret, disabled.Connection.WebhookSecret, "停用后的连接")
	require.Nil(t, disabled.Connection.WebhookStatus, "停用后的连接")
	_, err = f.status.Execute(ctx, f.identity, f.channelID, true)
	require.NoError(t, err)
	enabled := f.detail(t)
	require.Equal(t, secret, enabled.Connection.WebhookSecret, "启用后的连接")
	require.NotNil(t, enabled.Connection.WebhookStatus, "启用后的连接")
	require.Equal(t, string(domain.TelegramWebhookStatusWaiting), *enabled.Connection.WebhookStatus, "启用后的连接")
	require.Empty(t, f.api.webhooks(), "启停时调用了 Webhook 接口")
	require.Empty(t, f.api.deletedTokens(), "启停时调用了 Webhook 接口")

	// 重新生成后旧密钥立即失效。
	regenerated, err := f.regen.Execute(ctx, f.identity, f.channelID)
	require.NoError(t, err)
	require.NotEmpty(t, regenerated.Connection.WebhookSecret, "重新生成的转发密钥")
	require.NotEqual(t, secret, regenerated.Connection.WebhookSecret, "重新生成的转发密钥")
	require.ErrorIs(t, f.receiver.Preflight(ctx, f.channelID, secret), telegramaction.ErrWebhookUnauthorized, "旧密钥转发")

	// 改回直连后注册 Webhook，直连渠道没有转发密钥可重新生成。
	direct := f.connect(t, domain.TelegramConnectionDirect)
	webhooks := f.api.webhooks()
	require.Len(t, webhooks, 1, "改回直连的注册记录")
	require.Equal(t, direct.Connection.WebhookSecret, webhooks[0].Secret, "改回直连的注册记录")
	require.NotEqual(t, regenerated.Connection.WebhookSecret, webhooks[0].Secret, "改回直连的注册记录")
	_, err = f.regen.Execute(ctx, f.identity, f.channelID)
	require.ErrorIs(t, err, telegramaction.ErrGatewayModeRequired, "直连重新生成转发密钥")
}

// TestTelegramGatewayCustomerIdentity 验证网关转发附带的签名身份核验 Telegram 发送者：绑定、移到已有联系人、换绑、解绑、签名无效拒收，以及直连忽略签名身份。
func TestTelegramGatewayCustomerIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newTelegramGatewayFixture(t)
	f.connect(t, domain.TelegramConnectionGateway)
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.identity)
	require.NoError(t, err)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	sign := func(userID, email string) string {
		return signCustomer(t, secret, jwt.MapClaims{"sub": userID, "email": email, "exp": time.Now().Add(time.Hour).Unix()})
	}

	// 未附带签名身份时发送者为未核验的匿名联系人。
	const first, second int64 = 1001, 1002
	require.NoError(t, f.forward(t, first, ""))
	anonymous := f.senderIdentity(t, first)
	require.Nil(t, anonymous.VerifiedUserID, "匿名发送者")
	require.False(t, f.senderProfile(t, first).IdentityVerified, "匿名发送者")

	// 首次附带签名身份时当前联系人承接企业用户编号，邮箱补充为联系方式。
	userA := "tg-a-" + suffix
	require.NoError(t, f.forward(t, first, sign(userA, "a@example.com")))
	boundMessageID := f.updateID
	bound := f.senderIdentity(t, first)
	profile := f.senderProfile(t, first)
	require.Equal(t, *anonymous.ContactID, *bound.ContactID, "绑定后 identity")
	require.NotNil(t, bound.VerifiedUserID, "绑定后 identity")
	require.Equal(t, userA, *bound.VerifiedUserID, "绑定后 identity")
	require.True(t, profile.IdentityVerified, "绑定后 profile")
	require.Equal(t, userA, profile.ExternalUserID, "绑定后 profile")
	require.Equal(t, "a@example.com", profile.Email, "绑定后 profile")

	// 企业用户编号已有联系人时，渠道身份连同会话移到该联系人，原匿名联系人移入回收站。
	userB := "tg-b-" + suffix
	website, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "官网", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	var websiteContact *servermodels.Contact
	require.NoError(t, f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ensured, err := contactaction.EnsureChannelIdentity(ctx, tx, contactaction.EnsureChannelIdentityInput{
			WorkspaceID: f.identity.Workspace.ID, ChannelID: website.ID, ExternalID: "web-user:" + userB,
			ContactID: uuid.NewV7().String(), IdentityID: uuid.NewV7().String(), VerifiedUserID: userB,
		})
		websiteContact = ensured.Contact
		return err
	}))
	require.NoError(t, f.forward(t, second, ""))
	beforeMove := f.senderIdentity(t, second)
	require.NoError(t, f.forward(t, second, sign(userB, "")))
	moved := f.senderIdentity(t, second)
	require.Equal(t, websiteContact.ID, *moved.ContactID, "移动后的渠道身份")
	require.Equal(t, websiteContact.ID, f.senderProfile(t, second).ContactID, "移动后的渠道身份")
	require.NotNil(t, f.contact(t, *beforeMove.ContactID).DeletedAt, "原匿名联系人未移入回收站")
	var stale int
	require.NoError(t, f.db.NewSelect().TableExpr("conversation_participants AS cp").ColumnExpr("count(*)").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id").
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = cp.conversation_id").
		Where("cc.channel_identity_id = ? AND cs.kind = ? AND cs.source_id = ?", moved.ID, domain.ChatSubjectKindContact, beforeMove.ContactID).
		Scan(ctx, &stale))
	require.Zero(t, stale, "会话仍有原联系人参与记录")

	// 换绑到没有联系人的企业用户时新建联系人承接，已关联企业用户的原联系人保留。
	userC := "tg-c-" + suffix
	require.NoError(t, f.forward(t, first, sign(userC, "")))
	rebound := f.senderIdentity(t, first)
	require.NotEqual(t, *bound.ContactID, *rebound.ContactID, "换绑后的渠道身份")
	require.NotNil(t, rebound.VerifiedUserID, "换绑后的渠道身份")
	require.Equal(t, userC, *rebound.VerifiedUserID, "换绑后的渠道身份")
	previous := f.contact(t, *bound.ContactID)
	require.Nil(t, previous.DeletedAt, "换绑后原联系人")
	require.NotNil(t, previous.ExternalUserID, "换绑后原联系人")
	require.Equal(t, userA, *previous.ExternalUserID, "换绑后原联系人")
	current := f.contact(t, *rebound.ContactID)
	require.NotNil(t, current.ExternalUserID, "换绑后新联系人")
	require.Equal(t, userC, *current.ExternalUserID, "换绑后新联系人")

	// 重放已写入的旧消息不改变当前核验身份与所属联系人，无论是否附带签名身份。
	for _, token := range []string{sign(userA, "replay@example.com"), ""} {
		require.NoError(t, f.forwardMessage(t, first, boundMessageID, token))
		replayed := f.senderIdentity(t, first)
		require.Equal(t, *rebound.ContactID, *replayed.ContactID, "重放后的渠道身份")
		require.NotNil(t, replayed.VerifiedUserID, "重放后的渠道身份")
		require.Equal(t, userC, *replayed.VerifiedUserID, "重放后的渠道身份")
	}
	var replayEmails int
	require.NoError(t, f.db.NewSelect().TableExpr("contact_methods").ColumnExpr("count(*)").Where("value = ?", "replay@example.com").Scan(ctx, &replayEmails))
	require.Zero(t, replayEmails, "重放的消息补充了签名邮箱")

	// 不再附带签名身份时变回未核验，渠道身份留在原联系人。
	require.NoError(t, f.forward(t, second, ""))
	unbound := f.senderIdentity(t, second)
	require.Equal(t, websiteContact.ID, *unbound.ContactID, "解绑后的渠道身份")
	require.Nil(t, unbound.VerifiedUserID, "解绑后的渠道身份")
	require.False(t, f.senderProfile(t, second).IdentityVerified, "解绑后的渠道身份")

	// 签名无效时整条转发被拒收，消息不写入。
	var before int
	require.NoError(t, f.db.NewSelect().TableExpr("messages").ColumnExpr("count(*)").Where("workspace_id = ?", f.identity.Workspace.ID).Scan(ctx, &before))
	invalid := signCustomer(t, "wrong-secret", jwt.MapClaims{"sub": userA, "exp": time.Now().Add(time.Hour).Unix()})
	require.ErrorIs(t, f.forward(t, first, invalid), customerchataction.ErrCustomerIdentityInvalid, "签名无效的转发")
	var after int
	require.NoError(t, f.db.NewSelect().TableExpr("messages").ColumnExpr("count(*)").Where("workspace_id = ?", f.identity.Workspace.ID).Scan(ctx, &after))
	require.Equal(t, before, after, "签名无效的转发写入了消息")

	// 直连接收的回调不采用签名身份。
	f.connect(t, domain.TelegramConnectionDirect)
	const third int64 = 1003
	require.NoError(t, f.forward(t, third, sign("tg-d-"+suffix, "")))
	require.Nil(t, f.senderIdentity(t, third).VerifiedUserID, "直连采用了签名身份")
}

// TestTelegramGatewayIdentityChangeCancelsRun 验证发送者核验身份变化时取消按原身份装配的在途运行，新消息重新调度运行。
func TestTelegramGatewayIdentityChangeCancelsRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, providerID, modelID := newAIWorkspace(t)
	f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
	_, err := db.ExecContext(ctx, "UPDATE telegram_channel_settings SET connection_mode = ? WHERE channel_id = ?", domain.TelegramConnectionGateway, f.channel.ID)
	require.NoError(t, err)
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(db).Execute(ctx, identity)
	require.NoError(t, err)

	// 同一身份的后续消息不取消在途运行。
	f.receiveNext(t)
	f.reload(t)
	require.NotEqual(t, string(domain.AgentRunStatusCancelled), f.run.Status, "身份未变化时运行被取消")

	f.input.CustomerToken = signCustomer(t, secret, jwt.MapClaims{"sub": "tg-run-" + strconv.FormatInt(time.Now().UnixNano(), 36), "exp": time.Now().Add(time.Hour).Unix()})
	f.receiveNext(t)
	f.reload(t)
	require.Equal(t, string(domain.AgentRunStatusCancelled), f.run.Status, "身份变化后的原运行")
	require.NotNil(t, f.run.ErrorCode, "身份变化后的原运行")
	require.Equal(t, string(domain.AgentRunErrorCodeCustomerIdentityChanged), *f.run.ErrorCode, "身份变化后的原运行")
	var active int
	require.NoError(t, db.NewSelect().TableExpr("agent_runs").ColumnExpr("count(*)").
		Where("lane_id = ? AND status IN (?, ?)", f.run.LaneID, domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Scan(ctx, &active))
	require.Equal(t, 1, active, "身份变化后在途运行数，期望为新消息调度的 1 个")
}
