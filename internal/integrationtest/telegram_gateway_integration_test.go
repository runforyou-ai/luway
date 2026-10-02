//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/domain"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// telegramGatewayFixture 保存网关转发测试的工作区、渠道与操作。
type telegramGatewayFixture struct {
	db        *bun.DB
	identity  *servermodels.Identity
	channelID string
	api       *telegramBotAPIFake
	save      *channelaction.SaveTelegramConnectionAction
	status    *channelaction.UpdateTelegramChannelStatusAction
	regen     *channelaction.RegenerateTelegramGatewaySecretAction
	receiver  *customerchataction.ReceiveTelegramWebhookAction
	updateID  int64
}

// newTelegramGatewayFixture 创建独立工作区与 Telegram 渠道。
func newTelegramGatewayFixture(t *testing.T) *telegramGatewayFixture {
	t.Helper()
	store, err := serverstorage.Open(context.Background(), servertest.DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db := store.DB()
	installed := installWorkspace(t, db, workspaceSpec{
		Name: "网关转发企业", DisplayName: "管理员", Email: uniqueEmail("gateway"), Password: "password123",
		Locale: domain.LocaleChineseSimplified, TimeZone: "UTC",
	})
	api := &telegramBotAPIFake{bot: telegramintegration.Bot{ID: time.Now().UnixNano(), IsBot: true, FirstName: "Gateway", Username: "gateway_bot"}}
	runner := connectiontest.NewRunner(time.Second)
	return &telegramGatewayFixture{
		db: db, identity: installed.Identity, api: api,
		channelID: newTelegramScopeChannel(t, db, installed.Identity, "Telegram 网关"),
		save:      channelaction.NewSaveTelegramConnectionAction(db, runner, api),
		status:    channelaction.NewUpdateTelegramChannelStatusAction(db, runner, api),
		regen:     channelaction.NewRegenerateTelegramGatewaySecretAction(db),
		receiver:  customerchataction.NewReceiveTelegramWebhookAction(db, agentrunaction.NewScheduler(newTestTasks(db)), domain.FileStorageBackendLocal, newTestTasks(db)),
	}
}

// connect 按指定接入方式保存连接。
func (f *telegramGatewayFixture) connect(t *testing.T, mode domain.TelegramConnectionMode) *channelaction.TelegramChannelDetail {
	t.Helper()
	detail, err := f.save.Execute(context.Background(), f.identity, f.channelID, channelaction.TelegramChannelConnectionInput{
		ConnectionMode: mode, BotToken: "123456:gateway_token", WebhookBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

// detail 读取渠道当前连接设置。
func (f *telegramGatewayFixture) detail(t *testing.T) *channelaction.TelegramChannelDetail {
	t.Helper()
	detail, err := channelaction.NewGetTelegramChannelQuery(f.db).Execute(context.Background(), f.identity, f.channelID)
	if err != nil {
		t.Fatal(err)
	}
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
	return f.receiver.Execute(context.Background(), f.channelID, customerchataction.TelegramWebhookInput{
		Secret: f.detail(t).Connection.WebhookSecret, CustomerToken: customerToken, UpdateID: messageID,
		Message: &telegramintegration.InboundMessage{
			ChatID: senderID, SenderID: senderID, MessageID: messageID, DisplayName: "TG " + strconv.FormatInt(senderID, 10),
			Body: "查询我的订单", OriginatedAt: time.Now().UTC(),
		},
	})
}

// senderIdentity 读取 Telegram 发送者的渠道身份。
func (f *telegramGatewayFixture) senderIdentity(t *testing.T, senderID int64) *servermodels.ContactChannelIdentity {
	t.Helper()
	identity := &servermodels.ContactChannelIdentity{}
	if err := f.db.NewSelect().Model(identity).
		Where("cci.channel_id = ? AND cci.external_id = ?", f.channelID, strconv.FormatInt(senderID, 10)).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return identity
}

// senderProfile 读取 Telegram 发送者会话的客户资料。
func (f *telegramGatewayFixture) senderProfile(t *testing.T, senderID int64) contactaction.CustomerProfile {
	t.Helper()
	var conversationID string
	if err := f.db.NewSelect().TableExpr("channel_conversations AS cc").Column("cc.conversation_id").
		Where("cc.contact_channel_identity_id = ?", f.senderIdentity(t, senderID).ID).
		Scan(context.Background(), &conversationID); err != nil {
		t.Fatal(err)
	}
	profile, err := contactaction.NewGetCustomerProfileQuery(f.db).Execute(context.Background(), f.identity, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

// contact 读取联系人，含已移入回收站的联系人。
func (f *telegramGatewayFixture) contact(t *testing.T, contactID string) *servermodels.Contact {
	t.Helper()
	contact := &servermodels.Contact{}
	if err := f.db.NewSelect().Model(contact).Where("ct.id = ?", contactID).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return contact
}

// TestTelegramGatewayConnection 验证网关转发不注册或删除 Webhook，转发密钥跨保存与启停保留，重新生成后旧密钥失效。
func TestTelegramGatewayConnection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newTelegramGatewayFixture(t)

	saved := f.connect(t, domain.TelegramConnectionGateway)
	secret := saved.Connection.WebhookSecret
	if saved.Connection.ConnectionMode != string(domain.TelegramConnectionGateway) || secret == "" || saved.Connection.WebhookURL == "" ||
		saved.Connection.WebhookStatus == nil || *saved.Connection.WebhookStatus != string(domain.TelegramWebhookStatusWaiting) {
		t.Fatalf("网关转发连接 = %+v", saved.Connection)
	}
	if len(f.api.webhooks()) != 0 {
		t.Fatalf("网关转发注册了 Webhook：%+v", f.api.webhooks())
	}

	// 首次转发后连接正常；再次保存沿用转发密钥与连接状态。
	if err := f.receiver.Execute(ctx, f.channelID, customerchataction.TelegramWebhookInput{Secret: secret, UpdateID: 1, MyChatMember: true}); err != nil {
		t.Fatal(err)
	}
	if again := f.connect(t, domain.TelegramConnectionGateway); again.Connection.WebhookSecret != secret || again.Connection.WebhookStatus == nil ||
		*again.Connection.WebhookStatus != string(domain.TelegramWebhookStatusNormal) {
		t.Fatalf("再次保存后的连接 = %+v", again.Connection)
	}

	// 停用与启用不删除也不注册 Webhook，转发密钥保持不变。
	if _, err := f.status.Execute(ctx, f.identity, f.channelID, false); err != nil {
		t.Fatal(err)
	}
	if disabled := f.detail(t); disabled.Connection.WebhookSecret != secret || disabled.Connection.WebhookStatus != nil {
		t.Fatalf("停用后的连接 = %+v", disabled.Connection)
	}
	if _, err := f.status.Execute(ctx, f.identity, f.channelID, true); err != nil {
		t.Fatal(err)
	}
	if enabled := f.detail(t); enabled.Connection.WebhookSecret != secret || enabled.Connection.WebhookStatus == nil ||
		*enabled.Connection.WebhookStatus != string(domain.TelegramWebhookStatusWaiting) {
		t.Fatalf("启用后的连接 = %+v", enabled.Connection)
	}
	if len(f.api.webhooks()) != 0 || len(f.api.deletedTokens()) != 0 {
		t.Fatalf("启停时调用了 Webhook 接口：set=%+v delete=%+v", f.api.webhooks(), f.api.deletedTokens())
	}

	// 重新生成后旧密钥立即失效。
	regenerated, err := f.regen.Execute(ctx, f.identity, f.channelID)
	if err != nil {
		t.Fatal(err)
	}
	if regenerated.Connection.WebhookSecret == "" || regenerated.Connection.WebhookSecret == secret {
		t.Fatalf("重新生成的转发密钥 = %q", regenerated.Connection.WebhookSecret)
	}
	if err := f.receiver.Preflight(ctx, f.channelID, secret); !errors.Is(err, customerchataction.ErrTelegramWebhookUnauthorized) {
		t.Fatalf("旧密钥转发 = %v", err)
	}

	// 改回直连后注册 Webhook，直连渠道没有转发密钥可重新生成。
	direct := f.connect(t, domain.TelegramConnectionDirect)
	if webhooks := f.api.webhooks(); len(webhooks) != 1 || webhooks[0].Secret != direct.Connection.WebhookSecret || webhooks[0].Secret == regenerated.Connection.WebhookSecret {
		t.Fatalf("改回直连的注册记录 = %+v", webhooks)
	}
	if _, err := f.regen.Execute(ctx, f.identity, f.channelID); !errors.Is(err, channelaction.ErrTelegramGatewayModeRequired) {
		t.Fatalf("直连重新生成转发密钥 = %v", err)
	}
}

// TestTelegramGatewayCustomerIdentity 验证网关转发附带的签名身份核验 Telegram 发送者：绑定、移到已有联系人、换绑、解绑、签名无效拒收，以及直连忽略签名身份。
func TestTelegramGatewayCustomerIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newTelegramGatewayFixture(t)
	f.connect(t, domain.TelegramConnectionGateway)
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(f.db).Execute(ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	sign := func(userID, email string) string {
		return signCustomer(t, secret, jwt.MapClaims{"sub": userID, "email": email, "exp": time.Now().Add(time.Hour).Unix()})
	}

	// 未附带签名身份时发送者为未核验的匿名联系人。
	const first, second int64 = 1001, 1002
	if err := f.forward(t, first, ""); err != nil {
		t.Fatal(err)
	}
	anonymous := f.senderIdentity(t, first)
	if anonymous.VerifiedUserID != nil || f.senderProfile(t, first).IdentityVerified {
		t.Fatalf("匿名发送者 = %+v", anonymous)
	}

	// 首次附带签名身份时当前联系人承接企业用户编号，邮箱补充为联系方式。
	userA := "tg-a-" + suffix
	if err := f.forward(t, first, sign(userA, "a@example.com")); err != nil {
		t.Fatal(err)
	}
	boundMessageID := f.updateID
	bound := f.senderIdentity(t, first)
	profile := f.senderProfile(t, first)
	if bound.ContactID != anonymous.ContactID || bound.VerifiedUserID == nil || *bound.VerifiedUserID != userA ||
		!profile.IdentityVerified || profile.ExternalUserID != userA || profile.Email != "a@example.com" {
		t.Fatalf("绑定后 identity=%+v profile=%+v", bound, profile)
	}

	// 企业用户编号已有联系人时，渠道身份连同会话移到该联系人，原匿名联系人移入回收站。
	userB := "tg-b-" + suffix
	website, err := channelaction.NewCreateMessageChannelAction(f.db).Execute(ctx, f.identity, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "官网", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	var websiteContact *servermodels.Contact
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ensured, err := contactaction.EnsureChannelIdentity(ctx, tx, contactaction.EnsureChannelIdentityInput{
			OrganizationID: f.identity.Organization.ID, ChannelID: website.ID, ExternalID: "web-user:" + userB,
			ContactID: uuid.NewV7().String(), IdentityID: uuid.NewV7().String(), VerifiedUserID: userB,
		})
		websiteContact = ensured.Contact
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.forward(t, second, ""); err != nil {
		t.Fatal(err)
	}
	beforeMove := f.senderIdentity(t, second)
	if err := f.forward(t, second, sign(userB, "")); err != nil {
		t.Fatal(err)
	}
	moved := f.senderIdentity(t, second)
	if moved.ContactID != websiteContact.ID || f.senderProfile(t, second).ContactID != websiteContact.ID {
		t.Fatalf("移动后的渠道身份 = %+v，期望联系人 %s", moved, websiteContact.ID)
	}
	if emptied := f.contact(t, beforeMove.ContactID); emptied.DeletedAt == nil {
		t.Fatalf("原匿名联系人未移入回收站：%+v", emptied)
	}
	var stale int
	if err := f.db.NewSelect().TableExpr("conversation_participants AS cp").ColumnExpr("count(*)").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id").
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = cp.conversation_id").
		Where("cc.contact_channel_identity_id = ? AND cs.kind = ? AND cs.source_id = ?", moved.ID, domain.ChatSubjectKindContact, beforeMove.ContactID).
		Scan(ctx, &stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("会话仍有 %d 条原联系人参与记录", stale)
	}

	// 换绑到没有联系人的企业用户时新建联系人承接，已关联企业用户的原联系人保留。
	userC := "tg-c-" + suffix
	if err := f.forward(t, first, sign(userC, "")); err != nil {
		t.Fatal(err)
	}
	rebound := f.senderIdentity(t, first)
	if rebound.ContactID == bound.ContactID || rebound.VerifiedUserID == nil || *rebound.VerifiedUserID != userC {
		t.Fatalf("换绑后的渠道身份 = %+v", rebound)
	}
	if previous := f.contact(t, bound.ContactID); previous.DeletedAt != nil || previous.ExternalUserID == nil || *previous.ExternalUserID != userA {
		t.Fatalf("换绑后原联系人 = %+v", previous)
	}
	if current := f.contact(t, rebound.ContactID); current.ExternalUserID == nil || *current.ExternalUserID != userC {
		t.Fatalf("换绑后新联系人 = %+v", current)
	}

	// 重放已写入的旧消息不改变当前核验身份与所属联系人，无论是否附带签名身份。
	for _, token := range []string{sign(userA, "replay@example.com"), ""} {
		if err := f.forwardMessage(t, first, boundMessageID, token); err != nil {
			t.Fatal(err)
		}
		if replayed := f.senderIdentity(t, first); replayed.ContactID != rebound.ContactID || replayed.VerifiedUserID == nil || *replayed.VerifiedUserID != userC {
			t.Fatalf("重放后的渠道身份 = %+v，期望保持 %+v", replayed, rebound)
		}
	}
	var replayEmails int
	if err := f.db.NewSelect().TableExpr("contact_methods").ColumnExpr("count(*)").Where("value = ?", "replay@example.com").Scan(ctx, &replayEmails); err != nil {
		t.Fatal(err)
	}
	if replayEmails != 0 {
		t.Fatal("重放的消息补充了签名邮箱")
	}

	// 不再附带签名身份时变回未核验，渠道身份留在原联系人。
	if err := f.forward(t, second, ""); err != nil {
		t.Fatal(err)
	}
	if unbound := f.senderIdentity(t, second); unbound.ContactID != websiteContact.ID || unbound.VerifiedUserID != nil || f.senderProfile(t, second).IdentityVerified {
		t.Fatalf("解绑后的渠道身份 = %+v", unbound)
	}

	// 签名无效时整条转发被拒收，消息不写入。
	var before int
	if err := f.db.NewSelect().TableExpr("messages").ColumnExpr("count(*)").Where("organization_id = ?", f.identity.Organization.ID).Scan(ctx, &before); err != nil {
		t.Fatal(err)
	}
	invalid := signCustomer(t, "wrong-secret", jwt.MapClaims{"sub": userA, "exp": time.Now().Add(time.Hour).Unix()})
	if err := f.forward(t, first, invalid); !errors.Is(err, conversationaction.ErrCustomerIdentityInvalid) {
		t.Fatalf("签名无效的转发 = %v", err)
	}
	var after int
	if err := f.db.NewSelect().TableExpr("messages").ColumnExpr("count(*)").Where("organization_id = ?", f.identity.Organization.ID).Scan(ctx, &after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("签名无效的转发写入了 %d 条消息", after-before)
	}

	// 直连接收的回调不采用签名身份。
	f.connect(t, domain.TelegramConnectionDirect)
	const third int64 = 1003
	if err := f.forward(t, third, sign("tg-d-"+suffix, "")); err != nil {
		t.Fatal(err)
	}
	if direct := f.senderIdentity(t, third); direct.VerifiedUserID != nil {
		t.Fatalf("直连采用了签名身份：%+v", direct)
	}
}

// TestTelegramGatewayIdentityChangeCancelsRun 验证发送者核验身份变化时取消按原身份装配的在途运行，新消息重新调度运行。
func TestTelegramGatewayIdentityChangeCancelsRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, identity, providerID, modelID := newAIWorkspace(t)
	f := newAgentTelegramFixture(t, db, identity, providerID, modelID)
	if _, err := db.ExecContext(ctx, "UPDATE telegram_channel_settings SET connection_mode = ? WHERE channel_id = ?", domain.TelegramConnectionGateway, f.channel.ID); err != nil {
		t.Fatal(err)
	}
	secret, err := customerserviceaction.NewRegenerateCustomerIdentitySecretAction(db).Execute(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}

	// 同一身份的后续消息不取消在途运行。
	f.receiveNext(t)
	f.reload(t)
	if f.run.Status == string(domain.AgentRunStatusCancelled) {
		t.Fatalf("身份未变化时运行被取消：%+v", f.run)
	}

	f.input.CustomerToken = signCustomer(t, secret, jwt.MapClaims{"sub": "tg-run-" + strconv.FormatInt(time.Now().UnixNano(), 36), "exp": time.Now().Add(time.Hour).Unix()})
	f.receiveNext(t)
	f.reload(t)
	if f.run.Status != string(domain.AgentRunStatusCancelled) || f.run.ErrorCode == nil || *f.run.ErrorCode != string(domain.AgentRunErrorCodeCustomerIdentityChanged) {
		t.Fatalf("身份变化后的原运行 = %+v", f.run)
	}
	var active int
	if err := db.NewSelect().TableExpr("agent_runs").ColumnExpr("count(*)").
		Where("lane_id = ? AND status IN (?, ?)", f.run.LaneID, domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
		Scan(ctx, &active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("身份变化后在途运行数 = %d，期望为新消息调度的 1 个", active)
	}
}
