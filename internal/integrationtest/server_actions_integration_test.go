//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	contactaction "github.com/runforyou-ai/luway/internal/actions/contact"
	contactprofileaction "github.com/runforyou-ai/luway/internal/actions/contactprofile"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/actions/filemaintenance"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	inboxaction "github.com/runforyou-ai/luway/internal/actions/inbox"
	installationaction "github.com/runforyou-ai/luway/internal/actions/installation"
	organizationaction "github.com/runforyou-ai/luway/internal/actions/organization"
	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime/runstream"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

type testAgentRuntime struct {
	run func(context.Context, agentruntime.RunRequest, agentruntime.InputFeed) (agentruntime.RunResult, error)
}

func (r testAgentRuntime) Run(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
	return r.run(ctx, request, feed)
}

func assertDirectAgentRunStatus(t *testing.T, conversations []inboxaction.ConversationSummary, conversationID string, status domain.AgentRunStatus, preview string) {
	t.Helper()
	for _, conversation := range conversations {
		if conversation.ID != conversationID {
			continue
		}
		if conversation.Agent == nil || conversation.Type != domain.ConversationTypeAgent || conversation.Agent.AgentRunStatus == nil || *conversation.Agent.AgentRunStatus != status || conversation.Agent.Preview == nil || *conversation.Agent.Preview != preview {
			t.Fatalf("agent inbox conversation = %#v", conversation)
		}
		return
	}
	t.Fatalf("agent inbox conversation %q not found", conversationID)
}

// assertInboxConversationPresence 校验指定会话是否出现在收件箱查询结果中。
func assertInboxConversationPresence(t *testing.T, conversations []inboxaction.ConversationSummary, conversationID string, want bool) {
	t.Helper()
	found := false
	for _, conversation := range conversations {
		if conversation.ID == conversationID {
			found = true
			break
		}
	}
	if found != want {
		t.Fatalf("inbox conversation %q presence = %t, want %t: %#v", conversationID, found, want, conversations)
	}
}

