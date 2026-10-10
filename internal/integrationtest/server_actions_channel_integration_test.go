//go:build server

package integrationtest

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	channelinboundaction "github.com/runforyou-ai/luway/internal/actions/channelinbound"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/runforyou-ai/support"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	telegramintegration "github.com/runforyou-ai/luway/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/stretchr/testify/require"
)

// testMessageChannels 覆盖消息渠道的创建、详情、聊天界面配置、更新与启停列表流程。
func (s *serverActionsFixture) testMessageChannels(t *testing.T) {
	db, loggedIn, getChannel, updateChannel := s.db, s.loggedIn, s.getChannel, s.updateChannel
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
	require.ErrorIs(t, err, identityaction.ErrInvalid, "stale identity")

	s.channel, err = createChannel.Execute(context.Background(), loggedIn.Identity, channelaction.CreateMessageChannelInput{
		Type:                  domain.ChannelTypeWebsite,
		Name:                  "产品官网",
		Description:           "接收官网访客咨询",
		DefaultLocale:         domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	require.Equal(t, string(domain.ChannelTypeWebsite), s.channel.Type)
	require.Equal(t, loggedIn.Identity.User.ID, s.channel.CreatedByUserID)

	detail, err := getChannel.Execute(context.Background(), loggedIn.Identity, s.channel.ID)
	require.NoError(t, err)
	require.Equal(t, "产品官网", detail.ChatInterface.ChatTitle)
	require.Equal(t, channelaction.DefaultWebsiteChannelThemeColor, detail.ChatInterface.ThemeColor)

	s.telegramChannel, err = createChannel.Execute(context.Background(), loggedIn.Identity, channelaction.CreateMessageChannelInput{
		Type:                  domain.ChannelTypeTelegram,
		Name:                  "Telegram 客服",
		DefaultLocale:         domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	telegramDetail, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	require.Equal(t, string(domain.ChannelTypeTelegram), telegramDetail.Type)
	require.Empty(t, telegramDetail.Connection.BotToken)
	require.Nil(t, telegramDetail.Connection.WebhookStatus)
	telegramSettingCount, err := db.NewSelect().
		Model((*servermodels.TelegramChannelSetting)(nil)).
		Where("tcs.channel_id = ?", s.telegramChannel.ID).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(1), telegramSettingCount, "telegram setting count")

	telegramAPI := &telegramBotAPIFake{bot: telegramintegration.Bot{
		ID: 987654321, IsBot: true, FirstName: "Demo", LastName: "Support", Username: "demo_support_bot",
	}}
	telegramRunner := connectiontest.NewRunner(time.Second)
	testTelegram := telegramaction.NewTestConnectionAction(db, telegramRunner, telegramAPI)
	require.NoError(t, testTelegram.Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID, telegramaction.ConnectionTestInput{BotToken: "123456:draft_token"}))
	detailAfterTest, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	require.Empty(t, detailAfterTest.Connection.BotToken)
	require.Nil(t, detailAfterTest.Connection.WebhookStatus)
	require.Empty(t, telegramAPI.webhooks())

	saveTelegram := telegramaction.NewSaveConnectionAction(db, telegramRunner, telegramAPI)
	savedTelegram, err := saveTelegram.Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect,
		BotToken:       "123456:saved_token",
		WebhookBaseURL: "http://127.0.0.1:34115/app",
	})
	require.NoError(t, err)
	require.Equal(t, &telegramAPI.bot.ID, savedTelegram.Connection.BotID)
	require.Equal(t, &telegramAPI.bot.Username, savedTelegram.Connection.BotUsername)
	require.Equal(t, new("Demo Support"), savedTelegram.Connection.BotDisplayName)
	require.Equal(t, new(string(domain.TelegramWebhookStatusWaiting)), savedTelegram.Connection.WebhookStatus)
	const expectedTelegramWebhookURL = "http://127.0.0.1:34115/app/api/public/telegram-channels/"
	require.Equal(t, expectedTelegramWebhookURL+s.telegramChannel.ID+"/webhook", savedTelegram.Connection.WebhookURL)
	webhooks := telegramAPI.webhooks()
	require.Len(t, webhooks, 1)
	require.Equal(t, savedTelegram.Connection.WebhookURL, webhooks[0].URL)
	require.Equal(t, savedTelegram.Connection.WebhookSecret, webhooks[0].Secret)

	telegramAvatarAPI := &telegramProfilePhotoAPIStub{
		photo:      &telegramintegration.ProfilePhoto{FileID: "avatar-file-1", UniqueID: "avatar-version-1"},
		downloaded: telegramintegration.DownloadedPhoto{ContentType: "image/jpeg", Data: []byte{0xff, 0xd8, 0xff}},
	}
	importedAvatarWriter := &importedFileWriterStub{}
	telegramAvatarFiles := fileaction.NewImportAction(db, localStorage, importedAvatarWriter)
	telegramAdapters := channeladapter.NewRegistry()
	telegramAdapters.Register(domain.ChannelTypeTelegram, telegramaction.NewAdapter(db, nil, nil, telegramAvatarAPI))
	telegramScheduler := agentrunaction.NewScheduler(testEnqueuer)
	receiveTelegram := newTelegramWebhookWith(db, telegramAdapters, telegramScheduler, testEnqueuer, channelinboundaction.NewRetrieveMediaAction(db, telegramAdapters, nil, telegramScheduler))
	refreshTelegramAvatar := channelinboundaction.NewRefreshAvatarAction(db, telegramAdapters, telegramAvatarFiles)
	// loadTelegramIdentity 读取 Telegram 客户的渠道身份。
	loadTelegramIdentity := func() servermodels.ChannelIdentity {
		t.Helper()
		identity := servermodels.ChannelIdentity{}
		require.NoError(t, db.NewSelect().Model(&identity).
			Where("ci.channel_id = ? AND ci.external_id = ?", s.telegramChannel.ID, "998877").
			Scan(context.Background()))
		return identity
	}
	completedAvatarRefreshes := 0
	// 按任务参数执行一次头像同步，并取走已投递的同步任务。
	runTelegramAvatarRefresh := func() {
		t.Helper()
		identity := loadTelegramIdentity()
		require.NoError(t, refreshTelegramAvatar.Execute(context.Background(), channelinboundaction.RefreshAvatarInput{
			WorkspaceID: loggedIn.Identity.Workspace.ID, ChannelID: s.telegramChannel.ID, AccountID: strconv.FormatInt(telegramAPI.bot.ID, 10), ChannelIdentityID: identity.ID,
		}))
		// 模拟任务运行时完成本次同步任务。
		completedAvatarRefreshes += len(servertest.TakeInputs(t, testEnqueuer, channelinboundaction.RefreshAvatarActionName, func(input channelinboundaction.RefreshAvatarInput) bool {
			return input.ChannelIdentityID == identity.ID
		}))
	}
	// 统计渠道身份已投递的头像同步任务数，含已完成的任务。
	countTelegramAvatarRefreshes := func() int {
		t.Helper()
		identity := loadTelegramIdentity()
		return completedAvatarRefreshes + len(testEnqueuer.Keyed(channelinboundaction.RefreshAvatarActionName, "chavatar:"+identity.ID))
	}
	require.ErrorIs(t, receiveTelegram.Preflight(context.Background(), s.telegramChannel.ID, "wrong-secret"), telegramaction.ErrWebhookUnauthorized, "wrong secret")
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramUpdate{
		Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 1,
	}), "ignored update")
	connectedTelegram, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	require.Equal(t, new(string(domain.TelegramWebhookStatusNormal)), connectedTelegram.Connection.WebhookStatus, "status after ignored update")
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramUpdate{
		Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 2, MyChatMember: true,
	}))
	connectedTelegram, err = telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	require.Equal(t, new(string(domain.TelegramWebhookStatusNormal)), connectedTelegram.Connection.WebhookStatus, "connected Telegram status")
	telegramOriginatedAt := time.Date(2026, time.August, 30, 5, 6, 7, 0, time.UTC)
	telegramMessage := telegramUpdate{
		Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 3,
		Message: &telegramintegration.InboundMessage{
			ChatID: 998877, MessageID: 41, SenderID: 998877,
			DisplayName: "Telegram 访客", Body: "Telegram 私聊消息",
			OriginatedAt: telegramOriginatedAt,
		},
	}
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramMessage))
	// 首条入站消息投递头像同步任务。
	require.Equal(t, 1, countTelegramAvatarRefreshes(), "Telegram avatar refreshes after first message")
	runTelegramAvatarRefresh()
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramMessage), "duplicate Telegram message")
	// 头像接口失败只记录日志，保留现有头像。
	telegramAvatarAPI.err = errors.New("avatar unavailable")
	runTelegramAvatarRefresh()
	telegramAvatarAPI.err = nil
	telegramMessages := make([]servermodels.Message, 0)
	require.NoError(t, db.NewSelect().Model(&telegramMessages).
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.workspace_id = msg.workspace_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		OrderExpr("msg.message_seq ASC").
		Scan(context.Background()))
	require.Len(t, telegramMessages, 1)
	require.Equal(t, telegramMessage.Message.Body, telegramMessages[0].Body)
	require.True(t, telegramMessages[0].OriginatedAt.Equal(telegramOriginatedAt), "Telegram message originated at = %v", telegramMessages[0].OriginatedAt)
	telegramMessage.UpdateID = 4
	telegramMessage.Message.MessageID = 42
	telegramMessage.Message.DisplayName = "Telegram 新名称"
	telegramMessage.Message.Body = "同秒第二条消息"
	telegramAvatarAPI.photo = &telegramintegration.ProfilePhoto{FileID: "avatar-file-2", UniqueID: "avatar-version-2"}
	// 同步间隔内的新消息不再投递头像同步任务。
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramMessage))
	require.Equal(t, 1, countTelegramAvatarRefreshes(), "Telegram avatar refreshes within interval")
	runTelegramAvatarRefresh()
	var telegramIdentity servermodels.ChannelIdentity
	require.NoError(t, db.NewSelect().Model(&telegramIdentity).
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		Scan(context.Background()))
	require.Equal(t, &telegramMessage.Message.DisplayName, telegramIdentity.DisplayName)
	require.NotNil(t, telegramIdentity.AvatarFileID, "Telegram identity avatar = %#v", telegramIdentity)
	telegramAvatarFilesInDatabase := make([]servermodels.File, 0)
	require.NoError(t, db.NewSelect().Model(&telegramAvatarFilesInDatabase).
		Where("f.workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("f.purpose = ?", domain.FilePurposeContactAvatar).
		OrderExpr("f.created_at ASC, f.id ASC").
		Scan(context.Background()))
	require.Len(t, telegramAvatarFilesInDatabase, 2)
	require.Equal(t, string(domain.FileStatusDeleting), telegramAvatarFilesInDatabase[0].Status)
	require.Equal(t, new("avatar-version-1"), telegramAvatarFilesInDatabase[0].ExternalID)
	require.Equal(t, *telegramIdentity.AvatarFileID, telegramAvatarFilesInDatabase[1].ID)
	require.Equal(t, string(domain.FileStatusActive), telegramAvatarFilesInDatabase[1].Status)
	require.Equal(t, string(domain.FileStorageBackendLocal), telegramAvatarFilesInDatabase[1].StorageBackend)
	require.Equal(t, "image/jpeg", telegramAvatarFilesInDatabase[1].ContentType)
	require.Equal(t, new("avatar-version-2"), telegramAvatarFilesInDatabase[1].ExternalID)
	require.Equal(t, 2, importedAvatarWriter.saved, "imported Telegram avatar writes")
	latestTelegramMessage := servermodels.Message{}
	require.NoError(t, db.NewSelect().Model(&latestTelegramMessage).
		Join("JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.workspace_id = msg.workspace_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		OrderExpr("msg.message_seq DESC").
		Limit(1).
		Scan(context.Background()))
	require.Equal(t, telegramMessage.Message.Body, latestTelegramMessage.Body)
	require.True(t, latestTelegramMessage.OriginatedAt.Equal(telegramOriginatedAt), "latest Telegram message originated at = %v", latestTelegramMessage.OriginatedAt)
	telegramConversation := struct {
		ID string `bun:"id"`
	}{}
	err = db.NewSelect().
		TableExpr("channel_conversations AS cc").
		ColumnExpr("cc.conversation_id AS id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		Scan(context.Background(), &telegramConversation)
	require.NoError(t, err)
	s.telegramConversationID = telegramConversation.ID
	// 头像未变化的消息只因追加消息推进一次会话版本。
	versionBeforeSameAvatar := loadConversationVersion(t, db, telegramConversation.ID)
	sameAvatarMessage := *telegramMessage.Message
	sameAvatarMessage.MessageID = 43
	sameAvatarMessage.Body = "头像未变化的 Telegram 消息"
	// 超过同步间隔后的新消息重新投递头像同步任务。
	_, err = db.ExecContext(context.Background(), "UPDATE channel_identities SET avatar_checked_at = now() - interval '25 hours' WHERE channel_id = ? AND external_id = ?", s.telegramChannel.ID, "998877")
	require.NoError(t, err)
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramUpdate{
		Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 5, Message: &sameAvatarMessage,
	}))
	require.Equal(t, 2, countTelegramAvatarRefreshes(), "Telegram avatar refreshes after interval")
	runTelegramAvatarRefresh()
	telegramIdentity = servermodels.ChannelIdentity{}
	require.NoError(t, db.NewSelect().Model(&telegramIdentity).
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		Scan(context.Background()))
	require.Equal(t, &telegramAvatarFilesInDatabase[1].ID, telegramIdentity.AvatarFileID, "unchanged Telegram avatar")
	require.Equal(t, 2, importedAvatarWriter.saved, "unchanged Telegram avatar writes")
	require.Equal(t, versionBeforeSameAvatar+1, loadConversationVersion(t, db, telegramConversation.ID), "unchanged Telegram avatar conversation version")
	noAvatarMessage := *telegramMessage.Message
	noAvatarMessage.MessageID = 44
	noAvatarMessage.Body = "删除头像后的 Telegram 消息"
	telegramAvatarAPI.photo = nil
	require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramUpdate{
		Secret: savedTelegram.Connection.WebhookSecret, UpdateID: 6, Message: &noAvatarMessage,
	}))
	runTelegramAvatarRefresh()
	telegramIdentity = servermodels.ChannelIdentity{}
	require.NoError(t, db.NewSelect().Model(&telegramIdentity).
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		Scan(context.Background()))
	require.Nil(t, telegramIdentity.AvatarFileID, "deleted Telegram avatar = %#v", telegramIdentity)
	// 追加消息与头像删除各推进一次会话版本。
	require.Equal(t, versionBeforeSameAvatar+3, loadConversationVersion(t, db, telegramConversation.ID), "deleted Telegram avatar conversation version")
	// 幂等重放带来新名称时更新渠道身份并推进会话版本，名称不变的重放不推进。
	renamedReplay := noAvatarMessage
	renamedReplay.DisplayName = "Telegram 重放名称"
	for _, step := range []struct {
		updateID int64
		want     int64
	}{{7, versionBeforeSameAvatar + 4}, {8, versionBeforeSameAvatar + 4}} {
		require.NoError(t, receiveTelegram.Execute(context.Background(), s.telegramChannel.ID, telegramUpdate{
			Secret: savedTelegram.Connection.WebhookSecret, UpdateID: step.updateID, Message: &renamedReplay,
		}))
		require.Equal(t, step.want, loadConversationVersion(t, db, telegramConversation.ID), "Telegram replay %d conversation version", step.updateID)
	}
	telegramIdentity = servermodels.ChannelIdentity{}
	require.NoError(t, db.NewSelect().Model(&telegramIdentity).
		Where("ci.channel_id = ?", s.telegramChannel.ID).
		Where("ci.external_id = ?", "998877").
		Scan(context.Background()))
	require.Equal(t, new("Telegram 重放名称"), telegramIdentity.DisplayName, "renamed Telegram identity")
	activeTelegramAvatarCount, err := db.NewSelect().Model((*servermodels.File)(nil)).
		Where("workspace_id = ?", loggedIn.Identity.Workspace.ID).
		Where("purpose = ?", domain.FilePurposeContactAvatar).
		Where("status = ?", domain.FileStatusActive).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), activeTelegramAvatarCount, "active Telegram avatar count")

	reusedBotChannel, err := createChannel.Execute(context.Background(), loggedIn.Identity, channelaction.CreateMessageChannelInput{
		Type:                  domain.ChannelTypeTelegram,
		Name:                  "Telegram 复用确认",
		DefaultLocale:         domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	webhookCountBeforeReuse := len(telegramAPI.webhooks())
	_, err = saveTelegram.Execute(context.Background(), loggedIn.Identity, reusedBotChannel.ID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect,
		BotToken: "123456:reused_token", WebhookBaseURL: "http://127.0.0.1:34115/app",
	})
	require.ErrorIs(t, err, telegramaction.ErrBotReuseConfirmationRequired, "unconfirmed Telegram bot reuse")
	unconfirmedReuse, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, reusedBotChannel.ID)
	require.NoError(t, err)
	require.Empty(t, unconfirmedReuse.Connection.BotToken)
	require.Len(t, telegramAPI.webhooks(), webhookCountBeforeReuse)
	confirmedReuse, err := saveTelegram.Execute(context.Background(), loggedIn.Identity, reusedBotChannel.ID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect,
		BotToken: "123456:reused_token", WebhookBaseURL: "http://127.0.0.1:34115/app", ConfirmBotReuse: true,
	})
	require.NoError(t, err)
	require.Equal(t, &telegramAPI.bot.ID, confirmedReuse.Connection.BotID)
	require.Len(t, telegramAPI.webhooks(), webhookCountBeforeReuse+1)
	require.Equal(t, confirmedReuse.Connection.WebhookURL, telegramAPI.webhooks()[webhookCountBeforeReuse].URL)
	originalAfterReuse, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	require.Equal(t, connectedTelegram.Connection.WebhookSecret, originalAfterReuse.Connection.WebhookSecret)
	require.Equal(t, new(string(domain.TelegramWebhookStatusNormal)), originalAfterReuse.Connection.WebhookStatus)

	updateTelegramStatus := telegramaction.NewUpdateChannelStatusAction(db, telegramRunner, telegramAPI, testEnqueuer)
	disabledTelegram, err := updateTelegramStatus.Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID, false)
	require.NoError(t, err)
	require.False(t, disabledTelegram.Enabled, "disabled Telegram channel remains enabled")
	require.ErrorIs(t, receiveTelegram.Preflight(context.Background(), s.telegramChannel.ID, savedTelegram.Connection.WebhookSecret), channelaction.ErrNotFound, "disabled webhook preflight")
	require.Empty(t, telegramAPI.deletedTokens())

	reenabledTelegram, err := updateTelegramStatus.Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID, true)
	require.NoError(t, err)
	require.True(t, reenabledTelegram.Enabled, "re-enabled Telegram channel remains disabled")
	reenabledDetail, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	require.Equal(t, new(string(domain.TelegramWebhookStatusWaiting)), reenabledDetail.Connection.WebhookStatus)
	require.NotEqual(t, savedTelegram.Connection.WebhookSecret, reenabledDetail.Connection.WebhookSecret)
	_, err = db.NewDelete().Model((*servermodels.TelegramChannelSetting)(nil)).Where("channel_id = ?", reusedBotChannel.ID).Exec(context.Background())
	require.NoError(t, err)
	_, err = db.NewDelete().Model((*servermodels.Channel)(nil)).Where("id = ?", reusedBotChannel.ID).Exec(context.Background())
	require.NoError(t, err)

	startSaves := make(chan struct{})
	saveErrors := make(chan error, 2)
	for _, token := range []string{"123456:concurrent_one", "123456:concurrent_two"} {
		token := token
		go func() {
			<-startSaves
			_, err := saveTelegram.Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID, telegramaction.ConnectionInput{ConnectionMode: domain.TelegramConnectionDirect,
				BotToken: token, WebhookBaseURL: "http://127.0.0.1:34115/app",
			})
			saveErrors <- err
		}()
	}
	close(startSaves)
	for range 2 {
		require.NoError(t, <-saveErrors)
	}
	concurrentDetail, err := telegramaction.NewGetChannelQuery(db).Execute(context.Background(), loggedIn.Identity, s.telegramChannel.ID)
	require.NoError(t, err)
	webhooks = telegramAPI.webhooks()
	require.GreaterOrEqual(t, len(webhooks), 4)
	require.Equal(t, concurrentDetail.Connection.WebhookSecret, webhooks[len(webhooks)-1].Secret)
	settingCount, err := db.NewSelect().
		Model((*servermodels.WebsiteChannelSetting)(nil)).
		Where("wcs.channel_id = ?", s.telegramChannel.ID).
		Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), settingCount, "telegram website setting count")

	updateChatInterface := channelaction.NewUpdateWebsiteChannelChatInterfaceAction(db)
	chatInterface, err := updateChatInterface.Execute(context.Background(), loggedIn.Identity, s.channel.ID, channelaction.WebsiteChannelChatInterfaceInput{
		Title:              "在线咨询",
		GreetingMessage:    "你好，有什么可以帮你？",
		ThemeColor:         "#16a34a",
		AttachmentsEnabled: false, EmojiEnabled: true, RatingEnabled: false,
	})
	require.NoError(t, err)
	require.Equal(t, "在线咨询", chatInterface.ChatTitle)
	require.Equal(t, "#16A34A", chatInterface.ThemeColor)
	require.False(t, chatInterface.AttachmentsEnabled)
	require.True(t, chatInterface.EmojiEnabled)
	require.False(t, chatInterface.RatingEnabled)
	require.False(t, chatInterface.MultipleConversationsEnabled)

	// 首页保存问候语、卡片顺序与链接，问候语为空时回到默认文案。
	updateHome := channelaction.NewUpdateWebsiteChannelHomeAction(db)
	blocks := []domain.WebsiteHomeBlock{{Type: domain.WebsiteHomeBlockLinks, Enabled: true}, {Type: domain.WebsiteHomeBlockStartConversation, Enabled: true}, {Type: domain.WebsiteHomeBlockRecentConversation, Enabled: false}}
	home, err := updateHome.Execute(context.Background(), loggedIn.Identity, s.channel.ID, channelaction.WebsiteChannelHomeInput{
		Enabled: false, Welcome: " 欢迎 ", Headline: "",
		Blocks: blocks,
		Links:  []domain.WebsiteHomeLink{{Title: " 使用文档 ", URL: " https://docs.example.com "}, {Title: "社区", URL: "https://community.example.com/join"}},
	})
	require.NoError(t, err)
	links := []domain.WebsiteHomeLink{{Title: "使用文档", URL: "https://docs.example.com"}, {Title: "社区", URL: "https://community.example.com/join"}}
	require.False(t, home.HomeEnabled)
	require.Equal(t, "欢迎", support.Deref(home.HomeWelcome))
	require.Nil(t, home.HomeHeadline)
	require.Equal(t, blocks, home.HomeBlocks)
	require.Equal(t, links, home.HomeLinks)
	publicChannel, err := channelaction.NewGetPublicWebsiteChannelQuery(db).Execute(context.Background(), s.channel.ID)
	require.NoError(t, err)
	require.False(t, publicChannel.HomeEnabled)
	require.False(t, publicChannel.AttachmentsEnabled)
	require.False(t, publicChannel.RatingEnabled)
	require.False(t, publicChannel.MultipleConversationsEnabled)
	require.False(t, publicChannel.HelpEnabled)
	require.Equal(t, "欢迎", publicChannel.HomeWelcome)
	require.Equal(t, blocks, publicChannel.HomeBlocks)
	require.Equal(t, links, publicChannel.HomeLinks)
	// 卡片须每种各一次。
	invalidHomes := []struct {
		field string
		input channelaction.WebsiteChannelHomeInput
	}{
		{"blocks", channelaction.WebsiteChannelHomeInput{Blocks: blocks[:2]}},
		{"blocks", channelaction.WebsiteChannelHomeInput{Blocks: []domain.WebsiteHomeBlock{blocks[0], blocks[0], blocks[1]}}},
	}
	for _, invalid := range invalidHomes {
		_, err := updateHome.Execute(context.Background(), loggedIn.Identity, s.channel.ID, invalid.input)
		fieldError, ok := errors.AsType[*common.FieldError](err)
		require.True(t, ok, "home %+v err=%v", invalid, err)
		require.NotEmpty(t, fieldError.Fields[invalid.field], "home %+v err=%v", invalid, err)
	}

	s.channel, err = updateChannel.ExecuteBasics(context.Background(), loggedIn.Identity, s.channel.ID, channelaction.MessageChannelBasicsInput{
		Name:          "帮助中心",
		DefaultLocale: domain.CustomerLocaleEnglishUnitedStates,
	})
	require.NoError(t, err)
	require.Equal(t, "帮助中心", s.channel.Name)
	require.Nil(t, s.channel.Description)
	require.Equal(t, string(domain.LocaleEnglishUnitedStates), s.channel.DefaultLocale)
	s.telegramChannel, err = updateChannel.ExecuteBasics(context.Background(), loggedIn.Identity, s.telegramChannel.ID, channelaction.MessageChannelBasicsInput{
		Name:          "Telegram 支持",
		DefaultLocale: domain.CustomerLocaleEnglishUnitedStates,
	})
	require.NoError(t, err)
	require.Equal(t, string(domain.ChannelTypeTelegram), s.telegramChannel.Type)
	require.Equal(t, "Telegram 支持", s.telegramChannel.Name)
	require.Equal(t, string(domain.LocaleEnglishUnitedStates), s.telegramChannel.DefaultLocale)

	updateChannelStatus := newTestChannelStatusAction(db)
	s.channel, err = updateChannelStatus.Execute(context.Background(), loggedIn.Identity, s.channel.ID, false)
	require.NoError(t, err)
	require.False(t, s.channel.Enabled)
	listChannels := channelaction.NewListMessageChannelsQuery(db)
	channels, err := listChannels.Execute(context.Background(), loggedIn.Identity)
	require.NoError(t, err)
	require.Len(t, channels, 2)
	require.Equal(t, s.channel.ID, channels[0].ID)
	require.False(t, channels[0].Enabled)
	telegramListed := false
	for _, listedChannel := range channels {
		if listedChannel.ID == s.telegramChannel.ID && listedChannel.Type == string(domain.ChannelTypeTelegram) {
			telegramListed = true
			break
		}
	}
	require.True(t, telegramListed, "telegram channel missing from list: %#v", channels)

	s.channel, err = updateChannelStatus.Execute(context.Background(), loggedIn.Identity, s.channel.ID, true)
	require.NoError(t, err)
	require.True(t, s.channel.Enabled)
}