// TestServerActionsWithPostgreSQL 验证服务端核心操作。
// 工作区创建与管理员登录是全局前置，留在顶层；其余按领域拆成有序子测试，
// 子测试之间存在数据依赖，必须按声明顺序执行，不可并行。
func TestServerActionsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	databaseConfig := servertest.DatabaseConfig(t)
	store, err := serverstorage.Open(context.Background(), databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 全局前置：创建工作区并校验初始状态，失败直接终止整个测试。
	db := store.DB()
	status := installationaction.NewStatusQuery(db)
	adminEmail := uniqueEmail("admin")
	installed := installWorkspace(t, db, workspaceSpec{
		Name:        "演示测试公司",
		DisplayName: "管理员",
		Email:       adminEmail,
		Password:    "password123",
		Locale:      domain.LocaleEnglishUnitedStates,
		TimeZone:    "America/New_York",
	})
	if installed.Identity.User.RoleID == "" || installed.Identity.Organization.Name != "演示测试公司" || installed.Identity.Account.Locale != "en-US" || installed.Identity.Account.TimeZone != "America/New_York" || !installed.Identity.User.MessageNotificationsEnabled || installed.Identity.OrganizationIdentity.WorkStatus != string(domain.WorkStatusWorking) {
		t.Fatalf("unexpected identity: %#v", installed.Identity)
	}
	if installed.Identity.Organization.Slug == "" || installed.Identity.User.AccountID != installed.Identity.Account.ID {
		t.Fatalf("organization slug = %q, member account = %q, account = %q", installed.Identity.Organization.Slug, installed.Identity.User.AccountID, installed.Identity.Account.ID)
	}
	if installed.Identity.User.IdentityID == "" || installed.Identity.User.IdentityID == installed.Identity.User.ID {
		t.Fatalf("user identity id = %q, user id = %q", installed.Identity.User.IdentityID, installed.Identity.User.ID)
	}
	teamCount, err := db.NewSelect().Model((*servermodels.Team)(nil)).
		Where("organization_id = ?", installed.Identity.Organization.ID).
		Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if teamCount != 0 {
		t.Fatalf("team count after installation = %d, want 0", teamCount)
	}
	teamMemberCount, err := db.NewSelect().Model((*servermodels.TeamMember)(nil)).
		Where("organization_id = ?", installed.Identity.Organization.ID).
		Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if teamMemberCount != 0 {
		t.Fatalf("team member count after installation = %d, want 0", teamMemberCount)
	}
	deploymentInstalled, err := status.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !deploymentInstalled {
		t.Fatal("deployment with accounts is not installed")
	}
	otherInstalled := installWorkspace(t, db, workspaceSpec{
		Name:        "另一家测试公司",
		DisplayName: "管理员",
		Email:       uniqueEmail("other-admin"),
		Password:    "password123",
	})
	if otherInstalled.Identity.Organization.ID == installed.Identity.Organization.ID {
		t.Fatal("different workspaces resolved to the same organization")
	}
	// 管理员账号不是另一个工作区的成员，登录会话不能解析出该工作区的成员身份。
	resolveIdentity := authaction.NewResolveIdentityQuery(db)
	if _, err := resolveIdentity.Execute(context.Background(), otherInstalled.Identity.Organization.ID, installed.Token); !errors.Is(err, authaction.ErrMembershipNotFound) {
		t.Fatalf("cross workspace identity error = %v, want ErrMembershipNotFound", err)
	}
	// 非法工作区编号按无成员身份处理，无效令牌优先返回登录会话失效。
	if _, err := resolveIdentity.Execute(context.Background(), "not-a-workspace", installed.Token); !errors.Is(err, authaction.ErrMembershipNotFound) {
		t.Fatalf("invalid workspace identity error = %v, want ErrMembershipNotFound", err)
	}
	if _, err := resolveIdentity.Execute(context.Background(), "not-a-workspace", "invalid-token"); !errors.Is(err, authaction.ErrIdentityNotFound) {
		t.Fatalf("invalid token identity error = %v, want ErrIdentityNotFound", err)
	}
	// 全局前置：解析安装令牌、登出并重新登录管理员，失败直接终止整个测试。
	identity, err := resolveIdentity.Execute(context.Background(), installed.Identity.Organization.ID, installed.Token)
	if err != nil {
		t.Fatal(err)
	}
	if identity == nil || identity.Account.Email != adminEmail {
		t.Fatalf("unexpected identity: %#v", identity)
	}
	// 单次查询解析出的成员身份与安装时建立的身份一致。
	if identity.Organization.ID != installed.Identity.Organization.ID || identity.Organization.Slug != installed.Identity.Organization.Slug ||
		identity.User.ID != installed.Identity.User.ID || identity.OrganizationIdentity.ID != installed.Identity.OrganizationIdentity.ID ||
		identity.Session.AccountID != identity.Account.ID {
		t.Fatalf("resolved identity mismatch: %#v, want %#v", identity, installed.Identity)
	}
	if _, err := useraction.NewUpdateWorkStatusAction(db, newTestTasks(db)).Execute(context.Background(), identity, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusAway}); err != nil {
		t.Fatal(err)
	}

	logout := authaction.NewLogoutAction(db)
	if err := logout.Execute(context.Background(), &servermodels.AccountIdentity{Account: identity.Account, Session: identity.Session}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveIdentity.Execute(context.Background(), installed.Identity.Organization.ID, installed.Token); !errors.Is(err, authaction.ErrIdentityNotFound) {
		t.Fatalf("logged out identity error = %v, want ErrIdentityNotFound", err)
	}

	loggedIn := loginMember(t, db, installed.Identity.Organization.ID, strings.ToUpper(adminEmail[:1])+adminEmail[1:], "password123")
	if loggedIn.Identity.User.ID != installed.Identity.User.ID {
		t.Fatalf("login user = %q, want %q", loggedIn.Identity.User.ID, installed.Identity.User.ID)
	}
	// 登录保留成员上次设置的工作状态。
	if loggedIn.Identity.OrganizationIdentity.WorkStatus != string(domain.WorkStatusAway) {
		t.Fatalf("login work status = %q, want %q", loggedIn.Identity.OrganizationIdentity.WorkStatus, domain.WorkStatusAway)
	}

	// 跨子测试共享：前面子测试创建的实体和操作在后续子测试中继续使用。
	login := authaction.NewLoginAction(db)
	profileEmail := uniqueEmail("new")
	getChannel := channelaction.NewGetWebsiteChannelQuery(db)
	updateChannel := channelaction.NewUpdateMessageChannelAction(db)
	updateProfile := useraction.NewUpdateProfileAction(db)
	var (
		channel                *channelaction.MessageChannelRecord
		telegramChannel        *channelaction.MessageChannelRecord
		telegramConversationID string
		team                   *teamaction.TeamRecord
		memberRole             *servermodels.Role
		createdMember          *useraction.User
		resolvedAfterUpdate    *servermodels.Identity
	)
	// runStep 在子测试失败时立即终止整个测试。
	runStep := func(name string, step func(t *testing.T)) {
		t.Helper()
		if !t.Run(name, step) {
			t.FailNow()
		}
	}

	// 覆盖用户偏好设置更新与工作状态切换流程。
	runStep("用户偏好与工作状态", func(t *testing.T) {
		updatedPreferences, err := useraction.NewUpdatePreferencesAction(db).Execute(context.Background(), loggedIn.Identity, useraction.PreferencesInput{
			Locale:                      domain.LocaleEnglishUnitedStates,
			TimeZone:                    "America/New_York",
			MessageNotificationsEnabled: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if updatedPreferences.User.MessageNotificationsEnabled {
			t.Fatal("message notifications enabled = true, want false")
		}
		loggedIn.Identity = updatedPreferences
		resolvedPreferences, err := resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, loggedIn.Token)
		if err != nil {
			t.Fatal(err)
		}
		if resolvedPreferences == nil || resolvedPreferences.User.MessageNotificationsEnabled {
			t.Fatalf("identity after preferences update = %#v", resolvedPreferences)
		}
		updatedWorkStatus, err := useraction.NewUpdateWorkStatusAction(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusAway})
		if err != nil {
			t.Fatal(err)
		}
		if updatedWorkStatus.OrganizationIdentity.WorkStatus != string(domain.WorkStatusAway) {
			t.Fatalf("updated work status = %q, want %q", updatedWorkStatus.OrganizationIdentity.WorkStatus, domain.WorkStatusAway)
		}
		if _, err := useraction.NewUpdateWorkStatusAction(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, useraction.WorkStatusInput{WorkStatus: domain.WorkStatusWorking}); err != nil {
			t.Fatal(err)
		}
	})

	// 覆盖工作区名称更新，工作区标识保持创建时的取值。
	runStep("组织信息更新", func(t *testing.T) {
		updateOrganization := organizationaction.NewUpdateOrganizationAction(db)
		organization, err := updateOrganization.Execute(context.Background(), loggedIn.Identity, "  演示协作  ")
		if err != nil {
			t.Fatal(err)
		}
		if organization.Name != "演示协作" || organization.Slug != loggedIn.Identity.Organization.Slug {
			t.Fatalf("updated organization = %#v, want name 演示协作 and slug %q", organization, loggedIn.Identity.Organization.Slug)
		}
		_, err = updateOrganization.Execute(context.Background(), loggedIn.Identity, "")
		var validationError *organizationaction.ValidationError
		if !errors.As(err, &validationError) || validationError.Fields["name"] != organizationaction.ValidationNameRequired {
			t.Fatalf("empty name error = %#v, want name required", err)
		}
		loggedIn.Identity.Organization = *organization
	})

	// 覆盖消息渠道的创建、详情、聊天界面配置、更新与启停列表流程。
	runStep("消息渠道管理", func(t *testing.T) {
		createChannel := channelaction.NewCreateMessageChannelAction(db)
		staleIdentity := *loggedIn.Identity
		staleIdentity.User = loggedIn.Identity.User
		staleIdentity.User.ID = "00000000-0000-0000-0000-000000000000"
		_, err := createChannel.Execute(context.Background(), &staleIdentity, channelaction.CreateMessageChannelInput{
			Type:                  domain.ChannelTypeWebsite,
			Name:                  "无效渠道",
			DefaultLocale:         domain.CustomerLocaleChineseSimplified,
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if !errors.Is(err, identityaction.ErrInvalid) {
			t.Fatalf("stale identity error = %v, want %v", err, identityaction.ErrInvalid)
		}

		channel, err = createChannel.Execute(context.Background(), loggedIn.Identity, channelaction.CreateMessageChannelInput{
			Type:                  domain.ChannelTypeWebsite,
			Name:                  "产品官网",
			Description:           "接收官网访客咨询",
			DefaultLocale:         domain.CustomerLocaleChineseSimplified,
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil {
			t.Fatal(err)
		}
		if channel.Type != string(domain.ChannelTypeWebsite) || channel.CreatedByUserID != loggedIn.Identity.User.ID {
			t.Fatalf("unexpected created channel: %#v", channel)
		}

		detail, err := getChannel.Execute(context.Background(), loggedIn.Identity, channel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.ChatInterface.ChatTitle != "产品官网" || detail.ChatInterface.ThemeColor != channelaction.DefaultWebsiteChannelThemeColor {
			t.Fatalf("unexpected default chat interface: %#v", detail.ChatInterface)
		}

		telegramChannel, err = createChannel.Execute(context.Background(), loggedIn.Identity, channelaction.CreateMessageChannelInput{
			Type:                  domain.ChannelTypeTelegram,
			Name:                  "Telegram 客服",
			DefaultLocale:         domain.CustomerLocaleChineseSimplified,
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil {
			t.Fatal(err)
		}
		telegramDetail, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if telegramDetail.Type != string(domain.ChannelTypeTelegram) || telegramDetail.Connection.BotToken != "" || telegramDetail.Connection.WebhookStatus != nil {
			t.Fatalf("unexpected telegram channel: %#v", telegramDetail)
		}
		telegramSettingCount, err := db.NewSelect().
			Model((*servermodels.TelegramChannelSetting)(nil)).
			Where("tcs.channel_id = ?", telegramChannel.ID).
			Count(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if telegramSettingCount != 1 {
			t.Fatalf("telegram setting count = %d, want 1", telegramSettingCount)
		}

		telegramAPI := &telegramBotAPIFake{bot: telegramintegration.Bot{
			ID: 987654321, IsBot: true, FirstName: "Demo", LastName: "Support", Username: "demo_support_bot",
		}}
		telegramRunner := connectiontest.NewRunner(time.Second)
		testTelegram := channelaction.NewTestTelegramConnectionAction(db, telegramRunner, telegramAPI)
		if err := testTelegram.Execute(context.Background(), loggedIn.Identity, telegramChannel.ID, channelaction.TelegramChannelConnectionTestInput{BotToken: "123456:draft_token"}); err != nil {
			t.Fatal(err)
		}
		detailAfterTest, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detailAfterTest.Connection.BotToken != "" || detailAfterTest.Connection.WebhookStatus != nil || len(telegramAPI.webhooks()) != 0 {
			t.Fatalf("test changed Telegram setting: %#v", detailAfterTest.Connection)
		}

		saveTelegram := channelaction.NewSaveTelegramConnectionAction(db, telegramRunner, telegramAPI)
		savedTelegram, err := saveTelegram.Execute(context.Background(), loggedIn.Identity, telegramChannel.ID, channelaction.TelegramChannelConnectionInput{
			BotToken:       "123456:saved_token",
			WebhookBaseURL: "http://127.0.0.1:34115/app",
		})
		if err != nil {
			t.Fatal(err)
		}
		if savedTelegram.Connection.BotID == nil || *savedTelegram.Connection.BotID != telegramAPI.bot.ID ||
			savedTelegram.Connection.BotUsername == nil || *savedTelegram.Connection.BotUsername != telegramAPI.bot.Username ||
			savedTelegram.Connection.BotDisplayName == nil || *savedTelegram.Connection.BotDisplayName != "Demo Support" ||
			savedTelegram.Connection.WebhookStatus == nil || *savedTelegram.Connection.WebhookStatus != string(domain.TelegramWebhookStatusWaiting) {
			t.Fatalf("saved Telegram connection = %#v", savedTelegram.Connection)
		}
		const expectedTelegramWebhookURL = "http://127.0.0.1:34115/app/api/public/telegram-channels/"
		if savedTelegram.Connection.WebhookURL != expectedTelegramWebhookURL+telegramChannel.ID+"/webhook" {
			t.Fatalf("webhook URL = %q", savedTelegram.Connection.WebhookURL)
		}
		webhooks := telegramAPI.webhooks()
		if len(webhooks) != 1 || webhooks[0].URL != savedTelegram.Connection.WebhookURL || webhooks[0].Secret != savedTelegram.Connection.WebhookSecret {
			t.Fatalf("registered webhooks = %#v, saved secret = %q", webhooks, savedTelegram.Connection.WebhookSecret)
		}

		telegramAvatarAPI := &telegramProfilePhotoAPIStub{
			photo:      &telegramintegration.ProfilePhoto{FileID: "avatar-file-1", UniqueID: "avatar-version-1"},
			downloaded: telegramintegration.DownloadedPhoto{ContentType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
		}
		importedAvatarWriter := &importedFileWriterStub{}
		telegramAvatarFiles := fileaction.NewImportAction(db, domain.FileStorageBackendLocal, importedAvatarWriter)
		receiveTelegram := customerchataction.NewReceiveTelegramWebhookAction(db, agentrunaction.NewScheduler(newTestTasks(db)), domain.FileStorageBackendLocal, newTestTasks(db))
		refreshTelegramAvatar := channelaction.NewRefreshTelegramContactAvatarAction(db, telegramAvatarAPI, telegramAvatarFiles)
		// 按任务参数执行一次头像同步，并把已投递的同步任务标记完成。
		runTelegramAvatarRefresh := func() {
			t.Helper()
			identity := servermodels.ContactChannelIdentity{}
			if err := db.NewSelect().Model(&identity).
				Where("cci.channel_id = ? AND cci.external_id = ?", telegramChannel.ID, "998877").
				Scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := refreshTelegramAvatar.Execute(context.Background(), channelaction.RefreshTelegramContactAvatarInput{
				OrganizationID: loggedIn.Identity.Organization.ID, ChannelID: telegramChannel.ID, ChannelIdentityID: identity.ID, SenderID: 998877,
			}); err != nil {
				t.Fatal(err)
			}
			// 模拟任务运行时完成本次同步任务。
			if _, err := db.ExecContext(context.Background(), "UPDATE task_runs SET status = 'succeeded', completed_at = now() WHERE action_name = ? AND idempotency_key = ?",
				channelaction.RefreshTelegramContactAvatarActionName, "tgavatar:"+identity.ID); err != nil {
				t.Fatal(err)
			}
		}
		// 统计渠道身份的头像同步任务数。
		countTelegramAvatarRefreshes := func() int {
			t.Helper()
			count, err := db.NewSelect().TableExpr("task_runs AS tr").
				Join("JOIN contact_channel_identities AS cci ON 'tgavatar:' || cci.id::text = tr.idempotency_key").
				Where("tr.action_name = ? AND cci.channel_id = ? AND cci.external_id = ?", channelaction.RefreshTelegramContactAvatarActionName, telegramChannel.ID, "998877").
				Count(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			return count
		}
		if err := receiveTelegram.Preflight(context.Background(), telegramChannel.ID, "wrong-secret"); !errors.Is(err, customerchataction.ErrTelegramWebhookUnauthorized) {
			t.Fatalf("wrong secret error = %v", err)
		}
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, customerchataction.TelegramWebhookInput{
			Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 1,
		}); err != nil {
			t.Fatalf("ignored update error = %v", err)
		}
		connectedTelegram, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if connectedTelegram.Connection.WebhookStatus == nil || *connectedTelegram.Connection.WebhookStatus != string(domain.TelegramWebhookStatusNormal) {
			t.Fatalf("status after ignored update = %#v", connectedTelegram.Connection.WebhookStatus)
		}
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, customerchataction.TelegramWebhookInput{
			Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 2, MyChatMember: true,
		}); err != nil {
			t.Fatal(err)
		}
		connectedTelegram, err = channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if connectedTelegram.Connection.WebhookStatus == nil || *connectedTelegram.Connection.WebhookStatus != string(domain.TelegramWebhookStatusNormal) {
			t.Fatalf("connected Telegram status = %#v", connectedTelegram.Connection.WebhookStatus)
		}
		telegramOriginatedAt := time.Date(2026, time.August, 30, 5, 6, 7, 0, time.UTC)
		telegramMessage := customerchataction.TelegramWebhookInput{
			Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 3,
			Message: &telegramintegration.InboundMessage{
				ChatID: 998877, MessageID: 41, SenderID: 998877,
				DisplayName: "Telegram 访客", Body: "Telegram 私聊消息",
				OriginatedAt: telegramOriginatedAt,
			},
		}
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, telegramMessage); err != nil {
			t.Fatal(err)
		}
		// 首条入站消息投递头像同步任务。
		if count := countTelegramAvatarRefreshes(); count != 1 {
			t.Fatalf("Telegram avatar refreshes after first message = %d", count)
		}
		runTelegramAvatarRefresh()
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, telegramMessage); err != nil {
			t.Fatalf("duplicate Telegram message error = %v", err)
		}
		// 头像接口失败只记录日志，保留现有头像。
		telegramAvatarAPI.err = errors.New("avatar unavailable")
		runTelegramAvatarRefresh()
		telegramAvatarAPI.err = nil
		telegramMessages := make([]servermodels.Message, 0)
		if err := db.NewSelect().Model(&telegramMessages).
			Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.organization_id = msg.organization_id").
			Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			OrderExpr("msg.message_seq ASC").
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(telegramMessages) != 1 || telegramMessages[0].Body != telegramMessage.Message.Body || telegramMessages[0].SourceOrder != telegramMessage.Message.MessageID || !telegramMessages[0].OriginatedAt.Equal(telegramOriginatedAt) {
			t.Fatalf("Telegram messages = %#v", telegramMessages)
		}
		telegramMessage.UpdateID = 4
		telegramMessage.Message.MessageID = 42
		telegramMessage.Message.DisplayName = "Telegram 新名称"
		telegramMessage.Message.Body = "同秒第二条消息"
		telegramAvatarAPI.photo = &telegramintegration.ProfilePhoto{FileID: "avatar-file-2", UniqueID: "avatar-version-2"}
		// 同步间隔内的新消息不再投递头像同步任务。
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, telegramMessage); err != nil {
			t.Fatal(err)
		}
		if count := countTelegramAvatarRefreshes(); count != 1 {
			t.Fatalf("Telegram avatar refreshes within interval = %d", count)
		}
		runTelegramAvatarRefresh()
		var telegramIdentity servermodels.ContactChannelIdentity
		if err := db.NewSelect().Model(&telegramIdentity).
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if telegramIdentity.DisplayName == nil || *telegramIdentity.DisplayName != telegramMessage.Message.DisplayName {
			t.Fatalf("Telegram identity display name = %#v", telegramIdentity.DisplayName)
		}
		if telegramIdentity.AvatarFileID == nil {
			t.Fatalf("Telegram identity avatar = %#v", telegramIdentity)
		}
		telegramAvatarFilesInDatabase := make([]servermodels.File, 0)
		if err := db.NewSelect().Model(&telegramAvatarFilesInDatabase).
			Where("f.organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("f.purpose = ?", domain.FilePurposeContactAvatar).
			OrderExpr("f.created_at ASC, f.id ASC").
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(telegramAvatarFilesInDatabase) != 2 || telegramAvatarFilesInDatabase[0].Status != string(domain.FileStatusDeleting) ||
			telegramAvatarFilesInDatabase[1].ID != *telegramIdentity.AvatarFileID || telegramAvatarFilesInDatabase[1].Status != string(domain.FileStatusActive) ||
			telegramAvatarFilesInDatabase[1].StorageBackend != string(domain.FileStorageBackendLocal) || telegramAvatarFilesInDatabase[1].ContentType != "image/jpeg" ||
			telegramAvatarFilesInDatabase[0].ExternalID == nil || *telegramAvatarFilesInDatabase[0].ExternalID != "avatar-version-1" ||
			telegramAvatarFilesInDatabase[1].ExternalID == nil || *telegramAvatarFilesInDatabase[1].ExternalID != "avatar-version-2" {
			t.Fatalf("Telegram avatar files = %#v", telegramAvatarFilesInDatabase)
		}
		if importedAvatarWriter.saved != 2 {
			t.Fatalf("imported Telegram avatar writes = %d, want 2", importedAvatarWriter.saved)
		}
		latestTelegramMessage := servermodels.Message{}
		if err := db.NewSelect().Model(&latestTelegramMessage).
			Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.organization_id = msg.organization_id").
			Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			OrderExpr("msg.message_seq DESC").
			Limit(1).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if latestTelegramMessage.Body != telegramMessage.Message.Body || latestTelegramMessage.SourceOrder != telegramMessage.Message.MessageID || !latestTelegramMessage.OriginatedAt.Equal(telegramOriginatedAt) {
			t.Fatalf("latest Telegram message = %#v", latestTelegramMessage)
		}
		telegramConversation := struct {
			ID string `bun:"id"`
		}{}
		err = db.NewSelect().
			TableExpr("channel_conversations AS cc").
			ColumnExpr("cc.conversation_id AS id").
			Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			Scan(context.Background(), &telegramConversation)
		if err != nil {
			t.Fatal(err)
		}
		telegramConversationID = telegramConversation.ID
		// 头像未变化的消息只因追加消息推进一次会话版本。
		versionBeforeSameAvatar := loadConversationVersion(t, db, telegramConversation.ID)
		sameAvatarMessage := *telegramMessage.Message
		sameAvatarMessage.MessageID = 43
		sameAvatarMessage.Body = "头像未变化的 Telegram 消息"
		// 超过同步间隔后的新消息重新投递头像同步任务。
		if _, err := db.ExecContext(context.Background(), "UPDATE contact_channel_identities SET avatar_checked_at = now() - interval '25 hours' WHERE channel_id = ? AND external_id = ?", telegramChannel.ID, "998877"); err != nil {
			t.Fatal(err)
		}
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, customerchataction.TelegramWebhookInput{
			Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 5, Message: &sameAvatarMessage,
		}); err != nil {
			t.Fatal(err)
		}
		if count := countTelegramAvatarRefreshes(); count != 2 {
			t.Fatalf("Telegram avatar refreshes after interval = %d", count)
		}
		runTelegramAvatarRefresh()
		telegramIdentity = servermodels.ContactChannelIdentity{}
		if err := db.NewSelect().Model(&telegramIdentity).
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if telegramIdentity.AvatarFileID == nil || *telegramIdentity.AvatarFileID != telegramAvatarFilesInDatabase[1].ID || importedAvatarWriter.saved != 2 {
			t.Fatalf("unchanged Telegram avatar = %#v, writes = %d", telegramIdentity, importedAvatarWriter.saved)
		}
		if version := loadConversationVersion(t, db, telegramConversation.ID); version != versionBeforeSameAvatar+1 {
			t.Fatalf("unchanged Telegram avatar conversation version = %d, want %d", version, versionBeforeSameAvatar+1)
		}
		noAvatarMessage := *telegramMessage.Message
		noAvatarMessage.MessageID = 44
		noAvatarMessage.Body = "删除头像后的 Telegram 消息"
		telegramAvatarAPI.photo = nil
		if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, customerchataction.TelegramWebhookInput{
			Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 6, Message: &noAvatarMessage,
		}); err != nil {
			t.Fatal(err)
		}
		runTelegramAvatarRefresh()
		telegramIdentity = servermodels.ContactChannelIdentity{}
		if err := db.NewSelect().Model(&telegramIdentity).
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if telegramIdentity.AvatarFileID != nil {
			t.Fatalf("deleted Telegram avatar = %#v", telegramIdentity)
		}
		// 追加消息与头像删除各推进一次会话版本。
		if version := loadConversationVersion(t, db, telegramConversation.ID); version != versionBeforeSameAvatar+3 {
			t.Fatalf("deleted Telegram avatar conversation version = %d, want %d", version, versionBeforeSameAvatar+3)
		}
		// 幂等重放带来新名称时更新渠道身份并推进会话版本，名称不变的重放不推进。
		renamedReplay := noAvatarMessage
		renamedReplay.DisplayName = "Telegram 重放名称"
		for _, step := range []struct {
			updateID int64
			want     int64
		}{{7, versionBeforeSameAvatar + 4}, {8, versionBeforeSameAvatar + 4}} {
			if err := receiveTelegram.Execute(context.Background(), telegramChannel.ID, customerchataction.TelegramWebhookInput{
				Secret: savedTelegram.Connection.WebhookSecret, UpdateID: step.updateID, Message: &renamedReplay,
			}); err != nil {
				t.Fatal(err)
			}
			if version := loadConversationVersion(t, db, telegramConversation.ID); version != step.want {
				t.Fatalf("Telegram replay %d conversation version = %d, want %d", step.updateID, version, step.want)
			}
		}
		telegramIdentity = servermodels.ContactChannelIdentity{}
		if err := db.NewSelect().Model(&telegramIdentity).
			Where("cci.channel_id = ?", telegramChannel.ID).
			Where("cci.external_id = ?", "998877").
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if telegramIdentity.DisplayName == nil || *telegramIdentity.DisplayName != "Telegram 重放名称" {
			t.Fatalf("renamed Telegram identity = %#v", telegramIdentity)
		}
		activeTelegramAvatarCount, err := db.NewSelect().Model((*servermodels.File)(nil)).
			Where("organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("purpose = ?", domain.FilePurposeContactAvatar).
			Where("status = ?", domain.FileStatusActive).
			Count(context.Background())
		if err != nil || activeTelegramAvatarCount != 0 {
			t.Fatalf("active Telegram avatar count = %d, error = %v", activeTelegramAvatarCount, err)
		}

		reusedBotChannel, err := createChannel.Execute(context.Background(), loggedIn.Identity, channelaction.CreateMessageChannelInput{
			Type:                  domain.ChannelTypeTelegram,
			Name:                  "Telegram 复用确认",
			DefaultLocale:         domain.CustomerLocaleChineseSimplified,
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil {
			t.Fatal(err)
		}
		webhookCountBeforeReuse := len(telegramAPI.webhooks())
		_, err = saveTelegram.Execute(context.Background(), loggedIn.Identity, reusedBotChannel.ID, channelaction.TelegramChannelConnectionInput{
			BotToken: "123456:reused_token", WebhookBaseURL: "http://127.0.0.1:34115/app",
		})
		if !errors.Is(err, channelaction.ErrTelegramBotReuseConfirmationRequired) {
			t.Fatalf("unconfirmed Telegram bot reuse error = %v", err)
		}
		unconfirmedReuse, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, reusedBotChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if unconfirmedReuse.Connection.BotToken != "" || len(telegramAPI.webhooks()) != webhookCountBeforeReuse {
			t.Fatalf("unconfirmed reuse changed state: detail=%#v webhooks=%#v", unconfirmedReuse.Connection, telegramAPI.webhooks())
		}
		confirmedReuse, err := saveTelegram.Execute(context.Background(), loggedIn.Identity, reusedBotChannel.ID, channelaction.TelegramChannelConnectionInput{
			BotToken: "123456:reused_token", WebhookBaseURL: "http://127.0.0.1:34115/app", ConfirmBotReuse: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if confirmedReuse.Connection.BotID == nil || *confirmedReuse.Connection.BotID != telegramAPI.bot.ID ||
			len(telegramAPI.webhooks()) != webhookCountBeforeReuse+1 ||
			telegramAPI.webhooks()[webhookCountBeforeReuse].URL != confirmedReuse.Connection.WebhookURL {
			t.Fatalf("confirmed Telegram bot reuse = %#v, webhooks = %#v", confirmedReuse.Connection, telegramAPI.webhooks())
		}
		originalAfterReuse, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if originalAfterReuse.Connection.WebhookSecret != connectedTelegram.Connection.WebhookSecret ||
			originalAfterReuse.Connection.WebhookStatus == nil || *originalAfterReuse.Connection.WebhookStatus != string(domain.TelegramWebhookStatusNormal) {
			t.Fatalf("reusing bot changed old channel = %#v", originalAfterReuse.Connection)
		}

		updateTelegramStatus := channelaction.NewUpdateTelegramChannelStatusAction(db, telegramRunner, telegramAPI)
		disabledTelegram, err := updateTelegramStatus.Execute(context.Background(), loggedIn.Identity, telegramChannel.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if disabledTelegram.Enabled {
			t.Fatal("disabled Telegram channel remains enabled")
		}
		if err := receiveTelegram.Preflight(context.Background(), telegramChannel.ID, savedTelegram.Connection.WebhookSecret); !errors.Is(err, channelaction.ErrNotFound) {
			t.Fatalf("disabled webhook preflight error = %v", err)
		}
		if len(telegramAPI.deletedTokens()) != 0 {
			t.Fatalf("deleted Telegram tokens = %#v", telegramAPI.deletedTokens())
		}

		reenabledTelegram, err := updateTelegramStatus.Execute(context.Background(), loggedIn.Identity, telegramChannel.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if !reenabledTelegram.Enabled {
			t.Fatal("re-enabled Telegram channel remains disabled")
		}
		reenabledDetail, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		if reenabledDetail.Connection.WebhookStatus == nil || *reenabledDetail.Connection.WebhookStatus != string(domain.TelegramWebhookStatusWaiting) || reenabledDetail.Connection.WebhookSecret == savedTelegram.Connection.WebhookSecret {
			t.Fatalf("re-enabled Telegram connection = %#v", reenabledDetail.Connection)
		}
		if _, err := db.NewDelete().Model((*servermodels.TelegramChannelSetting)(nil)).Where("channel_id = ?", reusedBotChannel.ID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewDelete().Model((*servermodels.Channel)(nil)).Where("id = ?", reusedBotChannel.ID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}

		startSaves := make(chan struct{})
		saveErrors := make(chan error, 2)
		for _, token := range []string{"123456:concurrent_one", "123456:concurrent_two"} {
			token := token
			go func() {
				<-startSaves
				_, err := saveTelegram.Execute(context.Background(), loggedIn.Identity, telegramChannel.ID, channelaction.TelegramChannelConnectionInput{
					BotToken: token, WebhookBaseURL: "http://127.0.0.1:34115/app",
				})
				saveErrors <- err
			}()
		}
		close(startSaves)
		for range 2 {
			if err := <-saveErrors; err != nil {
				t.Fatal(err)
			}
		}
		concurrentDetail, err := channelaction.NewGetTelegramChannelQuery(db).Execute(context.Background(), loggedIn.Identity, telegramChannel.ID)
		if err != nil {
			t.Fatal(err)
		}
		webhooks = telegramAPI.webhooks()
		if len(webhooks) < 4 || webhooks[len(webhooks)-1].Secret != concurrentDetail.Connection.WebhookSecret {
			t.Fatalf("final registered webhook = %#v, database secret = %q", webhooks, concurrentDetail.Connection.WebhookSecret)
		}
		settingCount, err := db.NewSelect().
			Model((*servermodels.WebsiteChannelSetting)(nil)).
			Where("wcs.channel_id = ?", telegramChannel.ID).
			Count(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if settingCount != 0 {
			t.Fatalf("telegram website setting count = %d, want 0", settingCount)
		}

		updateChatInterface := channelaction.NewUpdateWebsiteChannelChatInterfaceAction(db)
		chatInterface, err := updateChatInterface.Execute(context.Background(), loggedIn.Identity, channel.ID, channelaction.WebsiteChannelChatInterfaceInput{
			Title:              "在线咨询",
			GreetingMessage:    "你好，有什么可以帮你？",
			ThemeColor:         "#16a34a",
			AttachmentsEnabled: false, EmojiEnabled: true, RatingEnabled: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if chatInterface.ChatTitle != "在线咨询" || chatInterface.ThemeColor != "#16A34A" ||
			chatInterface.AttachmentsEnabled || !chatInterface.EmojiEnabled || chatInterface.RatingEnabled || chatInterface.MultipleConversationsEnabled {
			t.Fatalf("unexpected updated chat interface: %#v", chatInterface)
		}

		// 首页保存问候语、卡片顺序与链接，问候语为空时回到默认文案。
		updateHome := channelaction.NewUpdateWebsiteChannelHomeAction(db)
		blocks := []domain.WebsiteHomeBlock{{Type: domain.WebsiteHomeBlockLinks, Enabled: true}, {Type: domain.WebsiteHomeBlockStartConversation, Enabled: true}, {Type: domain.WebsiteHomeBlockRecentConversation, Enabled: false}}
		home, err := updateHome.Execute(context.Background(), loggedIn.Identity, channel.ID, channelaction.WebsiteChannelHomeInput{
			Enabled: false, Welcome: " 欢迎 ", Headline: "",
			Blocks: blocks,
			Links:  []domain.WebsiteHomeLink{{Title: " 使用文档 ", URL: " https://docs.example.com "}, {Title: "社区", URL: "https://community.example.com/join"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		links := []domain.WebsiteHomeLink{{Title: "使用文档", URL: "https://docs.example.com"}, {Title: "社区", URL: "https://community.example.com/join"}}
		if home.HomeEnabled || common.StringValue(home.HomeWelcome) != "欢迎" || home.HomeHeadline != nil || !slices.Equal(home.HomeBlocks, blocks) || !slices.Equal(home.HomeLinks, links) {
			t.Fatalf("unexpected home: %#v", home)
		}
		publicChannel, err := channelaction.NewGetPublicWebsiteChannelQuery(db).Execute(context.Background(), channel.ID)
		if err != nil || publicChannel.HomeEnabled || publicChannel.AttachmentsEnabled || publicChannel.RatingEnabled || publicChannel.MultipleConversationsEnabled || publicChannel.HelpEnabled ||
			publicChannel.HomeWelcome != "欢迎" || !slices.Equal(publicChannel.HomeBlocks, blocks) || !slices.Equal(publicChannel.HomeLinks, links) {
			t.Fatalf("public channel=%#v err=%v", publicChannel, err)
		}
		// 链接标题必填、地址只接受 HTTP(S) 绝对地址，卡片须每种各一次。
		invalidHomes := []struct {
			field string
			input channelaction.WebsiteChannelHomeInput
		}{
			{"links", channelaction.WebsiteChannelHomeInput{Blocks: blocks, Links: []domain.WebsiteHomeLink{{Title: "", URL: "https://docs.example.com"}}}},
			{"links", channelaction.WebsiteChannelHomeInput{Blocks: blocks, Links: []domain.WebsiteHomeLink{{Title: "文档", URL: "javascript:alert(1)"}}}},
			{"blocks", channelaction.WebsiteChannelHomeInput{Blocks: blocks[:2]}},
			{"blocks", channelaction.WebsiteChannelHomeInput{Blocks: []domain.WebsiteHomeBlock{blocks[0], blocks[0], blocks[1]}}},
			{"welcome", channelaction.WebsiteChannelHomeInput{Blocks: blocks, Welcome: strings.Repeat("长", 101)}},
		}
		for _, invalid := range invalidHomes {
			_, err := updateHome.Execute(context.Background(), loggedIn.Identity, channel.ID, invalid.input)
			if fieldError, ok := errors.AsType[*common.FieldError](err); !ok || fieldError.Fields[invalid.field] == "" {
				t.Fatalf("home %+v err=%v", invalid, err)
			}
		}

		channel, err = updateChannel.ExecuteBasics(context.Background(), loggedIn.Identity, channel.ID, channelaction.MessageChannelBasicsInput{
			Name:          "帮助中心",
			DefaultLocale: domain.CustomerLocaleEnglishUnitedStates,
		})
		if err != nil {
			t.Fatal(err)
		}
		if channel.Name != "帮助中心" || channel.Description != nil || channel.DefaultLocale != string(domain.LocaleEnglishUnitedStates) {
			t.Fatalf("unexpected updated channel: %#v", channel)
		}
		telegramChannel, err = updateChannel.ExecuteBasics(context.Background(), loggedIn.Identity, telegramChannel.ID, channelaction.MessageChannelBasicsInput{
			Name:          "Telegram 支持",
			DefaultLocale: domain.CustomerLocaleEnglishUnitedStates,
		})
		if err != nil {
			t.Fatal(err)
		}
		if telegramChannel.Type != string(domain.ChannelTypeTelegram) || telegramChannel.Name != "Telegram 支持" || telegramChannel.DefaultLocale != string(domain.LocaleEnglishUnitedStates) {
			t.Fatalf("unexpected updated telegram channel: %#v", telegramChannel)
		}

		updateChannelStatus := newTestChannelStatusAction(db)
		channel, err = updateChannelStatus.Execute(context.Background(), loggedIn.Identity, channel.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if channel.Enabled {
			t.Fatal("channel enabled = true, want false")
		}
		listChannels := channelaction.NewListMessageChannelsQuery(db)
		channels, err := listChannels.Execute(context.Background(), loggedIn.Identity)
		if err != nil {
			t.Fatal(err)
		}
		if len(channels) != 2 || channels[0].ID != channel.ID || channels[0].Enabled {
			t.Fatalf("unexpected disabled channels: %#v", channels)
		}
		telegramListed := false
		for _, listedChannel := range channels {
			if listedChannel.ID == telegramChannel.ID && listedChannel.Type == string(domain.ChannelTypeTelegram) {
				telegramListed = true
				break
			}
		}
		if !telegramListed {
			t.Fatalf("telegram channel missing from list: %#v", channels)
		}

		channel, err = updateChannelStatus.Execute(context.Background(), loggedIn.Identity, channel.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if !channel.Enabled {
			t.Fatal("channel enabled = false, want true")
		}
	})

	// 覆盖团队创建、成员账号管理、角色变更保护与团队成员增删流程。
	runStep("团队与成员管理", func(t *testing.T) {
		var err error
		team, err = teamaction.NewCreateTeamAction(db).Execute(context.Background(), loggedIn.Identity, teamaction.Input{Name: "客户成功", Description: "服务客户"})
		if err != nil {
			t.Fatal(err)
		}
		memberRole = &servermodels.Role{}
		if err := db.NewSelect().Model(memberRole).
			Where("organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("kind = ?", domain.RoleKindMember).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		createdMember, err = newTestMemberCreator(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, memberSpec{
			HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "团队成员", Email: uniqueEmail("member"), Password: "password123", RoleID: memberRole.ID, TeamIDs: []string{team.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(createdMember.Teams) != 1 || createdMember.Teams[0].ID != team.ID {
			t.Fatalf("created member teams = %#v", createdMember.Teams)
		}
		if createdMember.IdentityID == "" || createdMember.IdentityID == createdMember.ID {
			t.Fatalf("member identity id = %q, user id = %q", createdMember.IdentityID, createdMember.ID)
		}
		updateRoles := roleaction.NewUpdateAssignmentsAction(db)
		if err := updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{{IdentityID: loggedIn.Identity.OrganizationIdentity.ID, RoleID: memberRole.ID}}); !errors.Is(err, roleaction.ErrLastActiveAdministrator) {
			t.Fatalf("remove last active administrator error = %v", err)
		}
		administratorAfterRollback, err := useraction.NewGetUserQuery(db).Execute(context.Background(), loggedIn.Identity, loggedIn.Identity.User.ID)
		if err != nil || administratorAfterRollback.RoleID != loggedIn.Identity.User.RoleID {
			t.Fatalf("administrator after rollback = %#v, error = %v", administratorAfterRollback, err)
		}
		if err := updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{
			{IdentityID: loggedIn.Identity.OrganizationIdentity.ID, RoleID: memberRole.ID},
			{IdentityID: createdMember.IdentityID, RoleID: loggedIn.Identity.User.RoleID},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, createdMember.ID, domain.IdentityStatusInactive); !errors.Is(err, useraction.ErrLastActiveAdministrator) {
			t.Fatalf("deactivate last active administrator error = %v", err)
		}
		if err := updateRoles.Execute(context.Background(), loggedIn.Identity, []roleaction.AssignmentInput{
			{IdentityID: loggedIn.Identity.OrganizationIdentity.ID, RoleID: loggedIn.Identity.User.RoleID},
			{IdentityID: createdMember.IdentityID, RoleID: memberRole.ID},
		}); err != nil {
			t.Fatal(err)
		}
		teamUsers, err := useraction.NewListUsersQuery(db).Execute(context.Background(), loggedIn.Identity, useraction.ListInput{TeamID: team.ID, Page: 1, PageSize: 50})
		if err != nil || teamUsers.Page.Total != 1 || len(teamUsers.Users) != 1 {
			t.Fatalf("team users = %#v, error = %v", teamUsers, err)
		}
		inactiveMember, err := testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, createdMember.ID, domain.IdentityStatusInactive)
		if err != nil {
			t.Fatal(err)
		}
		if inactiveMember.WorkStatus != domain.WorkStatusOffDuty {
			t.Fatalf("inactive member work status = %q, want %q", inactiveMember.WorkStatus, domain.WorkStatusOffDuty)
		}
		inactiveTeamMembers, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, team.ID, teamaction.MemberListInput{Page: 1, PageSize: 50})
		if err != nil || inactiveTeamMembers.Page.Total != 0 || len(inactiveTeamMembers.Members) != 0 {
			t.Fatalf("team members after user deactivation = %#v, error = %v", inactiveTeamMembers, err)
		}
		teamAfterUserDeactivation, err := teamaction.NewListTeamsQuery(db).Execute(context.Background(), loggedIn.Identity, teamaction.ListInput{Page: 1, PageSize: 50})
		if err != nil || len(teamAfterUserDeactivation.Teams) != 1 || teamAfterUserDeactivation.Teams[0].MemberCount != 0 {
			t.Fatalf("team after user deactivation = %#v, error = %v", teamAfterUserDeactivation, err)
		}
		reactivatedMember, err := testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, createdMember.ID, domain.IdentityStatusActive)
		if err != nil {
			t.Fatal(err)
		}
		if reactivatedMember.WorkStatus != domain.WorkStatusOffDuty {
			t.Fatalf("reactivated member work status = %q, want %q", reactivatedMember.WorkStatus, domain.WorkStatusOffDuty)
		}
		reactivatedTeamMembers, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, team.ID, teamaction.MemberListInput{Page: 1, PageSize: 50})
		if err != nil || reactivatedTeamMembers.Page.Total != 1 || len(reactivatedTeamMembers.Members) != 1 || reactivatedTeamMembers.Members[0].IdentityID != createdMember.IdentityID {
			t.Fatalf("team members after user reactivation = %#v, error = %v", reactivatedTeamMembers, err)
		}
		teamAfterUserReactivation, err := teamaction.NewListTeamsQuery(db).Execute(context.Background(), loggedIn.Identity, teamaction.ListInput{Page: 1, PageSize: 50})
		if err != nil || len(teamAfterUserReactivation.Teams) != 1 || teamAfterUserReactivation.Teams[0].MemberCount != 1 {
			t.Fatalf("team after user reactivation = %#v, error = %v", teamAfterUserReactivation, err)
		}
		if _, err := teamaction.NewRemoveMembersAction(db).Execute(context.Background(), loggedIn.Identity, team.ID, []teamaction.MemberIdentity{{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: createdMember.IdentityID}}); err != nil {
			t.Fatal(err)
		}
		candidates, err := teamaction.NewListMemberCandidatesQuery(db).Execute(context.Background(), loggedIn.Identity, team.ID, teamaction.MemberCandidateInput{Query: createdMember.DisplayName, Page: 1, PageSize: 50})
		if err != nil || candidates.Page.Total != 1 || len(candidates.Members) != 1 || candidates.Members[0].IdentityID != createdMember.IdentityID {
			t.Fatalf("team member candidates = %#v, error = %v", candidates, err)
		}
		team, err = teamaction.NewAddMembersAction(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, team.ID, []teamaction.MemberIdentity{{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: createdMember.IdentityID}})
		if err != nil || team.MemberCount != 1 {
			t.Fatalf("team after adding member = %#v, error = %v", team, err)
		}
	})

	// 覆盖单聊首发、双方收件箱、免打扰未读和内部文本消息。
	runStep("企业成员内部单聊", func(t *testing.T) {
		memberLogin := loginMember(t, db, loggedIn.Identity.Organization.ID, createdMember.Email, "password123")

		started, err := directchataction.NewSendFirstDirectTextMessageAction(db).Execute(context.Background(), loggedIn.Identity, directchataction.FirstDirectTextMessageInput{TargetIdentityID: memberLogin.Identity.OrganizationIdentity.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f63", Body: "首发"})
		if err != nil {
			t.Fatal(err)
		}
		conversationID := started.Conversation.ID
		requests := []struct {
			identity *servermodels.Identity
			targetID string
		}{
			{identity: loggedIn.Identity, targetID: memberLogin.Identity.OrganizationIdentity.ID},
			{identity: memberLogin.Identity, targetID: loggedIn.Identity.OrganizationIdentity.ID},
		}
		findDirect := directchataction.NewFindDirectConversationQuery(db)
		for _, request := range requests {
			foundConversation, findErr := findDirect.Execute(context.Background(), request.identity, request.targetID)
			if findErr != nil || foundConversation == nil || foundConversation.ID != conversationID {
				t.Fatalf("found direct conversation = %#v, error = %v", foundConversation, findErr)
			}
		}

		inbox := inboxaction.NewLoadInboxQuery(db)
		for _, request := range requests {
			itemsPage, _, loadErr := inbox.Execute(context.Background(), request.identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
			items := itemsPage.Conversations
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			found := false
			for _, item := range items {
				if item.ID == conversationID && item.Type == domain.ConversationTypeDirect && item.Direct != nil && item.Direct.PeerIdentityID == request.targetID && item.Direct.LastMessageAt != nil {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("direct conversation missing from inbox: %#v", items)
			}
		}

		notificationSettings := conversationaction.NewUpdateConversationNotificationSettingsAction(db)
		settings, err := notificationSettings.Execute(context.Background(), loggedIn.Identity, conversationID, true)
		if err != nil || !settings.Muted {
			t.Fatalf("mute direct = %#v, error = %v", settings, err)
		}
		directItemsBeforeMessagePage, countsBeforeDirectMessage, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		directItemsBeforeMessage := directItemsBeforeMessagePage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		var unreadBeforeDirectMessage int
		for _, item := range directItemsBeforeMessage {
			if item.ID == conversationID {
				unreadBeforeDirectMessage = item.UnreadCount
			}
		}
		send := directchataction.NewSendDirectTextMessageAction(db)
		message, err := send.Execute(context.Background(), memberLogin.Identity, directchataction.InternalTextMessageInput{
			ConversationID:  conversationID,
			ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
			Body:            "你好，管理员",
		})
		if err != nil {
			t.Fatal(err)
		}
		if message.Sender == nil || message.Sender.SourceID != memberLogin.Identity.OrganizationIdentity.ID {
			t.Fatalf("direct message sender = %#v", message.Sender)
		}
		directItemsPage, countsAfterDirectMessage, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		directItems := directItemsPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		var unreadAfterDirectMessage int
		for _, item := range directItems {
			if item.ID == conversationID && (!item.Muted || item.UnreadCount != unreadBeforeDirectMessage+1) {
				t.Fatalf("muted direct unread = %#v", item)
			}
			if item.ID == conversationID {
				unreadAfterDirectMessage = item.UnreadCount
			}
		}
		if countsAfterDirectMessage.Unread != countsBeforeDirectMessage.Unread+1 || countsAfterDirectMessage.Attention != countsBeforeDirectMessage.Attention {
			t.Fatalf("muted direct counts = %#v, before = %#v", countsAfterDirectMessage, countsBeforeDirectMessage)
		}
		settings, err = notificationSettings.Execute(context.Background(), loggedIn.Identity, conversationID, false)
		if err != nil || settings.Muted {
			t.Fatalf("unmute direct = %#v, error = %v", settings, err)
		}
		_, countsAfterDirectUnmute, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		if err != nil || countsAfterDirectUnmute.Attention != countsAfterDirectMessage.Attention+unreadAfterDirectMessage {
			t.Fatalf("unmuted direct counts = %#v, error = %v", countsAfterDirectUnmute, err)
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID})
		if err != nil || len(history.Messages) != 2 {
			t.Fatalf("direct message history = %#v, error = %v", history, err)
		}
	})

	// 覆盖群聊创建、成员资料、双方收件箱、成员授权、提醒与解散归档。
	runStep("企业成员基础群聊", func(t *testing.T) {
		memberLogin := loginMember(t, db, loggedIn.Identity.Organization.ID, createdMember.Email, "password123")
		observer, err := newTestMemberCreator(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, memberSpec{
			HandlesServiceRequests: true, MaxServiceSessions: 10, DisplayName: "群聊旁观者", Email: uniqueEmail("group-observer"), Password: "password123", RoleID: memberRole.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		observerLogin := loginMember(t, db, loggedIn.Identity.Organization.ID, observer.Email, "password123")

		create := groupchataction.NewCreateGroupConversationAction(db)
		group, err := create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
			Title:             "  产品讨论  ",
			MemberIdentityIDs: []string{memberLogin.Identity.OrganizationIdentity.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if group.Title != "产品讨论" || group.MemberCount != 2 {
			t.Fatalf("group conversation = %#v", group)
		}
		if _, err := create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
			Title:             "跨企业群聊",
			MemberIdentityIDs: []string{otherInstalled.Identity.OrganizationIdentity.ID},
		}); !errors.Is(err, conversationaction.ErrGroupMemberNotFound) {
			t.Fatalf("cross-organization group member error = %v", err)
		}

		get := groupchataction.NewGetGroupConversationQuery(db)
		for _, currentIdentity := range []*servermodels.Identity{loggedIn.Identity, memberLogin.Identity} {
			detail, getErr := get.Execute(context.Background(), currentIdentity, group.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if detail.Title != group.Title || len(detail.Participants) != 2 || detail.Participants[0].IdentityID != loggedIn.Identity.OrganizationIdentity.ID || detail.Participants[0].Role != domain.ConversationParticipantRoleOwner || detail.Participants[1].IdentityID != memberLogin.Identity.OrganizationIdentity.ID || detail.Participants[1].Role != domain.ConversationParticipantRoleMember {
				t.Fatalf("group detail = %#v", detail)
			}
		}
		if _, err := get.Execute(context.Background(), otherInstalled.Identity, group.ID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("cross-organization group detail error = %v", err)
		}
		if _, err := get.Execute(context.Background(), observerLogin.Identity, group.ID); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("non-participant group detail error = %v", err)
		}

		inbox := inboxaction.NewLoadInboxQuery(db)
		notificationSettings := conversationaction.NewUpdateConversationNotificationSettingsAction(db)
		settings, err := notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, true)
		if err != nil || !settings.Muted {
			t.Fatalf("mute empty group = %#v, error = %v", settings, err)
		}
		settings, err = notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, false)
		if err != nil || settings.Muted {
			t.Fatalf("unmute empty group = %#v, error = %v", settings, err)
		}
		if _, err := notificationSettings.Execute(context.Background(), observerLogin.Identity, group.ID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("non-participant group mute error = %v", err)
		}
		if _, err := notificationSettings.Execute(context.Background(), otherInstalled.Identity, group.ID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("cross-organization group mute error = %v", err)
		}
		for _, currentIdentity := range []*servermodels.Identity{loggedIn.Identity, memberLogin.Identity} {
			itemsPage, _, loadErr := inbox.Execute(context.Background(), currentIdentity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
			items := itemsPage.Conversations
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			found := false
			for _, item := range items {
				if item.ID == group.ID && item.Type == domain.ConversationTypeGroup && item.Group != nil && item.Group.Title == group.Title && item.Group.MemberCount == 2 && item.Group.LastMessageAt == nil {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("group conversation missing from inbox: %#v", items)
			}
		}

		send := newGroupSendAction(db)
		input := groupchataction.GroupTextMessageInput{
			ConversationID:  group.ID,
			ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f67",
			Body:            "你好，群聊",
		}
		message, err := send.Execute(context.Background(), memberLogin.Identity, input)
		if err != nil {
			t.Fatal(err)
		}
		if message.Sender == nil || message.Sender.SourceID != memberLogin.Identity.OrganizationIdentity.ID {
			t.Fatalf("group message sender = %#v", message.Sender)
		}
		ownerInboxPage, _, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		ownerInbox := ownerInboxPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range ownerInbox {
			if item.ID == group.ID && (item.UnreadCount != 1 || item.LastReadMessageID != nil) {
				t.Fatalf("owner unread group = %#v", item)
			}
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
		if err != nil || len(history.Messages) != 1 || history.Messages[0].ID != message.ID || history.Messages[0].Sender == nil || history.Messages[0].Sender.SourceID != memberLogin.Identity.OrganizationIdentity.ID {
			t.Fatalf("group message history = %#v, error = %v", history, err)
		}
		groupDetail, err := get.Execute(context.Background(), loggedIn.Identity, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		var ownerSubjectID, memberSubjectID string
		for _, participant := range groupDetail.Participants {
			if participant.IdentityID == loggedIn.Identity.OrganizationIdentity.ID {
				ownerSubjectID = participant.ChatSubjectID
			}
			if participant.IdentityID == memberLogin.Identity.OrganizationIdentity.ID {
				memberSubjectID = participant.ChatSubjectID
			}
		}
		if ownerSubjectID == "" || memberSubjectID == "" {
			t.Fatalf("group member chat subject missing: %#v", groupDetail.Participants)
		}
		relationInput := groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f75",
			Body: "请看前一条", ReplyToMessageID: message.ID, MentionSubjectIDs: []string{memberSubjectID},
		}
		relationMessage, err := send.Execute(context.Background(), loggedIn.Identity, relationInput)
		if err != nil || relationMessage.ReplyTo == nil || relationMessage.ReplyTo.ID != message.ID || len(relationMessage.Mentions) != 1 || relationMessage.Mentions[0].ChatSubjectID != memberSubjectID {
			t.Fatalf("group relation message = %#v, error = %v", relationMessage, err)
		}
		memberInboxPage, _, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		memberInbox := memberInboxPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range memberInbox {
			if item.ID == group.ID && (item.UnreadCount != 1 || item.MentionedUnreadCount != 1 || item.LastReadMessageID == nil || *item.LastReadMessageID != message.ID) {
				t.Fatalf("member unread group = %#v", item)
			}
		}
		readState, err := conversationaction.NewMarkConversationReadAction(db).Execute(context.Background(), memberLogin.Identity, group.ID, relationMessage.ID, false)
		if err != nil || readState.LastReadMessageID != relationMessage.ID {
			t.Fatalf("marked group read state = %#v, error = %v", readState, err)
		}
		memberInboxPage, _, err = inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		memberInbox = memberInboxPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range memberInbox {
			if item.ID == group.ID && (item.UnreadCount != 0 || item.MentionedUnreadCount != 0 || item.LastReadMessageID == nil || *item.LastReadMessageID != relationMessage.ID) {
				t.Fatalf("member read group = %#v", item)
			}
		}
		settings, err = notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, true)
		if err != nil || !settings.Muted {
			t.Fatalf("mute group = %#v, error = %v", settings, err)
		}
		beforeAttentionItemsPage, beforeAttentionCounts, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		beforeAttentionItems := beforeAttentionItemsPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		var beforeGroupUnread int
		for _, item := range beforeAttentionItems {
			if item.ID == group.ID {
				beforeGroupUnread = item.UnreadCount
			}
		}
		ordinaryMutedMessage, err := send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f80", Body: "静音后的普通消息",
		})
		if err != nil {
			t.Fatal(err)
		}
		mentionAllInput := groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f81", Body: "请所有人查看", MentionSubjectIDs: []string{memberSubjectID}, MentionAll: true,
		}
		mentionAllMessage, err := send.Execute(context.Background(), loggedIn.Identity, mentionAllInput)
		if err != nil || !mentionAllMessage.MentionAll {
			t.Fatalf("mention all message = %#v, error = %v", mentionAllMessage, err)
		}
		afterAttentionItemsPage, afterAttentionCounts, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		afterAttentionItems := afterAttentionItemsPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range afterAttentionItems {
			if item.ID == group.ID && (!item.Muted || item.UnreadCount != beforeGroupUnread+2 || item.MentionedUnreadCount != 1) {
				t.Fatalf("muted group unread = %#v", item)
			}
		}
		if afterAttentionCounts.Unread != beforeAttentionCounts.Unread+2 || afterAttentionCounts.Attention != beforeAttentionCounts.Attention+1 {
			t.Fatalf("muted group counts = %#v, before = %#v", afterAttentionCounts, beforeAttentionCounts)
		}
		history, err = conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), memberLogin.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
		if err != nil || len(history.Messages) < 2 || history.Messages[len(history.Messages)-2].ID != ordinaryMutedMessage.ID || history.Messages[len(history.Messages)-1].ID != mentionAllMessage.ID || !history.Messages[len(history.Messages)-1].MentionAll {
			t.Fatalf("mention all history = %#v, error = %v", history, err)
		}
		settings, err = notificationSettings.Execute(context.Background(), memberLogin.Identity, group.ID, false)
		if err != nil || settings.Muted {
			t.Fatalf("unmute group = %#v, error = %v", settings, err)
		}
		_, afterGroupUnmuteCounts, err := inbox.Execute(context.Background(), memberLogin.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		if err != nil || afterGroupUnmuteCounts.Attention != afterAttentionCounts.Attention+1 {
			t.Fatalf("unmuted group counts = %#v, error = %v", afterGroupUnmuteCounts, err)
		}
		var relationConflict *conversationaction.ConflictError
		_, err = send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f76",
			Body: "无效引用", ReplyToMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f77",
		})
		if !errors.As(err, &relationConflict) || relationConflict.Reason != conversationaction.ConflictReasonReplyTargetInvalid {
			t.Fatalf("invalid group reply error = %#v", err)
		}
		_, err = send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f78",
			Body: "无效提醒", MentionSubjectIDs: []string{observerLogin.Identity.OrganizationIdentity.ID},
		})
		if !errors.As(err, &relationConflict) || relationConflict.Reason != groupchataction.ConflictReasonGroupMentionTargetInvalid {
			t.Fatalf("invalid group mention error = %#v", err)
		}
		_, err = send.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f79",
			Body: "提醒自己", MentionSubjectIDs: []string{ownerSubjectID},
		})
		if !errors.As(err, &relationConflict) || relationConflict.Reason != groupchataction.ConflictReasonGroupMentionTargetInvalid {
			t.Fatalf("self group mention error = %#v", err)
		}
		history, err = conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
		if err != nil || len(history.Messages) != 4 || history.Messages[1].ReplyTo == nil || history.Messages[1].ReplyTo.ID != message.ID || len(history.Messages[1].Mentions) != 1 || history.Messages[1].Mentions[0].ChatSubjectID != memberSubjectID || !history.Messages[3].MentionAll {
			t.Fatalf("group relation history = %#v, error = %v", history, err)
		}
		if _, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), otherInstalled.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("cross-organization group history error = %v", err)
		}
		if _, err := send.Execute(context.Background(), observerLogin.Identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f68", Body: "旁观者消息",
		}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("non-participant group send error = %v", err)
		}
		if _, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), observerLogin.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID}); !errors.Is(err, conversationaction.ErrConversationNotFound) {
			t.Fatalf("non-participant group history error = %v", err)
		}

		dissolvedGroup, err := create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
			Title: "群聊解散测试", MemberIdentityIDs: []string{memberLogin.Identity.OrganizationIdentity.ID},
		})
		if err != nil {
			t.Fatal(err)
		}
		remainingGroup, err := groupchataction.NewRemoveGroupConversationMemberAction(db, newGroupAgentCoordinator(db)).Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationMemberInput{
			ConversationID: dissolvedGroup.ID, MemberIdentityID: memberLogin.Identity.OrganizationIdentity.ID,
		})
		if err != nil || len(remainingGroup.Participants) != 1 {
			t.Fatalf("remaining group = %#v, error = %v", remainingGroup, err)
		}
		if _, err := groupchataction.NewDissolveGroupConversationAction(db, newGroupAgentCoordinator(db)).Execute(context.Background(), loggedIn.Identity, dissolvedGroup.ID); err != nil {
			t.Fatal(err)
		}
		itemsPage, _, err := inbox.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		items := itemsPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		foundDissolved := false
		for _, item := range items {
			if item.ID == dissolvedGroup.ID && item.Group != nil && item.Group.Status == domain.ConversationStatusArchived {
				foundDissolved = true
				break
			}
		}
		if !foundDissolved {
			t.Fatalf("dissolved group missing from inbox: %#v", items)
		}

		addedGroup, err := groupchataction.NewAddGroupConversationMembersAction(db).Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationMembersInput{
			ConversationID: group.ID, MemberIdentityIDs: []string{observerLogin.Identity.OrganizationIdentity.ID},
		})
		if err != nil || len(addedGroup.Participants) != 3 {
			t.Fatalf("added group member = %#v, error = %v", addedGroup, err)
		}
		observerState := &servermodels.ConversationUserState{}
		if err := db.NewSelect().Model(observerState).
			Where("organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("conversation_id = ?", group.ID).
			Where("user_id = ?", observerLogin.Identity.User.ID).
			Scan(context.Background()); err != nil || observerState.LastReadMessageID == nil || observerState.LastReadAt == nil {
			t.Fatalf("added group member read state = %#v, error = %v", observerState, err)
		}
		if _, err := conversationaction.NewMarkConversationReadAction(db).Execute(context.Background(), observerLogin.Identity, group.ID, *observerState.LastReadMessageID, false); err != nil {
			t.Fatalf("mark added group member read: %v", err)
		}

		if _, err := testUserStatusAction(db).Execute(context.Background(), loggedIn.Identity, observer.ID, domain.IdentityStatusInactive); err != nil {
			t.Fatal(err)
		}
		if _, err := create.Execute(context.Background(), loggedIn.Identity, groupchataction.GroupConversationInput{
			Title: "停用成员群聊", MemberIdentityIDs: []string{observer.IdentityID},
		}); !errors.Is(err, conversationaction.ErrGroupMemberNotFound) {
			t.Fatalf("inactive group member error = %v", err)
		}
	})

	// 覆盖 AI 员工的创建、执行配置修订、状态切换、团队与渠道联动及团队删除。
	runStep("AI员工", func(t *testing.T) {
		provider := &servermodels.AIProvider{
			OrganizationID: loggedIn.Identity.Organization.ID,
			Brand:          string(domain.AIProviderBrandOpenAI),
			Name:           "测试模型服务",
			CredentialType: string(domain.AIProviderCredentialTypeAPIKey),
			APIKey:         "test-key",
			APIURL:         "https://example.com/v1",
		}
		if _, err := db.NewInsert().Model(provider).
			Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").
			Returning("id").
			Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		model := &servermodels.AIModel{
			ProviderID: provider.ID, Identifier: "chat-model", Name: "测试对话模型", Type: string(domain.AIModelTypeChat),
			InputModalities: json.RawMessage(`["text"]`), ContextWindow: 128000, MaxOutputTokens: 4096,
		}
		insertAIModels(t, db, model)
		createdAgent, err := agentaction.NewCreateAgentAction(db).Execute(context.Background(), loggedIn.Identity, agentaction.CreateInput{
			ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, DisplayName: "接待智能体",
			TeamIDs: []string{team.ID},
			Execution: agentaction.ExecutionInput{
				Mode: domain.AgentExecutionModeManaged,
				Managed: &agentaction.ManagedExecutionInput{
					ModelID: model.ID, SystemInstruction: "负责接待客户。",
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(createdAgent.Teams) != 1 || createdAgent.Teams[0].ID != team.ID || createdAgent.CreatedAt.IsZero() || createdAgent.Execution.Managed == nil || createdAgent.Execution.Managed.Model.ID != model.ID {
			t.Fatalf("created agent = %#v", createdAgent)
		}
		serviceAssignees, err := inboxaction.NewListServiceAssigneesQuery(db).Execute(context.Background(), loggedIn.Identity)
		if err != nil {
			t.Fatal(err)
		}
		var agentListedAsCustomerService bool
		for _, assignee := range serviceAssignees {
			if assignee.IdentityID == createdAgent.IdentityID && assignee.Type == domain.OrganizationIdentityTypeAgent {
				agentListedAsCustomerService = true
				break
			}
		}
		if !agentListedAsCustomerService {
			t.Fatalf("AI customer service missing from assignees: %#v", serviceAssignees)
		}
		originalRevisionID := createdAgent.Execution.RevisionID
		agentWithUpdatedExecution, err := agentaction.NewUpdateExecutionAction(db).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateExecutionInput{ExecutionInput: agentaction.ExecutionInput{
			Mode: domain.AgentExecutionModeManaged,
			Managed: &agentaction.ManagedExecutionInput{
				ModelID: model.ID, SystemInstruction: "负责接待并回答客户问题。",
			},
		}})
		if err != nil || agentWithUpdatedExecution.Execution.RevisionID == originalRevisionID || agentWithUpdatedExecution.Execution.Managed.SystemInstruction != "负责接待并回答客户问题。" {
			t.Fatalf("updated agent execution = %#v, error = %v", agentWithUpdatedExecution, err)
		}
		revisionCount, err := db.NewSelect().Model((*servermodels.AgentRevision)(nil)).
			Where("organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("agent_id = ?", createdAgent.ID).
			Count(context.Background())
		if err != nil || revisionCount != 2 {
			t.Fatalf("agent revision count = %d, error = %v", revisionCount, err)
		}
		if createdAgent.IdentityID == "" || createdAgent.IdentityID == createdAgent.ID {
			t.Fatalf("agent identity id = %q, agent id = %q", createdAgent.IdentityID, createdAgent.ID)
		}
		agents, err := agentaction.NewListAgentsQuery(db).Execute(context.Background(), loggedIn.Identity, agentaction.ListInput{Page: 1, PageSize: 50})
		if err != nil || agents.Page.Total != 1 || len(agents.Agents) != 1 || agents.Agents[0].ID != createdAgent.ID {
			t.Fatalf("agent directory = %#v, error = %v", agents, err)
		}
		updatedAgent, err := agentaction.NewUpdateAgentAction(db, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{
			DisplayName:      "售前智能体",
			TeamIDs:          []string{team.ID},
			ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer},
			WorkStatus:       domain.WorkStatusAway,
		})
		if err != nil || updatedAgent.DisplayName != "售前智能体" || updatedAgent.WorkStatus != domain.WorkStatusAway {
			t.Fatalf("updated agent = %#v, error = %v", updatedAgent, err)
		}
		agent, err := agentaction.NewGetAgentQuery(db).Execute(context.Background(), loggedIn.Identity, createdAgent.ID)
		if err != nil || agent.DisplayName != "售前智能体" || agent.WorkStatus != domain.WorkStatusAway {
			t.Fatalf("agent detail = %#v, error = %v", agent, err)
		}
		// 核验无效工作状态触发整次资料提交回滚。
		if _, err := agentaction.NewUpdateAgentAction(db, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{
			DisplayName: "不应保存的名称", TeamIDs: []string{team.ID}, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: "invalid",
		}); err == nil {
			t.Fatal("invalid agent work status update succeeded")
		} else {
			var fieldError *common.FieldError
			if !errors.As(err, &fieldError) || fieldError.Fields["workStatus"] != agentaction.ValidationWorkStatusInvalid {
				t.Fatalf("invalid agent work status error = %#v", err)
			}
		}
		agent, err = agentaction.NewGetAgentQuery(db).Execute(context.Background(), loggedIn.Identity, createdAgent.ID)
		if err != nil || agent.DisplayName != "售前智能体" || agent.WorkStatus != domain.WorkStatusAway {
			t.Fatalf("agent after rejected update = %#v, error = %v", agent, err)
		}
		teamMembers, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, team.ID, teamaction.MemberListInput{WorkStatus: domain.WorkStatusAway, Page: 1, PageSize: 50})
		if err != nil || teamMembers.Page.Total != 1 || len(teamMembers.Members) != 1 || teamMembers.Members[0].IdentityID != createdAgent.IdentityID || teamMembers.Members[0].WorkStatus != domain.WorkStatusAway {
			t.Fatalf("team directory = %#v, error = %v", teamMembers, err)
		}
		channel, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, channel.ID, channelaction.MessageChannelReceptionInput{
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: createdAgent.IdentityID},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil || channel.InitialRoutingTargetID == nil || *channel.InitialRoutingTargetID != createdAgent.IdentityID {
			t.Fatalf("agent channel routing = %#v, error = %v", channel, err)
		}
		_, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, telegramChannel.ID, channelaction.MessageChannelReceptionInput{
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: createdAgent.IdentityID},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil {
			t.Fatalf("Telegram agent route error = %#v", err)
		}

		taskRuntime := newTestTasks(db)
		if err := taskRuntime.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error {
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		scheduler := agentrunaction.NewScheduler(taskRuntime)
		coordinator := agentrunaction.NewExecuteAction(db, taskRuntime, nil, testAttachmentReader(db), nil, nil)
		claimServiceSession := servicesessionaction.NewClaimServiceSessionAction(db, coordinator, newTestTasks(db))
		transferServiceSession := servicesessionaction.NewTransferServiceSessionAction(db, coordinator, scheduler, newTestTasks(db))
		closeServiceSession := servicesessionaction.NewCloseServiceSessionAction(db, coordinator, newTestTasks(db))
		if _, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, telegramConversationID); err != nil {
			t.Fatal(err)
		}
		_, err = transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
			ConversationID: telegramConversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
		})
		if err != nil {
			t.Fatalf("Telegram agent transfer error = %#v", err)
		}
		channel, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, channel.ID, channelaction.MessageChannelReceptionInput{
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil {
			t.Fatal(err)
		}
		publicQueueInbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil).Execute(context.Background(), customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: channel.ID, ExternalID: "web-session:fedcba9876543210fedcba9876543210",
			ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f92", Body: "需要人工接待",
		})
		if err != nil {
			t.Fatal(err)
		}
		sendCustomerMessage := servicesessionaction.NewSendServiceTextMessageAction(db, newTestTasks(db))
		_, err = sendCustomerMessage.Execute(context.Background(), loggedIn.Identity, servicesessionaction.ServiceTextMessageInput{
			ConversationID: publicQueueInbound.Conversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f93", Body: "我来处理",
		})
		if err != nil {
			t.Fatal(err)
		}
		memberHistory, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(context.Background(), customerchataction.MessageHistoryInput{
			ChannelID: channel.ID, ExternalID: "web-session:fedcba9876543210fedcba9876543210", ConversationID: publicQueueInbound.Conversation.ID,
		})
		// 成员回复前自动领取周期，访客在回复前看到成员加入事件。
		if err != nil || len(memberHistory.Messages) != 3 || memberHistory.Messages[0].SenderIdentityType != nil ||
			memberHistory.Messages[1].Event == nil || memberHistory.Messages[1].Event.Type != customerchataction.VisitorEventMemberJoined ||
			memberHistory.Messages[2].Author != domain.MessageAuthorAgent || memberHistory.Messages[2].SenderIdentityType == nil || *memberHistory.Messages[2].SenderIdentityType != domain.OrganizationIdentityTypeUser {
			t.Fatalf("website visitor and human sender identities = %#v, error = %v", memberHistory, err)
		}
		memberSummariesDirectory, err := customerchataction.NewListWebsiteConversationsQuery(db).Execute(context.Background(), channel.ID, "web-session:fedcba9876543210fedcba9876543210")
		memberSummaries := memberSummariesDirectory.Conversations
		if err != nil || len(memberSummaries) != 1 || memberSummaries[0].PreviewSenderIdentityType == nil || *memberSummaries[0].PreviewSenderIdentityType != domain.OrganizationIdentityTypeUser {
			t.Fatalf("website human preview identity = %#v, error = %v", memberSummaries, err)
		}
		publicQueueSession := &servermodels.ServiceSession{}
		if err := db.NewSelect().Model(publicQueueSession).
			Where("ss.id = ?", publicQueueInbound.Conversation.ServiceSessionID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if publicQueueSession.AssigneeIdentityID == nil || *publicQueueSession.AssigneeIdentityID != loggedIn.Identity.OrganizationIdentity.ID {
			t.Fatalf("public queue reply session = %#v", publicQueueSession)
		}
		closedPublicQueue, err := closeServiceSession.Execute(context.Background(), loggedIn.Identity, publicQueueInbound.Conversation.ID)
		if err != nil || closedPublicQueue.Status != domain.ServiceSessionStatusClosed {
			t.Fatalf("closed public queue session = %#v, error = %v", closedPublicQueue, err)
		}
		channel, err = updateChannel.ExecuteReception(context.Background(), loggedIn.Identity, channel.ID, channelaction.MessageChannelReceptionInput{
			NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: createdAgent.IdentityID},
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		})
		if err != nil {
			t.Fatal(err)
		}

		websiteMessageInput := customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef",
			ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f80", Body: "需要 AI 接待",
		}
		websiteInbound, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil).Execute(context.Background(), websiteMessageInput)
		if err != nil {
			t.Fatal(err)
		}
		websiteRetried, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil).Execute(context.Background(), websiteMessageInput)
		if err != nil || websiteRetried.Message.ID != websiteInbound.Message.ID {
			t.Fatalf("idempotent website message = %#v, error = %v", websiteRetried, err)
		}
		websiteSession := &servermodels.ServiceSession{}
		if err := db.NewSelect().Model(websiteSession).
			Where("ss.id = ?", websiteInbound.Conversation.ServiceSessionID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if websiteSession.AssigneeIdentityID == nil || *websiteSession.AssigneeIdentityID != createdAgent.IdentityID {
			t.Fatalf("website agent route session = %#v", websiteSession)
		}
		inboxQuery := inboxaction.NewLoadInboxQuery(db)
		allBeforeWebsiteClaimPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
		allBeforeWebsiteClaim := allBeforeWebsiteClaimPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertInboxConversationPresence(t, allBeforeWebsiteClaim, websiteInbound.Conversation.ID, false)
		coworkerInboxBeforeWebsiteClaimPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{
			Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: createdAgent.IdentityID,
		})
		coworkerInboxBeforeWebsiteClaim := coworkerInboxBeforeWebsiteClaimPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertInboxConversationPresence(t, coworkerInboxBeforeWebsiteClaim, websiteInbound.Conversation.ID, true)
		websiteTriggerCount, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).
			Join("JOIN agent_lanes al ON al.id = ai.lane_id").
			Where("al.conversation_id = ?", websiteInbound.Conversation.ID).
			Count(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		websiteRunCount, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
			Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
			Count(context.Background())
		if err != nil || websiteTriggerCount != 1 || websiteRunCount != 1 {
			t.Fatalf("website agent route triggers = %d, runs = %d, error = %v", websiteTriggerCount, websiteRunCount, err)
		}
		initialWebsiteRun := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(initialWebsiteRun).
			Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
			Where("agr.status = ?", domain.AgentRunStatusQueued).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if initialWebsiteRun.ScopeKind != string(domain.AgentExecutionScopeServiceSession) || initialWebsiteRun.ScopeID != websiteSession.ID || initialWebsiteRun.InputStartSeq != 1 || initialWebsiteRun.InputEndSeq != nil {
			t.Fatalf("initial website agent run = %#v", initialWebsiteRun)
		}
		claimedWebsite, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
		if err != nil || claimedWebsite.Assignee == nil || claimedWebsite.Assignee.IdentityID != loggedIn.Identity.OrganizationIdentity.ID {
			t.Fatalf("claim agent session without state = %#v, error = %v", claimedWebsite, err)
		}
		allAfterWebsiteClaimPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
		allAfterWebsiteClaim := allAfterWebsiteClaimPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertInboxConversationPresence(t, allAfterWebsiteClaim, websiteInbound.Conversation.ID, true)
		transferredWithoutReply, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
			ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
		})
		if err != nil || transferredWithoutReply.Assignee == nil || transferredWithoutReply.Assignee.IdentityID != createdAgent.IdentityID {
			t.Fatalf("transfer unparticipated website session = %#v, error = %v", transferredWithoutReply, err)
		}
		allAfterTransferWithoutReplyPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
		allAfterTransferWithoutReply := allAfterTransferWithoutReplyPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertInboxConversationPresence(t, allAfterTransferWithoutReply, websiteInbound.Conversation.ID, false)
		reclaimedWebsite, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
		if err != nil || reclaimedWebsite.Assignee == nil || reclaimedWebsite.Assignee.IdentityID != loggedIn.Identity.OrganizationIdentity.ID {
			t.Fatalf("reclaim website session before reply = %#v, error = %v", reclaimedWebsite, err)
		}
		if _, err := servicesessionaction.NewSendServiceTextMessageAction(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, servicesessionaction.ServiceTextMessageInput{
			ConversationID: websiteInbound.Conversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f91", Body: "我已参与处理",
		}); err != nil {
			t.Fatal(err)
		}
		transferredWebsite, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
			ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
		})
		if err != nil || transferredWebsite.Assignee == nil || transferredWebsite.Assignee.IdentityID != createdAgent.IdentityID {
			t.Fatalf("transfer website session to agent = %#v, error = %v", transferredWebsite, err)
		}
		allAfterWebsiteTransferPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
		allAfterWebsiteTransfer := allAfterWebsiteTransferPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		// 本人回复后转给 AI 员工，会话由 AI 员工负责，不再需要本人处理。
		assertInboxConversationPresence(t, allAfterWebsiteTransfer, websiteInbound.Conversation.ID, false)
		updatedAgent, err = agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, domain.IdentityStatusInactive)
		if err != nil || updatedAgent.Status != domain.IdentityStatusInactive || updatedAgent.WorkStatus != domain.WorkStatusOffDuty {
			t.Fatalf("inactive agent = %#v, error = %v", updatedAgent, err)
		}
		teamMembersAfterAgentDeactivation, err := teamaction.NewListMembersQuery(db).Execute(context.Background(), loggedIn.Identity, team.ID, teamaction.MemberListInput{Page: 1, PageSize: 50})
		if err != nil || teamMembersAfterAgentDeactivation.Page.Total != 1 || len(teamMembersAfterAgentDeactivation.Members) != 1 || teamMembersAfterAgentDeactivation.Members[0].IdentityID != createdMember.IdentityID {
			t.Fatalf("team members after agent deactivation = %#v, error = %v", teamMembersAfterAgentDeactivation, err)
		}
		teamAfterAgentDeactivation, err := teamaction.NewUpdateTeamAction(db).Execute(context.Background(), loggedIn.Identity, team.ID, teamaction.Input{Name: team.Name, Description: team.Description})
		if err != nil || teamAfterAgentDeactivation.MemberCount != 1 {
			t.Fatalf("team after agent deactivation = %#v, error = %v", teamAfterAgentDeactivation, err)
		}
		if _, err := agentaction.NewUpdateAgentAction(db, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{DisplayName: updatedAgent.DisplayName, TeamIDs: []string{team.ID}, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking}); err == nil {
			t.Fatal("inactive agent work status update succeeded")
		} else {
			var fieldError *common.FieldError
			if !errors.As(err, &fieldError) || fieldError.Fields["workStatus"] != agentaction.ValidationWorkStatusUnavailable {
				t.Fatalf("inactive agent work status error = %#v", err)
			}
		}
		updatedAgent, err = agentaction.NewUpdateStatusAction(db, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, domain.IdentityStatusActive)
		if err != nil {
			t.Fatal(err)
		}
		if updatedAgent.WorkStatus != domain.WorkStatusOffDuty {
			t.Fatalf("reactivated agent work status = %q, want %q", updatedAgent.WorkStatus, domain.WorkStatusOffDuty)
		}
		teamAfterAgentReactivation, err := teamaction.NewListTeamsQuery(db).Execute(context.Background(), loggedIn.Identity, teamaction.ListInput{Page: 1, PageSize: 50})
		if err != nil || len(teamAfterAgentReactivation.Teams) != 1 || teamAfterAgentReactivation.Teams[0].MemberCount != 2 {
			t.Fatalf("team after agent reactivation = %#v, error = %v", teamAfterAgentReactivation, err)
		}
		updatedAgent, err = agentaction.NewUpdateAgentAction(db, testServiceSessionReturner(db)).Execute(context.Background(), loggedIn.Identity, createdAgent.ID, agentaction.UpdateInput{DisplayName: updatedAgent.DisplayName, TeamIDs: []string{team.ID}, ServiceAudiences: []domain.ServiceAudience{domain.ServiceAudienceCustomer}, WorkStatus: domain.WorkStatusWorking})
		if err != nil || updatedAgent.WorkStatus != domain.WorkStatusWorking {
			t.Fatalf("working agent = %#v, error = %v", updatedAgent, err)
		}
		if _, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
			ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
		}); err != nil {
			t.Fatal(err)
		}

		agentStart, err := directchataction.NewSendFirstAgentTextMessageAction(db, scheduler).Execute(context.Background(), loggedIn.Identity, directchataction.FirstAgentTextMessageInput{ConversationID: "0198ddf0-a234-7f01-8d99-e3e0af0f5fff",
			AgentIdentityID: createdAgent.IdentityID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f70", Body: "计算 6 乘以 7",
		})
		if err != nil || agentStart.Conversation.Agent.AgentIdentityID != createdAgent.IdentityID {
			t.Fatalf("agent direct conversation = %#v, error = %v", agentStart.Conversation, err)
		}
		agentConversation := agentStart.Conversation
		sendAgentMessage := directchataction.NewSendAgentTextMessageAction(db, scheduler)
		if _, err := sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
			ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f71", Body: "只给我最终结果",
		}); err != nil {
			t.Fatal(err)
		}

		state := &servermodels.AgentLane{}
		if err := db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		triggerCount, err := db.NewSelect().Model((*servermodels.AgentInput)(nil)).Join("JOIN agent_lanes al ON al.id = ai.lane_id").Where("al.conversation_id = ?", agentConversation.ID).Count(context.Background())
		if err != nil || state.DesiredSeq != 2 || state.ProcessedSeq != 0 || triggerCount != 2 {
			t.Fatalf("scheduled agent input state = %#v, triggers = %d, error = %v", state, triggerCount, err)
		}
		run := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(run).Where("agr.conversation_id = ?", agentConversation.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if run.ScopeKind != string(domain.AgentExecutionScopeConversation) || run.ScopeID != agentConversation.ID {
			t.Fatalf("direct run scope fields = %#v", run)
		}
		websiteConversationID := websiteInbound.Conversation.ID
		if _, err := customerchataction.NewReceiveWebsiteCustomerMessageAction(db, scheduler, newTestTasks(db), nil).Execute(context.Background(), customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef",
			ConversationID: &websiteConversationID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f81", Body: "接管前的新问题",
		}); err != nil {
			t.Fatal(err)
		}
		customerState := &servermodels.AgentLane{}
		if err := db.NewSelect().Model(customerState).
			Where("al.conversation_id = ?", websiteInbound.Conversation.ID).
			Where("al.agent_identity_id = ?", createdAgent.IdentityID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		customerRun := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(customerRun).
			Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
			Where("agr.agent_identity_id = ?", createdAgent.IdentityID).
			Where("agr.status = ?", domain.AgentRunStatusQueued).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if customerState.DesiredSeq != 3 || customerState.ProcessedSeq != 2 || customerRun.InputStartSeq != 3 || customerRun.InputEndSeq != nil {
			t.Fatalf("scheduled customer follow-up run = %#v, state = %#v", customerRun, customerState)
		}
		_, err = closeServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
		var closeOwnedConflict *conversationaction.ConflictError
		if !errors.As(err, &closeOwnedConflict) || closeOwnedConflict.Reason != conversationaction.ConflictReasonServiceSessionOwned {
			t.Fatalf("close agent-owned session error = %#v", err)
		}
		if err := db.NewSelect().Model(customerRun).Where("agr.id = ?", customerRun.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if customerRun.Status != string(domain.AgentRunStatusQueued) || customerRun.ErrorCode != nil {
			t.Fatalf("run changed by unauthorized close = %#v", customerRun)
		}
		claimedRunningWebsite, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
		if err != nil || claimedRunningWebsite.Assignee == nil || claimedRunningWebsite.Assignee.IdentityID != loggedIn.Identity.OrganizationIdentity.ID {
			t.Fatalf("claim running agent session = %#v, error = %v", claimedRunningWebsite, err)
		}
		if err := db.NewSelect().Model(customerRun).Where("agr.id = ?", customerRun.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(customerState).
			Where("al.conversation_id = ?", customerState.ConversationID).
			Where("al.agent_identity_id = ?", customerState.AgentIdentityID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if customerRun.Status != string(domain.AgentRunStatusCancelled) || customerRun.ErrorCode == nil || *customerRun.ErrorCode != string(domain.AgentRunErrorCodeAssigneeChanged) || customerState.ProcessedSeq != 3 || customerState.ProcessedSeq != customerState.DesiredSeq {
			t.Fatalf("cancelled customer run = %#v, state = %#v", customerRun, customerState)
		}
		if err := coordinator.Execute(context.Background(), agentrunaction.RunInput{RunID: customerRun.ID}); err != nil {
			t.Fatalf("cancelled run retry error = %v", err)
		}
		if err := coordinator.FinalizeFailure(context.Background(), agentrunaction.RunInput{RunID: customerRun.ID}, errors.New("task retry exhausted")); err != nil {
			t.Fatalf("cancelled run finalizer error = %v", err)
		}
		transferredForCustomerRun, err := transferServiceSession.Execute(context.Background(), loggedIn.Identity, servicesessionaction.TransferServiceSessionInput{
			ConversationID: websiteInbound.Conversation.ID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: createdAgent.IdentityID,
		})
		if err != nil || transferredForCustomerRun.Assignee == nil || transferredForCustomerRun.Assignee.IdentityID != createdAgent.IdentityID {
			t.Fatalf("transfer website session for customer run = %#v, error = %v", transferredForCustomerRun, err)
		}
		absorbingCustomerRun := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(absorbingCustomerRun).
			Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
			Where("agr.agent_identity_id = ?", createdAgent.IdentityID).
			Where("agr.status = ?", domain.AgentRunStatusQueued).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		customerRuntime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			pending, err := feed.Peek(ctx, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if len(pending) != 1 || pending[0].Seq != 4 {
				return agentruntime.RunResult{}, fmt.Errorf("unexpected initial customer trigger: %#v", pending)
			}
			claimed, err := feed.Claim(ctx, pending[0].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if claimed.EndSeq != 4 || len(claimed.Messages) == 0 || claimed.Messages[len(claimed.Messages)-1].Content != "接管前的新问题" {
				return agentruntime.RunResult{}, fmt.Errorf("unexpected customer claim: %#v", claimed)
			}
			return agentruntime.RunResult{Content: "已回复新问题", EndSeq: claimed.EndSeq, Usage: agentruntime.Usage{TotalTokens: 18}}, nil
		}}
		if err := agentrunaction.NewExecuteAction(db, taskRuntime, customerRuntime, testAttachmentReader(db), nil, nil).Execute(context.Background(), agentrunaction.RunInput{RunID: absorbingCustomerRun.ID}); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(absorbingCustomerRun).Where("agr.id = ?", absorbingCustomerRun.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(customerState).
			Where("al.conversation_id = ?", customerState.ConversationID).
			Where("al.agent_identity_id = ?", customerState.AgentIdentityID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		queuedCustomerRuns, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
			Where("agr.conversation_id = ?", websiteInbound.Conversation.ID).
			Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
			Count(context.Background())
		if err != nil || absorbingCustomerRun.Status != string(domain.AgentRunStatusSucceeded) || absorbingCustomerRun.InputStartSeq != 4 || absorbingCustomerRun.InputEndSeq == nil || *absorbingCustomerRun.InputEndSeq != 4 || customerState.DesiredSeq != 4 || customerState.ProcessedSeq != 4 || queuedCustomerRuns != 0 {
			t.Fatalf("absorbed customer run = %#v, state = %#v, active runs = %d, error = %v", absorbingCustomerRun, customerState, queuedCustomerRuns, err)
		}
		websiteMessages, err := customerchataction.NewListWebsiteMessagesQuery(db).Execute(context.Background(), customerchataction.MessageHistoryInput{
			ChannelID: channel.ID, ExternalID: "web-session:0123456789abcdef0123456789abcdef", ConversationID: websiteInbound.Conversation.ID,
		})
		if err != nil || len(websiteMessages.Messages) == 0 || websiteMessages.Messages[len(websiteMessages.Messages)-1].Author != domain.MessageAuthorAgent || websiteMessages.Messages[len(websiteMessages.Messages)-1].Body != "已回复新问题" || websiteMessages.Messages[len(websiteMessages.Messages)-1].SenderIdentityType == nil || *websiteMessages.Messages[len(websiteMessages.Messages)-1].SenderIdentityType != domain.OrganizationIdentityTypeAgent {
			t.Fatalf("website messages after customer run = %#v, error = %v", websiteMessages, err)
		}
		websiteSummariesDirectory, err := customerchataction.NewListWebsiteConversationsQuery(db).Execute(context.Background(), channel.ID, "web-session:0123456789abcdef0123456789abcdef")
		websiteSummaries := websiteSummariesDirectory.Conversations
		if err != nil || len(websiteSummaries) == 0 || websiteSummaries[0].ID != websiteInbound.Conversation.ID || websiteSummaries[0].PreviewSenderIdentityType == nil || *websiteSummaries[0].PreviewSenderIdentityType != domain.OrganizationIdentityTypeAgent {
			t.Fatalf("website AI preview identity = %#v, error = %v", websiteSummaries, err)
		}
		if _, err := claimServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(run).Where("agr.id = ?", run.ID).Scan(context.Background()); err != nil || run.Status != string(domain.AgentRunStatusQueued) {
			t.Fatalf("direct run after customer takeover = %#v, error = %v", run, err)
		}

		taskRun := &servermodels.TaskRun{}
		if err := db.NewSelect().Model(taskRun).Where("tr.idempotency_key = ?", "agent:"+run.ID).Scan(context.Background()); err != nil || taskRun.MaxAttempts != 3 {
			t.Fatalf("agent task run = %#v, error = %v", taskRun, err)
		}
		inboxBeforeRunPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		inboxBeforeRun := inboxBeforeRunPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertDirectAgentRunStatus(t, inboxBeforeRun, agentConversation.ID, domain.AgentRunStatusQueued, "只给我最终结果")

		var executionStreams []string
		var successfulBlocks []agentruntime.Block
		executedRuntime := testAgentRuntime{run: func(ctx context.Context, request agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			executionStreams = append(executionStreams, request.StreamID)
			if request.Assignment.AgentName != "售前智能体" || request.Assignment.Model.Identifier != model.Identifier {
				return agentruntime.RunResult{}, errors.New("unexpected agent runtime request")
			}
			pending, err := feed.Peek(ctx, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if len(pending) != 2 || pending[1].Seq != 2 {
				return agentruntime.RunResult{}, errors.New("unexpected pending agent triggers")
			}
			firstClaim, err := feed.Claim(ctx, pending[0].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if firstClaim.EndSeq != 1 || len(firstClaim.Messages) != 1 {
				return agentruntime.RunResult{}, errors.New("agent claim exceeded requested boundary")
			}
			claimed, err := feed.Claim(ctx, pending[1].Seq)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if claimed.EndSeq != 2 || len(claimed.Messages) != 2 {
				return agentruntime.RunResult{}, errors.New("unexpected claimed agent input")
			}
			successfulBlocks = []agentruntime.Block{{
				ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
				Kind: domain.AgentRunBlockThinking, Payload: agentruntime.BlockPayload{Text: "计算过程"},
			}}
			request.OnStream(runstream.Delta{RunID: request.RunID, StreamID: request.StreamID, Attempt: request.Attempt, Sequence: 1, Operations: []runstream.Operation{{
				Kind:  runstream.OperationUpsertBlock,
				Block: &runstream.Block{ID: successfulBlocks[0].ID, Position: 1, ModelCallID: successfulBlocks[0].ModelCallID, Kind: domain.AgentRunBlockThinking, Text: "计算过程"},
			}}})
			return agentruntime.RunResult{Content: "结果是 42", EndSeq: claimed.EndSeq, Usage: agentruntime.Usage{TotalTokens: 12}, Blocks: successfulBlocks}, nil
		}}
		executeAgentRun := agentrunaction.NewExecuteAction(db, taskRuntime, executedRuntime, testAttachmentReader(db), nil, nil)
		// 用查询钩子让该会话的 AI 回复写入失败，共享测试库上的消息表不加结构锁。
		responseFailure := &agentResponseFailureHook{conversationID: agentConversation.ID}
		responseFailure.armed.Store(true)
		db.AddQueryHook(responseFailure)
		persistenceErr := executeAgentRun.Execute(context.Background(), agentrunaction.RunInput{RunID: run.ID})
		if persistenceErr == nil || servertask.IsPermanent(persistenceErr) {
			t.Fatalf("agent completion persistence error = %#v", persistenceErr)
		}
		if err := db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(run).Where("agr.id = ?", run.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		messageCountAfterPersistenceError, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", agentConversation.ID).Count(context.Background())
		if err != nil || state.ProcessedSeq != 0 || run.Status != string(domain.AgentRunStatusRunning) || messageCountAfterPersistenceError != 2 {
			t.Fatalf("agent run after completion persistence error = %#v, state = %#v, messages = %d, error = %v", run, state, messageCountAfterPersistenceError, err)
		}
		if count, err := db.NewSelect().Model((*servermodels.AgentRunBlock)(nil)).Where("arb.agent_run_id = ?", run.ID).Count(context.Background()); err != nil || count != 0 {
			t.Fatalf("blocks after failed transaction = %d, error = %v", count, err)
		}
		if _, _, exists := executeAgentRun.SubscribeRunStream(run.ID, func(runstream.Delta) {}, func() {}); exists {
			t.Fatal("failed attempt retained its temporary stream")
		}
		responseFailure.armed.Store(false)
		if err := executeAgentRun.Execute(context.Background(), agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(run).Where("agr.id = ?", run.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		messageCount, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.conversation_id = ?", agentConversation.ID).Count(context.Background())
		if err != nil || state.ProcessedSeq != 2 || run.Status != string(domain.AgentRunStatusSucceeded) || run.ResponseMessageID == nil || messageCount != 3 {
			t.Fatalf("completed agent run = %#v, state = %#v, messages = %d, error = %v", run, state, messageCount, err)
		}
		var savedBlocks []servermodels.AgentRunBlock
		if err := db.NewSelect().Model(&savedBlocks).Where("arb.agent_run_id = ?", run.ID).Order("position ASC").Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(savedBlocks) != 1 || savedBlocks[0].ID != successfulBlocks[0].ID || savedBlocks[0].OrganizationID != run.OrganizationID || !strings.Contains(string(savedBlocks[0].Payload), "计算过程") {
			t.Fatalf("saved blocks = %#v", savedBlocks)
		}
		if len(executionStreams) != 2 || executionStreams[0] == "" || executionStreams[0] == executionStreams[1] {
			t.Fatalf("retry streams = %#v", executionStreams)
		}
		if err := executeAgentRun.Execute(context.Background(), agentrunaction.RunInput{RunID: run.ID}); err != nil || len(executionStreams) != 2 {
			t.Fatalf("completed run was recomputed: streams = %#v, error = %v", executionStreams, err)
		}
		inboxAfterRunPage, _, err := inboxaction.NewLoadInboxQuery(db).Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopeChat})
		inboxAfterRun := inboxAfterRunPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertDirectAgentRunStatus(t, inboxAfterRun, agentConversation.ID, domain.AgentRunStatusSucceeded, "结果是 42")
		// 最新窗口和锚点窗口均返回消息所属的运行引用，不将过程附到用户消息。
		for _, anchor := range []string{"", *run.ResponseMessageID} {
			history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: agentConversation.ID, AroundMessageID: anchor})
			if err != nil || len(history.AgentRuns) != 0 {
				t.Fatalf("agent message history = %#v, error = %v", history, err)
			}
			foundProcess := false
			for _, message := range history.Messages {
				if message.ID != *run.ResponseMessageID {
					if message.AgentProcess != nil {
						t.Fatal("user message contains agent process")
					}
					continue
				}
				process := message.AgentProcess
				if process == nil || process.ID != run.ID || process.Usage.TotalTokens != 12 || process.DurationMilliseconds < 0 {
					t.Fatalf("message agent process = %#v", process)
				}
				foundProcess = true
			}
			if !foundProcess {
				t.Fatal("reply process missing from message window")
			}
		}
		// 过程内容按运行编号单独读取，未知运行不返回详情。
		runProcess, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(context.Background(), loggedIn.Identity, run.ID)
		if err != nil || runProcess.ID != run.ID || len(runProcess.Blocks) != 1 || runProcess.Blocks[0].Payload.Text != "计算过程" ||
			runProcess.Usage.TotalTokens != 12 || runProcess.DurationMilliseconds < 0 {
			t.Fatalf("agent run process = %#v, error = %v", runProcess, err)
		}
		if _, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(context.Background(), loggedIn.Identity, uuid.NewV7().String()); !errors.Is(err, conversationaction.ErrAgentRunProcessUnavailable) {
			t.Fatalf("unknown agent run process error = %v", err)
		}
		// 同企业其他成员没有这条 AI 会话的阅读资格，读不到运行过程。
		outsiderLogin := loginMember(t, db, loggedIn.Identity.Organization.ID, createdMember.Email, "password123")
		if _, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(context.Background(), outsiderLogin.Identity, run.ID); !errors.Is(err, conversationaction.ErrAgentRunProcessUnavailable) {
			t.Fatalf("outsider agent run process error = %v", err)
		}
		// 运行过程流按同一阅读资格授权，本人取得运行所属会话，未知运行与无资格成员均不返回。
		authorizeRunStream := conversationaction.NewAuthorizeAgentRunStreamQuery(db)
		if streamConversationID, err := authorizeRunStream.Execute(context.Background(), loggedIn.Identity, run.ID); err != nil || streamConversationID != agentConversation.ID {
			t.Fatalf("agent run stream conversation = %q, error = %v", streamConversationID, err)
		}
		if _, err := authorizeRunStream.Execute(context.Background(), loggedIn.Identity, uuid.NewV7().String()); !errors.Is(err, conversationaction.ErrAgentRunProcessUnavailable) {
			t.Fatalf("unknown agent run stream error = %v", err)
		}
		if _, err := authorizeRunStream.Execute(context.Background(), outsiderLogin.Identity, run.ID); !errors.Is(err, conversationaction.ErrAgentRunProcessUnavailable) {
			t.Fatalf("outsider agent run stream error = %v", err)
		}

		if _, err := sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
			ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f72", Body: "这次模拟模型失败",
		}); err != nil {
			t.Fatal(err)
		}
		failedRun := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(failedRun).
			Where("agr.conversation_id = ?", agentConversation.ID).
			Where("agr.status = ?", domain.AgentRunStatusQueued).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		failingRuntime := testAgentRuntime{run: func(ctx context.Context, _ agentruntime.RunRequest, feed agentruntime.InputFeed) (agentruntime.RunResult, error) {
			pending, err := feed.Peek(ctx, 0)
			if err != nil {
				return agentruntime.RunResult{}, err
			}
			if len(pending) != 1 || pending[0].Seq != 3 {
				return agentruntime.RunResult{}, errors.New("unexpected failing agent trigger")
			}
			if _, err := feed.Claim(ctx, pending[0].Seq); err != nil {
				return agentruntime.RunResult{}, err
			}
			return agentruntime.RunResult{
				Usage: agentruntime.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
				Blocks: []agentruntime.Block{{
					ID: uuid.NewV7().String(), Position: 1, ModelCallID: uuid.NewV7().String(),
					Kind: domain.AgentRunBlockThinking, Payload: agentruntime.BlockPayload{Text: "中断前的思考"},
				}},
			}, errors.New("model rejected input")
		}}
		if err := agentrunaction.NewExecuteAction(db, taskRuntime, failingRuntime, testAttachmentReader(db), nil, nil).Execute(context.Background(), agentrunaction.RunInput{RunID: failedRun.ID}); err == nil {
			t.Fatal("failing agent run succeeded")
		}
		if err := db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(failedRun).Where("agr.id = ?", failedRun.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if state.ProcessedSeq != 3 || failedRun.Status != string(domain.AgentRunStatusFailed) || failedRun.InputEndSeq == nil || *failedRun.InputEndSeq != 3 {
			t.Fatalf("failed claimed agent run = %#v, state = %#v", failedRun, state)
		}
		failedHistory, err := conversationaction.NewListConversationMessagesQuery(db).Execute(context.Background(), loggedIn.Identity, conversationaction.ConversationMessageHistoryInput{ConversationID: agentConversation.ID})
		if err != nil || failedRun.LastError == nil || len(failedHistory.AgentRuns) != 0 {
			t.Fatalf("failed run message state = %#v, error = %v", failedHistory, err)
		}
		// 失败运行的过程内容同样按运行编号读取。
		failedRunProcess, err := conversationaction.NewGetAgentRunProcessQuery(db).Execute(context.Background(), loggedIn.Identity, failedRun.ID)
		if err != nil || len(failedRunProcess.Blocks) != 1 || failedRunProcess.Blocks[0].Payload.Text != "中断前的思考" || failedRunProcess.Usage.TotalTokens != 7 {
			t.Fatalf("failed agent run process = %#v, error = %v", failedRunProcess, err)
		}
		// 运行过程流不限运行状态，失败运行同样按会话阅读资格授权。
		if streamConversationID, err := authorizeRunStream.Execute(context.Background(), loggedIn.Identity, failedRun.ID); err != nil || streamConversationID != agentConversation.ID {
			t.Fatalf("failed agent run stream conversation = %q, error = %v", streamConversationID, err)
		}

		if _, err := sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
			ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f73", Body: "失败后继续",
		}); err != nil {
			t.Fatal(err)
		}
		exhaustedRun := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(exhaustedRun).
			Where("agr.conversation_id = ?", agentConversation.ID).
			Where("agr.status = ?", domain.AgentRunStatusQueued).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if exhaustedRun.InputStartSeq != 4 {
			t.Fatalf("agent run after failure starts at %d, want 4", exhaustedRun.InputStartSeq)
		}
		finalizer := agentrunaction.NewExecuteAction(db, taskRuntime, failingRuntime, testAttachmentReader(db), nil, nil)
		if err := finalizer.FinalizeFailure(context.Background(), agentrunaction.RunInput{RunID: exhaustedRun.ID}, errors.New("task attempts exhausted")); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(state).Where("al.conversation_id = ?", agentConversation.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(exhaustedRun).Where("agr.id = ?", exhaustedRun.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if state.ProcessedSeq != 4 || exhaustedRun.Status != string(domain.AgentRunStatusFailed) || exhaustedRun.InputEndSeq == nil || *exhaustedRun.InputEndSeq != 4 {
			t.Fatalf("exhausted agent run = %#v, state = %#v", exhaustedRun, state)
		}
		if _, err := sendAgentMessage.Execute(context.Background(), loggedIn.Identity, directchataction.InternalTextMessageInput{
			ConversationID: agentConversation.ID, ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f74", Body: "耗尽后继续",
		}); err != nil {
			t.Fatal(err)
		}
		nextRun := &servermodels.AgentRun{}
		if err := db.NewSelect().Model(nextRun).
			Where("agr.conversation_id = ?", agentConversation.ID).
			Where("agr.status = ?", domain.AgentRunStatusQueued).
			Scan(context.Background()); err != nil || nextRun.InputStartSeq != 5 {
			t.Fatalf("agent run after exhausted task = %#v, error = %v", nextRun, err)
		}

		testAgentFailureMessages(t, db, loggedIn.Identity, taskRuntime, agentConversation.ID, failedRun.ID, exhaustedRun.ID, nextRun.ID)

		t.Run("Agent 运行期 MCP 服务", func(t *testing.T) {
			testAgentRunMCPServices(t, db, loggedIn.Identity, model.ID, taskRuntime)
		})

		closedWebsite, err := closeServiceSession.Execute(context.Background(), loggedIn.Identity, websiteInbound.Conversation.ID)
		if err != nil || closedWebsite.Status != domain.ServiceSessionStatusClosed {
			t.Fatalf("closed participated website session = %#v, error = %v", closedWebsite, err)
		}
		allAfterWebsiteClosePage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{Scope: domain.InboxScopePending})
		allAfterWebsiteClose := allAfterWebsiteClosePage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertInboxConversationPresence(t, allAfterWebsiteClose, websiteInbound.Conversation.ID, false)
		closedInboxPage, _, err := inboxQuery.Execute(context.Background(), loggedIn.Identity, inboxaction.LoadInput{
			Scope: domain.InboxScopeAll, AssigneeFilter: domain.InboxAssigneeFilterIdentity, AssigneeIdentityID: loggedIn.Identity.OrganizationIdentity.ID, ServiceStatus: domain.ServiceSessionStatusClosed,
		})
		closedInbox := closedInboxPage.Conversations
		if err != nil {
			t.Fatal(err)
		}
		assertInboxConversationPresence(t, closedInbox, websiteInbound.Conversation.ID, true)

		if _, err := useraction.NewUpdateUserAction(db, testServiceSessionReturner(db), newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, createdMember.ID, useraction.UpdateInput{
			DisplayName: createdMember.DisplayName, RoleID: createdMember.RoleID, TeamIDs: []string{team.ID},
		}); err != nil {
			t.Fatal(err)
		}
		if err := teamaction.NewDeleteTeamAction(db, newTestTasks(db)).Execute(context.Background(), loggedIn.Identity, team.ID); err != nil {
			t.Fatal(err)
		}
		memberAfterTeamDelete, err := useraction.NewGetUserQuery(db).Execute(context.Background(), loggedIn.Identity, createdMember.ID)
		if err != nil || len(memberAfterTeamDelete.Teams) != 0 {
			t.Fatalf("member after team delete = %#v, error = %v", memberAfterTeamDelete, err)
		}
	})

	// 覆盖头像文件上传激活、个人资料更新、头像替换与过期清理流程。
	runStep("文件与个人资料", func(t *testing.T) {
		avatar, err := fileaction.NewCreateUploadAction(db).Execute(context.Background(), loggedIn.Identity, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeUserAvatar, FileName: "avatar.png", ContentType: "image/png", ByteSize: 1024,
		})
		if err != nil {
			t.Fatal(err)
		}
		if avatar.Status != string(domain.FileStatusPending) || avatar.ExpiresAt == nil {
			t.Fatalf("pending avatar = %#v", avatar)
		}
		avatar, err = markFileUploaded(context.Background(), db, loggedIn.Identity, avatar.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if avatar.Status != string(domain.FileStatusUploaded) || avatar.ExpiresAt == nil {
			t.Fatalf("uploaded avatar = %#v", avatar)
		}

		updatedIdentity, err := updateProfile.Execute(context.Background(), loggedIn.Identity, useraction.ProfileInput{
			DisplayName:  "  新姓名  ",
			Email:        " " + strings.ToUpper(profileEmail) + " ",
			AvatarFileID: avatar.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if updatedIdentity.OrganizationIdentity.DisplayName != "新姓名" || updatedIdentity.Account.Email != profileEmail || updatedIdentity.OrganizationIdentity.AvatarFileID == nil || *updatedIdentity.OrganizationIdentity.AvatarFileID != avatar.ID {
			t.Fatalf("updated identity = %#v", updatedIdentity)
		}
		activeAvatar := &servermodels.File{}
		if err := db.NewSelect().Model(activeAvatar).Where("f.id = ?", avatar.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if activeAvatar.Status != string(domain.FileStatusActive) || activeAvatar.ExpiresAt != nil {
			t.Fatalf("active avatar = %#v", activeAvatar)
		}
		resolvedAfterUpdate, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, loggedIn.Token)
		if err != nil {
			t.Fatal(err)
		}
		if resolvedAfterUpdate == nil || resolvedAfterUpdate.Account.Email != profileEmail || resolvedAfterUpdate.OrganizationIdentity.DisplayName != "新姓名" {
			t.Fatalf("identity after profile update = %#v", resolvedAfterUpdate)
		}
		replacement, err := fileaction.NewCreateUploadAction(db).Execute(context.Background(), resolvedAfterUpdate, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeUserAvatar, FileName: "replacement.webp", ContentType: "image/webp", ByteSize: 2048,
		})
		if err != nil {
			t.Fatal(err)
		}
		replacement, err = markFileUploaded(context.Background(), db, resolvedAfterUpdate, replacement.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		updatedIdentity, err = updateProfile.Execute(context.Background(), resolvedAfterUpdate, useraction.ProfileInput{
			DisplayName: "新姓名", Email: profileEmail, AvatarFileID: replacement.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if updatedIdentity.OrganizationIdentity.AvatarFileID == nil || *updatedIdentity.OrganizationIdentity.AvatarFileID != replacement.ID {
			t.Fatalf("replacement avatar identity = %#v", updatedIdentity)
		}
		if err := db.NewSelect().Model(activeAvatar).Where("f.id = ?", avatar.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if activeAvatar.Status != string(domain.FileStatusDeleting) || activeAvatar.ExpiresAt == nil {
			t.Fatalf("replaced avatar = %#v", activeAvatar)
		}
		if _, err := db.NewUpdate().Model((*servermodels.File)(nil)).
			Set("expires_at = ?", time.Now().UTC().Add(-time.Second)).
			Where("id = ?", avatar.ID).
			Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		localFiles, err := serverfilecontent.NewLocalStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		cleanup := filemaintenance.NewDeleteExpiredAction(db, serverfilecontent.NewDeleter(localFiles, serverfilecontent.S3Config{}))
		if err := cleanup.Execute(context.Background(), filemaintenance.DeleteExpiredInput{FileID: avatar.ID}); err != nil {
			t.Fatal(err)
		}
		resolvedAfterUpdate, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, loggedIn.Token)
		if err != nil {
			t.Fatal(err)
		}
		_, err = updateProfile.Execute(context.Background(), resolvedAfterUpdate, useraction.ProfileInput{
			DisplayName: "不应保存的姓名", Email: uniqueEmail("discarded"), AvatarFileID: "00000000-0000-0000-0000-000000000099",
		})
		if !errors.Is(err, fileaction.ErrLinkedImageNotFound) {
			t.Fatalf("invalid avatar error = %v, want file not found", err)
		}
		resolvedAfterUpdate, err = resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, loggedIn.Token)
		if err != nil {
			t.Fatal(err)
		}
		if resolvedAfterUpdate.Account.Email != profileEmail || resolvedAfterUpdate.OrganizationIdentity.DisplayName != "新姓名" {
			t.Fatalf("profile changed after invalid avatar: %#v", resolvedAfterUpdate)
		}
	})

	// 覆盖修改账号密码校验、新旧密码登录验证，以及其他登录会话随之失效。
	runStep("修改密码", func(t *testing.T) {
		otherSession, err := login.Execute(context.Background(), authaction.LoginInput{Email: profileEmail, Password: "password123"})
		if err != nil {
			t.Fatal(err)
		}
		changePassword := accountaction.NewChangePasswordAction(db)
		currentAccount := &servermodels.AccountIdentity{Account: resolvedAfterUpdate.Account, Session: resolvedAfterUpdate.Session}
		err = changePassword.Execute(context.Background(), currentAccount, accountaction.ChangePasswordInput{
			CurrentPassword: "incorrect-password",
			NewPassword:     "new-password123",
		})
		var passwordValidation *accountaction.ValidationError
		if !errors.As(err, &passwordValidation) || passwordValidation.Fields["currentPassword"] != accountaction.ValidationCurrentPasswordIncorrect {
			t.Fatalf("incorrect current password error = %v, want current password validation", err)
		}
		if err := changePassword.Execute(context.Background(), currentAccount, accountaction.ChangePasswordInput{
			CurrentPassword: "password123",
			NewPassword:     "new-password123",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := login.Execute(context.Background(), authaction.LoginInput{Email: profileEmail, Password: "password123"}); !errors.Is(err, authaction.ErrInvalidCredentials) {
			t.Fatalf("old password login error = %v, want invalid credentials", err)
		}
		if _, err := login.Execute(context.Background(), authaction.LoginInput{Email: profileEmail, Password: "new-password123"}); err != nil {
			t.Fatalf("new password login error = %v", err)
		}
		if _, err := resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, otherSession.Token); !errors.Is(err, authaction.ErrIdentityNotFound) {
			t.Fatalf("other session after password change error = %v, want ErrIdentityNotFound", err)
		}
		if _, err := resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, loggedIn.Token); err != nil {
			t.Fatalf("current session after password change error = %v", err)
		}
	})

	// 覆盖个人资料邮箱与其他账号重复时校验失败、头像文件保留并可重试的流程。
	runStep("邮箱冲突与头像重试", func(t *testing.T) {
		otherEmail := uniqueEmail("other")
		otherAccount, err := identityaction.CreateAccount(context.Background(), db, identityaction.NewAccount{
			Email: otherEmail, PasswordHash: "unused", DisplayName: "其他成员", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
		})
		if err != nil {
			t.Fatal(err)
		}
		otherIdentity := &servermodels.OrganizationIdentity{
			OrganizationID: loggedIn.Identity.Organization.ID,
			Type:           string(domain.OrganizationIdentityTypeUser), DisplayName: "其他成员", WorkStatus: string(domain.WorkStatusWorking),
		}
		if _, err := db.NewInsert().Model(otherIdentity).
			Column("organization_id", "type", "display_name", "work_status").Returning("id").Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		otherUser := &servermodels.User{
			IdentityID:     otherIdentity.ID,
			OrganizationID: loggedIn.Identity.Organization.ID,
			AccountID:      otherAccount.ID,
			RoleID:         memberRole.ID,
			Status:         string(domain.IdentityStatusActive),
		}
		if _, err := db.NewInsert().Model(otherUser).
			Column("identity_id", "organization_id", "account_id", "role_id", "status").
			Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		retryAvatar, err := fileaction.NewCreateUploadAction(db).Execute(context.Background(), resolvedAfterUpdate, domain.FileStorageBackendLocal, fileaction.UploadInput{
			Purpose: domain.FilePurposeUserAvatar, FileName: "retry.png", ContentType: "image/png", ByteSize: 4096,
		})
		if err != nil {
			t.Fatal(err)
		}
		retryAvatar, err = markFileUploaded(context.Background(), db, resolvedAfterUpdate, retryAvatar.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		_, err = updateProfile.Execute(context.Background(), resolvedAfterUpdate, useraction.ProfileInput{
			DisplayName:  "新姓名",
			Email:        strings.ToUpper(otherEmail),
			AvatarFileID: retryAvatar.ID,
		})
		var profileValidation *useraction.ValidationError
		if !errors.As(err, &profileValidation) || profileValidation.Fields["email"] != useraction.ValidationEmailDuplicate {
			t.Fatalf("duplicate profile email error = %v, want email validation", err)
		}
		if err := db.NewSelect().Model(retryAvatar).Where("f.id = ?", retryAvatar.ID).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if retryAvatar.Status != string(domain.FileStatusUploaded) || retryAvatar.ExpiresAt == nil {
			t.Fatalf("retry avatar after validation failure = %#v", retryAvatar)
		}
		updatedIdentity, err := updateProfile.Execute(context.Background(), resolvedAfterUpdate, useraction.ProfileInput{
			DisplayName: "新姓名", Email: profileEmail, AvatarFileID: retryAvatar.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if updatedIdentity.OrganizationIdentity.AvatarFileID == nil || *updatedIdentity.OrganizationIdentity.AvatarFileID != retryAvatar.ID {
			t.Fatalf("retried profile avatar = %#v", updatedIdentity)
		}
	})

	// 覆盖联系人的创建、联系方式保留、渠道不可变校验、软删除恢复与失效身份拦截。
	runStep("联系人管理", func(t *testing.T) {
		createContact := contactaction.NewCreateContactAction(db)
		_, err := createContact.Execute(context.Background(), loggedIn.Identity, contactaction.ContactInput{
			DisplayName: "无效渠道联系人",
			ChannelID:   "00000000-0000-0000-0000-000000000099",
			Stage:       domain.ContactStageVisitor,
		})
		var channelValidation *contactaction.ValidationError
		if !errors.As(err, &channelValidation) || channelValidation.Fields["channelId"] != contactaction.ValidationChannelInvalid {
			t.Fatalf("invalid channel error = %v, want channel validation", err)
		}

		contact, err := createContact.Execute(context.Background(), loggedIn.Identity, contactaction.ContactInput{
			DisplayName: "林晓",
			ChannelID:   channel.ID,
			Stage:       domain.ContactStageLead,
			Notes:       "采购负责人",
			Methods: []contactaction.MethodInput{
				{Type: domain.ContactMethodTypeEmail, Value: "LIN@example.com", Label: "工作"},
				{Type: domain.ContactMethodTypeEmail, Value: "lin.private@example.com", Label: "私人"},
				{Type: domain.ContactMethodTypePhone, Value: "+86 138-0000-0000", Label: "手机"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if contact.Contact.DisplayName == nil || *contact.Contact.DisplayName != "林晓" || contact.Contact.SourceChannelID != channel.ID || contact.SourceChannel.ID != channel.ID || len(contact.Methods) != 3 {
			t.Fatalf("unexpected created contact: %#v", contact)
		}
		type methodIdentity struct {
			typeName string
			value    string
		}
		storedMethods := make([]servermodels.ContactMethod, 0)
		if err := db.NewSelect().
			Model(&storedMethods).
			Where("cm.organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("cm.contact_id = ?", contact.Contact.ID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		preservedMethods := make(map[methodIdentity]servermodels.ContactMethod, len(storedMethods))
		for _, method := range storedMethods {
			preservedMethods[methodIdentity{typeName: method.Type, value: method.Value}] = method
		}
		preservedInputs := make([]contactaction.MethodInput, 0, len(contact.Methods))
		for _, method := range contact.Methods {
			label := ""
			if method.Label != nil {
				label = *method.Label
			}
			preservedInputs = append(preservedInputs, contactaction.MethodInput{
				Type: method.Type, Value: method.Value, Label: label, IsPrimary: method.IsPrimary,
			})
		}
		preservedContact, err := contactaction.NewUpdateContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID, contactaction.ContactInput{
			DisplayName: "林晓（已确认）",
			ChannelID:   channel.ID,
			Stage:       domain.ContactStageLead,
			Notes:       "采购负责人",
			Methods:     preservedInputs,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(preservedContact.Methods) != len(contact.Methods) {
			t.Fatalf("preserved methods count = %d, want %d", len(preservedContact.Methods), len(contact.Methods))
		}
		afterMethods := make([]servermodels.ContactMethod, 0)
		if err := db.NewSelect().
			Model(&afterMethods).
			Where("cm.organization_id = ?", loggedIn.Identity.Organization.ID).
			Where("cm.contact_id = ?", contact.Contact.ID).
			Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, method := range afterMethods {
			before, ok := preservedMethods[methodIdentity{typeName: method.Type, value: method.Value}]
			labelsEqual := (method.Label == nil && before.Label == nil) ||
				(method.Label != nil && before.Label != nil && *method.Label == *before.Label)
			if !ok || method.ID != before.ID || !labelsEqual || method.IsPrimary != before.IsPrimary || !method.CreatedAt.Equal(before.CreatedAt) || !method.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatalf("contact method was recreated or changed: before=%#v after=%#v", before, method)
			}
		}

		contactList := contactaction.NewListContactsQuery(db)
		activeContacts, err := contactList.Execute(context.Background(), loggedIn.Identity, contactaction.ListInput{
			Query: "lin@example", Stage: domain.ContactStageLead, ChannelID: channel.ID, Page: 1, PageSize: 50,
		})
		if err != nil {
			t.Fatal(err)
		}
		if activeContacts.Page.Total != 1 || len(activeContacts.Contacts) != 1 || activeContacts.Contacts[0].PrimaryEmail == nil || activeContacts.Contacts[0].SourceChannelName != channel.Name {
			t.Fatalf("unexpected contact list: %#v", activeContacts)
		}

		// 联系人编号在工作区内按创建顺序递增，列表可按访客编号名称检索。
		next, err := createContact.Execute(context.Background(), loggedIn.Identity, contactaction.ContactInput{DisplayName: "周宁", ChannelID: channel.ID, Stage: domain.ContactStageVisitor})
		if err != nil {
			t.Fatal(err)
		}
		if contact.Contact.Number < 1 || next.Contact.Number != contact.Contact.Number+1 {
			t.Fatalf("contact numbers = %d, %d", contact.Contact.Number, next.Contact.Number)
		}
		numbered, err := contactList.Execute(context.Background(), loggedIn.Identity, contactaction.ListInput{
			Query: fmt.Sprintf("访客 #%d", next.Contact.Number), Page: 1, PageSize: 50,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(numbered.Contacts) != 1 || numbered.Contacts[0].ID != next.Contact.ID || numbered.Contacts[0].Number != next.Contact.Number {
			t.Fatalf("contacts by number = %#v", numbered)
		}

		_, err = contactaction.NewUpdateContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID, contactaction.ContactInput{
			DisplayName: "林晓",
			ChannelID:   "00000000-0000-0000-0000-000000000099",
			Stage:       domain.ContactStageLead,
			Methods:     []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "lin@example.com"}},
		})
		var immutableChannelValidation *contactaction.ValidationError
		if !errors.As(err, &immutableChannelValidation) || immutableChannelValidation.Fields["channelId"] != contactaction.ValidationChannelImmutable {
			t.Fatalf("immutable channel error = %v, want channel validation", err)
		}

		updatedContact, err := contactaction.NewUpdateContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID, contactaction.ContactInput{
			DisplayName: "林晓（采购）",
			ChannelID:   channel.ID,
			Stage:       domain.ContactStageCustomer,
			Methods:     []contactaction.MethodInput{{Type: domain.ContactMethodTypeEmail, Value: "lin@example.com"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if updatedContact.Contact.Stage != domain.ContactStageCustomer || len(updatedContact.Methods) != 1 {
			t.Fatalf("unexpected updated contact: %#v", updatedContact)
		}

		if _, err := db.NewUpdate().Table("users").Set("status = 'inactive'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		// 成员停用只影响该工作区：账号仍能登录，但不能解析出该工作区的成员身份。
		inactiveSession, err := login.Execute(context.Background(), authaction.LoginInput{Email: profileEmail, Password: "new-password123"})
		if err != nil {
			t.Fatalf("inactive member account login error = %v", err)
		}
		if _, err := resolveIdentity.Execute(context.Background(), loggedIn.Identity.Organization.ID, inactiveSession.Token); !errors.Is(err, authaction.ErrMembershipNotFound) {
			t.Fatalf("inactive member identity error = %v, want ErrMembershipNotFound", err)
		}
		if err := contactaction.NewDeleteContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID); !errors.Is(err, identityaction.ErrInvalid) {
			t.Fatalf("inactive user delete error = %v, want %v", err, identityaction.ErrInvalid)
		}
		if _, err := db.NewUpdate().Table("users").Set("status = 'active'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := contactaction.NewDeleteContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID); err != nil {
			t.Fatal(err)
		}
		deletedContacts, err := contactList.Execute(context.Background(), loggedIn.Identity, contactaction.ListInput{Deleted: true, Page: 1, PageSize: 50})
		if err != nil {
			t.Fatal(err)
		}
		if deletedContacts.Page.Total != 1 || len(deletedContacts.Contacts) != 1 {
			t.Fatalf("unexpected deleted contact list: %#v", deletedContacts)
		}
		if _, err := db.NewUpdate().Table("users").Set("status = 'inactive'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := contactaction.NewRestoreContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID); !errors.Is(err, identityaction.ErrInvalid) {
			t.Fatalf("inactive user restore error = %v, want %v", err, identityaction.ErrInvalid)
		}
		if _, err := db.NewUpdate().Table("users").Set("status = 'active'").Where("id = ?", loggedIn.Identity.User.ID).Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := contactaction.NewRestoreContactAction(db).Execute(context.Background(), loggedIn.Identity, contact.Contact.ID); err != nil {
			t.Fatal(err)
		}
	})

	// 覆盖联系人字段与标签的定义维护、取值校验、单选选项移除清空取值、标签筛选和删除定义时的级联清理。
	runStep("联系人档案", func(t *testing.T) {
		ctx := context.Background()
		identity := loggedIn.Identity
		contact, err := contactaction.NewCreateContactAction(db).Execute(ctx, identity, contactaction.ContactInput{
			DisplayName: "档案联系人", ChannelID: channel.ID, Stage: domain.ContactStageCustomer, Notes: "偏好邮件沟通",
		})
		if err != nil {
			t.Fatal(err)
		}
		createField := contactprofileaction.NewCreateFieldAction(db)
		company, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: " 公司 ", Type: domain.ContactFieldTypeText})
		if err != nil {
			t.Fatal(err)
		}
		if company.Name != "公司" || len(company.Options) != 0 {
			t.Fatalf("created text field = %#v", company)
		}
		var duplicate *common.FieldError
		if _, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: "公司", Type: domain.ContactFieldTypeNumber}); !errors.As(err, &duplicate) || duplicate.Fields["name"] != contactprofileaction.ValidationNameDuplicate {
			t.Fatalf("duplicate field error = %v", err)
		}
		seats, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: "席位数", Type: domain.ContactFieldTypeNumber})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := createField.Execute(ctx, identity, contactprofileaction.FieldInput{Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{{Name: "基础版"}, {Name: "专业版"}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Options) != 2 || plan.Options[0].ID == "" || plan.Options[1].ID == "" {
			t.Fatalf("select options = %#v", plan.Options)
		}

		setValue := contactprofileaction.NewSetFieldValueAction(db)
		if err := setValue.Execute(ctx, identity, contact.Contact.ID, company.ID, "  Acme  "); err != nil {
			t.Fatal(err)
		}
		var invalid *common.FieldError
		for _, input := range []string{"二十", "1e2", "1.", ".5"} {
			if err := setValue.Execute(ctx, identity, contact.Contact.ID, seats.ID, input); !errors.As(err, &invalid) || invalid.Fields["value"] != contactprofileaction.ValidationValueInvalid {
				t.Fatalf("invalid number %q error = %v", input, err)
			}
		}
		// 数字按十进制文本规范化，不经过二进制浮点。
		for _, number := range []struct{ input, want string }{{"9007199254740993", "9007199254740993"}, {"-0.00", "0"}, {"+020.50", "20.5"}} {
			input, want := number.input, number.want
			if err := setValue.Execute(ctx, identity, contact.Contact.ID, seats.ID, input); err != nil {
				t.Fatal(err)
			}
			stored := &servermodels.ContactFieldValue{}
			if err := db.NewSelect().Model(stored).Where("cfv.contact_id = ? AND cfv.field_id = ?", contact.Contact.ID, seats.ID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if stored.Value != want {
				t.Fatalf("normalized number %q = %q, want %q", input, stored.Value, want)
			}
		}
		// 档案实际变化时更新联系人的更新时间。
		var beforeTouch time.Time
		if err := db.NewSelect().Table("contacts").Column("updated_at").Where("id = ?", contact.Contact.ID).Scan(ctx, &beforeTouch); err != nil {
			t.Fatal(err)
		}
		if err := setValue.Execute(ctx, identity, contact.Contact.ID, plan.ID, "unknown-option"); !errors.As(err, &invalid) || invalid.Fields["value"] != contactprofileaction.ValidationValueInvalid {
			t.Fatalf("invalid option error = %v", err)
		}
		if err := setValue.Execute(ctx, identity, contact.Contact.ID, plan.ID, plan.Options[1].ID); err != nil {
			t.Fatal(err)
		}
		var afterTouch time.Time
		if err := db.NewSelect().Table("contacts").Column("updated_at").Where("id = ?", contact.Contact.ID).Scan(ctx, &afterTouch); err != nil {
			t.Fatal(err)
		}
		if !afterTouch.After(beforeTouch) {
			t.Fatalf("contact updated_at = %v, want after %v", afterTouch, beforeTouch)
		}

		createTag := contactprofileaction.NewCreateTagAction(db)
		vip, err := createTag.Execute(ctx, identity, contactprofileaction.TagInput{Name: "VIP"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := createTag.Execute(ctx, identity, contactprofileaction.TagInput{Name: "vip"}); !errors.As(err, &duplicate) || duplicate.Fields["name"] != contactprofileaction.ValidationNameDuplicate {
			t.Fatalf("duplicate tag error = %v", err)
		}
		addTag := contactprofileaction.NewAddTagAction(db)
		for range 2 {
			if err := addTag.Execute(ctx, identity, contact.Contact.ID, vip.ID); err != nil {
				t.Fatal(err)
			}
		}

		detail, err := contactaction.NewGetContactQuery(db).Execute(ctx, identity, contact.Contact.ID)
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		for _, value := range detail.Profile.Fields {
			if value.Source != domain.ContactProfileSourceMember {
				t.Fatalf("field value source = %q", value.Source)
			}
			values[value.FieldID] = value.Value
		}
		if values[company.ID] != "Acme" || values[seats.ID] != "20.5" || values[plan.ID] != plan.Options[1].ID || len(detail.Profile.Tags) != 1 || detail.Profile.Tags[0].ID != vip.ID {
			t.Fatalf("contact profile = %#v", detail.Profile)
		}

		agentProfile, err := contactprofileaction.LoadAgentProfile(ctx, db, identity.Organization.ID, contact.Contact.ID)
		if err != nil {
			t.Fatal(err)
		}
		if agentProfile.Stage != string(domain.ContactStageCustomer) || agentProfile.Notes != "偏好邮件沟通" || agentProfile.Fields["套餐"] != "专业版" || agentProfile.Fields["席位数"] != "20.5" || len(agentProfile.Tags) != 1 || agentProfile.Tags[0] != "VIP" {
			t.Fatalf("agent profile = %#v", agentProfile)
		}

		renamedTag, err := contactprofileaction.NewUpdateTagAction(db).Execute(ctx, identity, vip.ID, contactprofileaction.TagInput{Name: "重要客户"})
		if err != nil {
			t.Fatal(err)
		}
		if renamedTag.Name != "重要客户" {
			t.Fatalf("renamed tag = %#v", renamedTag)
		}
		if _, err := contactprofileaction.NewUpdateTagAction(db).Execute(ctx, identity, vip.ID, contactprofileaction.TagInput{Name: "VIP"}); err != nil {
			t.Fatal(err)
		}

		listed, err := contactaction.NewListContactsQuery(db).Execute(ctx, identity, contactaction.ListInput{TagID: vip.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Contacts) != 1 || listed.Contacts[0].ID != contact.Contact.ID || len(listed.Contacts[0].Tags) != 1 || listed.Contacts[0].Tags[0].Name != "VIP" {
			t.Fatalf("contacts filtered by tag = %#v", listed.Contacts)
		}

		// 移除被选中的选项后该字段取值随之清空，类型不可修改。
		updateField := contactprofileaction.NewUpdateFieldAction(db)
		var immutable *common.FieldError
		if _, err := updateField.Execute(ctx, identity, plan.ID, contactprofileaction.FieldInput{Name: "套餐", Type: domain.ContactFieldTypeText}); !errors.As(err, &immutable) || immutable.Fields["type"] != contactprofileaction.ValidationFieldTypeImmutable {
			t.Fatalf("change field type error = %v", err)
		}
		if _, err := updateField.Execute(ctx, identity, plan.ID, contactprofileaction.FieldInput{Name: "套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{plan.Options[0], {ID: plan.Options[0].ID, Name: "重复编号"}}}); !errors.As(err, &invalid) || invalid.Fields["options"] != contactprofileaction.ValidationOptionInvalid {
			t.Fatalf("duplicate option id error = %v", err)
		}
		renamed, err := updateField.Execute(ctx, identity, plan.ID, contactprofileaction.FieldInput{Name: "订阅套餐", Type: domain.ContactFieldTypeSelect, Options: []domain.ContactFieldOption{plan.Options[0], {Name: "企业版"}}})
		if err != nil {
			t.Fatal(err)
		}
		if renamed.Name != "订阅套餐" || len(renamed.Options) != 2 || renamed.Options[0].ID != plan.Options[0].ID || renamed.Options[1].ID == plan.Options[1].ID {
			t.Fatalf("updated select field = %#v", renamed)
		}
		if err := setValue.Execute(ctx, identity, contact.Contact.ID, company.ID, ""); err != nil {
			t.Fatal(err)
		}
		profile, err := contactprofileaction.Load(ctx, db, identity.Organization.ID, contact.Contact.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(profile.Fields) != 1 || profile.Fields[0].FieldID != seats.ID {
			t.Fatalf("profile after option removal and clearing = %#v", profile.Fields)
		}

		if err := contactprofileaction.NewDeleteFieldAction(db).Execute(ctx, identity, seats.ID); err != nil {
			t.Fatal(err)
		}
		if err := contactprofileaction.NewDeleteTagAction(db).Execute(ctx, identity, vip.ID); err != nil {
			t.Fatal(err)
		}
		profile, err = contactprofileaction.Load(ctx, db, identity.Organization.ID, contact.Contact.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(profile.Fields) != 0 || len(profile.Tags) != 0 {
			t.Fatalf("profile after deleting definitions = %#v", profile)
		}
		orphans, err := db.NewSelect().Model((*servermodels.ContactFieldValue)(nil)).Where("field_id = ?", seats.ID).Count(ctx)
		if err != nil || orphans != 0 {
			t.Fatalf("orphan field values = %d, %v", orphans, err)
		}
		for _, field := range []string{company.ID, plan.ID} {
			if err := contactprofileaction.NewDeleteFieldAction(db).Execute(ctx, identity, field); err != nil {
				t.Fatal(err)
			}
		}
		if err := contactaction.NewDeleteContactAction(db).Execute(ctx, identity, contact.Contact.ID); err != nil {
			t.Fatal(err)
		}
	})
}

type telegramBotAPIFake struct {
	mu             sync.Mutex
	bot            telegramintegration.Bot
	getMeTokens    []string
	setWebhooks    []telegramintegration.Webhook
	deleteWebhooks []string
}

// GetMe 记录 Token 并返回测试机器人。
func (f *telegramBotAPIFake) GetMe(_ context.Context, token string) (telegramintegration.Bot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMeTokens = append(f.getMeTokens, token)
	return f.bot, nil
}

// SetWebhook 记录最后一次注册参数。
func (f *telegramBotAPIFake) SetWebhook(_ context.Context, _ string, webhook telegramintegration.Webhook) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setWebhooks = append(f.setWebhooks, webhook)
	return nil
}

// DeleteWebhook 记录被清理的 Token。
func (f *telegramBotAPIFake) DeleteWebhook(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteWebhooks = append(f.deleteWebhooks, token)
	return nil
}

// webhooks 返回注册调用快照。
func (f *telegramBotAPIFake) webhooks() []telegramintegration.Webhook {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]telegramintegration.Webhook(nil), f.setWebhooks...)
}

// deletedTokens 返回删除调用快照。
func (f *telegramBotAPIFake) deletedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleteWebhooks...)
}

var _ telegramintegration.BotAPI = (*telegramBotAPIFake)(nil)

type telegramProfilePhotoAPIStub struct {
	photo      *telegramintegration.ProfilePhoto
	downloaded telegramintegration.DownloadedPhoto
	err        error
}

// GetUserProfilePhoto 返回测试预设的当前头像。
func (s *telegramProfilePhotoAPIStub) GetUserProfilePhoto(context.Context, string, int64) (*telegramintegration.ProfilePhoto, error) {
	return s.photo, s.err
}

// DownloadPhoto 返回测试预设的头像内容。
func (s *telegramProfilePhotoAPIStub) DownloadPhoto(context.Context, string, string) (telegramintegration.DownloadedPhoto, error) {
	return s.downloaded, s.err
}

var _ telegramintegration.ProfilePhotoAPI = (*telegramProfilePhotoAPIStub)(nil)

type importedFileWriterStub struct {
	saved int
}

// Save 接受测试导入内容并返回固定 ETag。
func (s *importedFileWriterStub) Save(context.Context, *servermodels.File, []byte) (string, error) {
	s.saved++
	return "telegram-avatar-etag", nil
}

var _ fileaction.ContentWriter = (*importedFileWriterStub)(nil)

// agentResponseFailureHook 在启用期间以已取消的上下文执行指定会话的 AI 回复写入，使该次写入失败。
type agentResponseFailureHook struct {
	conversationID string
	armed          atomic.Bool
}

// BeforeQuery 对启用期间指定会话带 agent 幂等键的消息写入返回已取消的上下文。
func (h *agentResponseFailureHook) BeforeQuery(ctx context.Context, event *bun.QueryEvent) context.Context {
	if !h.armed.Load() || !strings.HasPrefix(event.Query, `INSERT INTO "messages"`) ||
		!strings.Contains(event.Query, h.conversationID) || !strings.Contains(event.Query, "'agent:") {
		return ctx
	}
	failed, cancel := context.WithCancel(ctx)
	cancel()
	return failed
}

// AfterQuery 不处理查询结果。
func (h *agentResponseFailureHook) AfterQuery(context.Context, *bun.QueryEvent) {}
