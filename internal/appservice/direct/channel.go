//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// channelOps 持有消息渠道的 Action 和 Query。
type channelOps struct {
	listMessageChannels               *channelaction.ListMessageChannelsQuery
	getWebsiteChannel                 *channelaction.GetWebsiteChannelQuery
	getTelegramChannel                *channelaction.GetTelegramChannelQuery
	getMessageChannel                 *channelaction.GetMessageChannelQuery
	createMessageChannel              *channelaction.CreateMessageChannelAction
	updateMessageChannel              *channelaction.UpdateMessageChannelAction
	updateWebsiteChannelChatInterface *channelaction.UpdateWebsiteChannelChatInterfaceAction
	updateWebsiteChannelAccess        *channelaction.UpdateWebsiteChannelAccessAction
	updateWebsiteChannelHelpCenter    *channelaction.UpdateWebsiteChannelHelpCenterAction
	updateWebsiteChannelHome          *channelaction.UpdateWebsiteChannelHomeAction
	testTelegramConnection            *channelaction.TestTelegramConnectionAction
	saveTelegramConnection            *channelaction.SaveTelegramConnectionAction
	updateMessageChannelStatus        *channelaction.UpdateMessageChannelStatusAction
	listChannelOptions                *channelaction.ListChannelOptionsQuery
}

// newChannelOps 创建消息渠道的业务实现依赖。
func newChannelOps(db *bun.DB, connectionRunner *connectiontest.Runner, telegramAPI *telegram.Client) channelOps {
	return channelOps{
		listMessageChannels:               channelaction.NewListMessageChannelsQuery(db),
		getWebsiteChannel:                 channelaction.NewGetWebsiteChannelQuery(db),
		getTelegramChannel:                channelaction.NewGetTelegramChannelQuery(db),
		getMessageChannel:                 channelaction.NewGetMessageChannelQuery(db),
		createMessageChannel:              channelaction.NewCreateMessageChannelAction(db),
		updateMessageChannel:              channelaction.NewUpdateMessageChannelAction(db),
		updateWebsiteChannelChatInterface: channelaction.NewUpdateWebsiteChannelChatInterfaceAction(db),
		updateWebsiteChannelAccess:        channelaction.NewUpdateWebsiteChannelAccessAction(db),
		updateWebsiteChannelHelpCenter:    channelaction.NewUpdateWebsiteChannelHelpCenterAction(db),
		updateWebsiteChannelHome:          channelaction.NewUpdateWebsiteChannelHomeAction(db),
		testTelegramConnection:            channelaction.NewTestTelegramConnectionAction(db, connectionRunner, telegramAPI),
		saveTelegramConnection:            channelaction.NewSaveTelegramConnectionAction(db, connectionRunner, telegramAPI),
		updateMessageChannelStatus:        channelaction.NewUpdateMessageChannelStatusAction(db, channelaction.NewUpdateTelegramChannelStatusAction(db, connectionRunner, telegramAPI)),
		listChannelOptions:                channelaction.NewListChannelOptionsQuery(db),
	}
}

const telegramBotReuseConfirmationReason = "telegram_bot_reuse_confirmation_required"

// ListMessageChannels 返回当前企业的消息渠道。
func (o *directOperations) ListMessageChannels(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.MessageChannelList, error) {
	channels, err := o.listMessageChannels.Execute(ctx, identity)
	if err != nil {
		return appservice.MessageChannelList{}, o.channelError(ctx, meta, err, i18n.ErrorChannelListFailed, identity.Organization.ID, "")
	}
	result := make([]appservice.MessageChannelSummary, 0, len(channels))
	for index := range channels {
		result = append(result, messageChannelFromRecord(&channels[index]))
	}
	return appservice.MessageChannelList{Channels: result}, nil
}

// GetWebsiteChannel 返回网站渠道详情。
func (o *directOperations) GetWebsiteChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WebsiteChannel, error) {
	detail, err := o.getWebsiteChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WebsiteChannel{}, o.channelError(ctx, meta, err, i18n.ErrorChannelReadFailed, identity.Organization.ID, channelID)
	}
	return appservice.WebsiteChannel{
		MessageChannelSummary: messageChannelFromRecord(&detail.MessageChannelRecord),
		ChatInterface:         websiteChannelSettingFromRecord(&detail.ChatInterface),
		Access:                websiteChannelAccessFromRecord(&detail.ChatInterface),
		Home:                  websiteChannelHomeFromRecord(&detail.ChatInterface),
		HelpCenter:            appservice.WebsiteChannelHelpCenter{Enabled: detail.ChatInterface.HelpEnabled, KnowledgeBaseIDs: detail.HelpCenterKnowledgeBaseIDs},
	}, nil
}

// GetTelegramChannel 返回 Telegram 渠道详情。
func (o *directOperations) GetTelegramChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.TelegramChannel, error) {
	detail, err := o.getTelegramChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.TelegramChannel{}, o.channelError(ctx, meta, err, i18n.ErrorChannelReadFailed, identity.Organization.ID, channelID)
	}
	return telegramChannelFromRecord(detail), nil
}

// TestTelegramChannelConnection 测试 Telegram 草稿 Token。
func (o *directOperations) TestTelegramChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.TelegramChannelConnectionTestInput) error {
	err := o.testTelegramConnection.Execute(ctx, identity, channelID, channelaction.TelegramChannelConnectionTestInput{BotToken: input.BotToken})
	if err == nil {
		return nil
	}
	return o.telegramConnectionError(ctx, meta, err, i18n.ErrorTelegramConnectionTestFailed, identity.Organization.ID, channelID)
}

// SaveTelegramChannelConnection 保存 Telegram 机器人和 Webhook 设置。
func (o *directOperations) SaveTelegramChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.TelegramChannelConnectionInput) (appservice.TelegramChannel, error) {
	detail, err := o.saveTelegramConnection.Execute(ctx, identity, channelID, channelaction.TelegramChannelConnectionInput{
		BotToken: input.BotToken, WebhookBaseURL: input.WebhookBaseURL, ConfirmBotReuse: input.ConfirmBotReuse,
	})
	if err != nil {
		return appservice.TelegramChannel{}, o.telegramConnectionError(ctx, meta, err, i18n.ErrorTelegramConnectionSaveFailed, identity.Organization.ID, channelID)
	}
	slog.Info("Telegram 渠道连接已保存", "organization_id", identity.Organization.ID, "channel_id", channelID)
	return telegramChannelFromRecord(detail), nil
}

// GetMessageChannel 返回消息渠道基础信息。
func (o *directOperations) GetMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.MessageChannelSummary, error) {
	channel, err := o.getMessageChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.MessageChannelSummary{}, o.channelError(ctx, meta, err, i18n.ErrorChannelReadFailed, identity.Organization.ID, channelID)
	}
	return messageChannelFromRecord(channel), nil
}

// CreateMessageChannel 创建消息渠道。
func (o *directOperations) CreateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreateMessageChannelInput) (appservice.MessageChannelSummary, error) {
	// 转换消息渠道创建输入。
	actionInput := channelaction.CreateMessageChannelInput{
		MessageChannelInput: channelInput(input.MessageChannelInput),
		Type:                domain.ChannelType(input.Type),
	}
	channel, err := o.createMessageChannel.Execute(ctx, identity, actionInput)
	if err != nil {
		return appservice.MessageChannelSummary{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelCreateFailed, identity.Organization.ID, "")
	}
	slog.Info("消息渠道创建成功", "organization_id", identity.Organization.ID, "channel_id", channel.ID, "channel_type", channel.Type)
	return messageChannelFromRecord(channel), nil
}

// UpdateMessageChannel 修改消息渠道基础信息。
func (o *directOperations) UpdateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.MessageChannelBasicsInput) (appservice.MessageChannelSummary, error) {
	channel, err := o.updateMessageChannel.ExecuteBasics(ctx, identity, channelID, channelaction.MessageChannelBasicsInput{
		Name:          input.Name,
		Description:   input.Description,
		DefaultLocale: domain.CustomerLocale(input.DefaultLocale),
	})
	if err != nil {
		return appservice.MessageChannelSummary{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelUpdateFailed, identity.Organization.ID, channelID)
	}
	slog.Info("消息渠道更新成功", "organization_id", identity.Organization.ID, "channel_id", channel.ID, "channel_type", channel.Type)
	return messageChannelFromRecord(channel), nil
}

// UpdateMessageChannelReception 修改消息渠道接待设置。
func (o *directOperations) UpdateMessageChannelReception(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.MessageChannelReceptionInput) (appservice.MessageChannelSummary, error) {
	channel, err := o.updateMessageChannel.ExecuteReception(ctx, identity, channelID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelRoutingTargetInput(input.NewConversationTarget),
		FallbackTarget:        channelRoutingTargetInput(input.FallbackTarget),
	})
	if err != nil {
		return appservice.MessageChannelSummary{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelUpdateFailed, identity.Organization.ID, channelID)
	}
	return messageChannelFromRecord(channel), nil
}

// UpdateWebsiteChannelChatInterface 修改网站渠道聊天窗口外观与对话功能。
func (o *directOperations) UpdateWebsiteChannelChatInterface(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelChatInterfaceInput) (appservice.WebsiteChannelChatInterface, error) {
	setting, err := o.updateWebsiteChannelChatInterface.Execute(ctx, identity, channelID, channelaction.WebsiteChannelChatInterfaceInput{
		Title: input.Title, GreetingMessage: input.GreetingMessage, ThemeColor: input.ThemeColor,
		AttachmentsEnabled: input.AttachmentsEnabled, EmojiEnabled: input.EmojiEnabled, RatingEnabled: input.RatingEnabled,
		MultipleConversationsEnabled: input.MultipleConversationsEnabled,
	})
	if err != nil {
		return appservice.WebsiteChannelChatInterface{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelChatInterfaceUpdateFailed, identity.Organization.ID, channelID)
	}
	slog.Info("网站渠道聊天界面更新成功", "organization_id", identity.Organization.ID, "channel_id", channelID)
	return websiteChannelSettingFromRecord(setting), nil
}

// UpdateWebsiteChannelHome 修改网站渠道 Messenger 首页。
func (o *directOperations) UpdateWebsiteChannelHome(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelHomeInput) (appservice.WebsiteChannelHome, error) {
	blocks := make([]domain.WebsiteHomeBlock, 0, len(input.Blocks))
	for _, block := range input.Blocks {
		blocks = append(blocks, domain.WebsiteHomeBlock{Type: domain.WebsiteHomeBlockType(block.Type), Enabled: block.Enabled})
	}
	links := make([]domain.WebsiteHomeLink, 0, len(input.Links))
	for _, link := range input.Links {
		links = append(links, domain.WebsiteHomeLink{Title: link.Title, URL: link.URL})
	}
	setting, err := o.updateWebsiteChannelHome.Execute(ctx, identity, channelID, channelaction.WebsiteChannelHomeInput{
		Enabled: input.Enabled, Welcome: input.Welcome, Headline: input.Headline, Blocks: blocks, Links: links,
	})
	if err != nil {
		return appservice.WebsiteChannelHome{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelHomeUpdateFailed, identity.Organization.ID, channelID)
	}
	return websiteChannelHomeFromRecord(setting), nil
}

// UpdateWebsiteChannelHelpCenter 修改网站渠道帮助页签开关与发布的知识库。
func (o *directOperations) UpdateWebsiteChannelHelpCenter(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelHelpCenterInput) (appservice.WebsiteChannelHelpCenter, error) {
	record, err := o.updateWebsiteChannelHelpCenter.Execute(ctx, identity, channelID, channelaction.WebsiteChannelHelpCenterInput{Enabled: input.Enabled, KnowledgeBaseIDs: input.KnowledgeBaseIDs})
	if err != nil {
		return appservice.WebsiteChannelHelpCenter{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelHelpCenterUpdateFailed, identity.Organization.ID, channelID)
	}
	return appservice.WebsiteChannelHelpCenter{Enabled: record.Enabled, KnowledgeBaseIDs: record.KnowledgeBaseIDs}, nil
}

// UpdateWebsiteChannelAccess 修改网站渠道允许使用的网站。
func (o *directOperations) UpdateWebsiteChannelAccess(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelAccessInput) (appservice.WebsiteChannelAccess, error) {
	setting, err := o.updateWebsiteChannelAccess.Execute(ctx, identity, channelID, channelaction.WebsiteChannelAccessInput{
		AllowedHosts: input.AllowedHosts,
	})
	if err != nil {
		return appservice.WebsiteChannelAccess{}, o.channelMutationError(ctx, meta, err, i18n.ErrorChannelAccessUpdateFailed, identity.Organization.ID, channelID)
	}
	slog.Info("网站渠道允许使用的网站更新成功", "organization_id", identity.Organization.ID, "channel_id", channelID)
	return websiteChannelAccessFromRecord(setting), nil
}

// DeactivateMessageChannel 停用消息渠道。
func (o *directOperations) DeactivateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.MessageChannelSummary, error) {
	return o.setMessageChannelEnabled(ctx, meta, identity, channelID, false)
}

// ActivateMessageChannel 启用消息渠道。
func (o *directOperations) ActivateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.MessageChannelSummary, error) {
	return o.setMessageChannelEnabled(ctx, meta, identity, channelID, true)
}

// setMessageChannelEnabled 修改消息渠道的启用状态。
func (o *directOperations) setMessageChannelEnabled(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, enabled bool) (appservice.MessageChannelSummary, error) {
	channel, err := o.updateMessageChannelStatus.Execute(ctx, identity, channelID, enabled)
	if err != nil {
		return appservice.MessageChannelSummary{}, o.channelError(ctx, meta, err, i18n.ErrorChannelUpdateFailed, identity.Organization.ID, channelID)
	}
	slog.Info("消息渠道状态已更新", "organization_id", identity.Organization.ID, "channel_id", channel.ID, "channel_type", channel.Type, "enabled", enabled)
	return messageChannelFromRecord(channel), nil
}

// ListChannelOptions 返回当前企业的渠道选择项。
func (o *directOperations) ListChannelOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ChannelOptionList, error) {
	channels, err := o.listChannelOptions.Execute(ctx, identity)
	if err != nil {
		return appservice.ChannelOptionList{}, o.channelError(ctx, meta, err, i18n.ErrorChannelSummaryListFailed, identity.Organization.ID, "")
	}
	result := make([]appservice.ChannelOption, 0, len(channels))
	for _, channel := range channels {
		result = append(result, appservice.ChannelOption{ID: channel.ID, Type: appservice.ChannelType(channel.Type), Name: channel.Name})
	}
	return appservice.ChannelOptionList{Channels: result}, nil
}

// channelMutationError 转换渠道写入校验和操作错误。
func (o *directOperations) channelMutationError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, channelID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, channelFieldKeys(validationError.Fields))
	}
	return o.channelError(ctx, meta, err, failureKey, organizationID, channelID)
}

// channelError 转换渠道读取和状态修改错误。
func (o *directOperations) channelError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, channelID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, channelaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorChannelNotFound)
	}
	attributes := []any{"organization_id", organizationID, "failure", failureKey, "error", err}
	if channelID != "" {
		attributes = append(attributes, "channel_id", channelID)
	}
	slog.Warn("消息渠道操作失败", attributes...)
	return appservice.FailedError(meta, failureKey)
}

// messageChannelFromRecord 转换消息渠道传输结构。
func messageChannelFromRecord(channel *channelaction.MessageChannelRecord) appservice.MessageChannelSummary {
	return appservice.MessageChannelSummary{
		ID: channel.ID, OrganizationID: channel.OrganizationID, CreatedByUserID: channel.CreatedByUserID,
		Type: appservice.ChannelType(channel.Type), Name: channel.Name, Description: channel.Description, DefaultLocale: appservice.CustomerLocale(channel.DefaultLocale), Enabled: channel.Enabled,
		NewConversationTarget: channelRoutingTargetFromRecord(channel.InitialRoutingTargetType, channel.InitialRoutingTargetID),
		FallbackTarget:        channelRoutingTargetFromRecord(channel.FallbackRoutingTargetType, channel.FallbackRoutingTargetID),
		CreatedAt:             channel.CreatedAt, UpdatedAt: channel.UpdatedAt,
	}
}

// websiteChannelSettingFromRecord 转换网站渠道聊天窗口外观与对话功能设置。
func websiteChannelSettingFromRecord(setting *channelaction.WebsiteChannelSettingRecord) appservice.WebsiteChannelChatInterface {
	return appservice.WebsiteChannelChatInterface{
		Title: setting.ChatTitle, GreetingMessage: setting.GreetingMessage, ThemeColor: setting.ThemeColor,
		AttachmentsEnabled: setting.AttachmentsEnabled, EmojiEnabled: setting.EmojiEnabled, RatingEnabled: setting.RatingEnabled,
		MultipleConversationsEnabled: setting.MultipleConversationsEnabled,
	}
}

// websiteChannelHomeFromRecord 转换网站渠道 Messenger 首页设置。
func websiteChannelHomeFromRecord(setting *channelaction.WebsiteChannelSettingRecord) appservice.WebsiteChannelHome {
	home := appservice.WebsiteChannelHome{
		Enabled: setting.HomeEnabled,
		Welcome: common.StringValue(setting.HomeWelcome), Headline: common.StringValue(setting.HomeHeadline),
		Blocks: make([]appservice.WebsiteChannelHomeBlock, 0, len(setting.HomeBlocks)), Links: make([]appservice.WebsiteChannelHomeLink, 0, len(setting.HomeLinks)),
	}
	for _, block := range setting.HomeBlocks {
		home.Blocks = append(home.Blocks, appservice.WebsiteChannelHomeBlock{Type: appservice.WebsiteHomeBlockType(block.Type), Enabled: block.Enabled})
	}
	for _, link := range setting.HomeLinks {
		home.Links = append(home.Links, appservice.WebsiteChannelHomeLink{Title: link.Title, URL: link.URL})
	}
	return home
}

// websiteChannelAccessFromRecord 转换网站渠道允许使用的网站。
func websiteChannelAccessFromRecord(setting *channelaction.WebsiteChannelSettingRecord) appservice.WebsiteChannelAccess {
	return appservice.WebsiteChannelAccess{AllowedHosts: setting.AllowedEmbedHosts}
}

// telegramChannelFromRecord 转换 Telegram 渠道详情。
func telegramChannelFromRecord(detail *channelaction.TelegramChannelDetail) appservice.TelegramChannel {
	connection := detail.Connection
	var botID *string
	if connection.BotID != nil {
		value := strconv.FormatInt(*connection.BotID, 10)
		botID = &value
	}
	var status *appservice.TelegramWebhookStatus
	if connection.WebhookStatus != nil {
		value := appservice.TelegramWebhookStatus(*connection.WebhookStatus)
		status = &value
	}
	return appservice.TelegramChannel{
		MessageChannelSummary: messageChannelFromRecord(&detail.MessageChannelRecord),
		Connection: appservice.TelegramChannelConnection{
			BotToken: connection.BotToken, BotID: botID, BotUsername: connection.BotUsername,
			BotDisplayName: connection.BotDisplayName, WebhookURL: connection.WebhookURL,
			WebhookSecret: connection.WebhookSecret, WebhookStatus: status,
		},
	}
}

// telegramConnectionError 转换 Telegram 连接校验和外部访问错误。
func (o *directOperations) telegramConnectionError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, channelID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, channelFieldKeys(validationError.Fields))
	}
	if errors.Is(err, channelaction.ErrNotFound) || errors.Is(err, identityaction.ErrInvalid) {
		return o.channelError(ctx, meta, err, failureKey, organizationID, channelID)
	}
	if errors.Is(err, channelaction.ErrTelegramBotReuseConfirmationRequired) {
		return appservice.ConflictError(meta, i18n.FieldTelegramBotInUse, telegramBotReuseConfirmationReason)
	}
	_, kind, classified := connectiontest.Details(err)
	if !classified {
		return o.channelError(ctx, meta, err, failureKey, organizationID, channelID)
	}
	switch kind {
	case connectiontest.FailureInvalidConfig, connectiontest.FailureUnauthorized, connectiontest.FailureForbidden, connectiontest.FailureNotFound:
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{"botToken": i18n.FieldTelegramBotTokenInvalid})
	default:
		return appservice.UnavailableError(meta, failureKey, nil)
	}
}

// channelInput 转换消息渠道修改输入。
func channelInput(input appservice.MessageChannelInput) channelaction.MessageChannelInput {
	return channelaction.MessageChannelInput{
		Name: input.Name, Description: input.Description, DefaultLocale: domain.CustomerLocale(input.DefaultLocale),
		NewConversationTarget: channelRoutingTargetInput(input.NewConversationTarget),
		FallbackTarget:        channelRoutingTargetInput(input.FallbackTarget),
	}
}

// channelRoutingTargetInput 转换渠道会话流转目标输入。
func channelRoutingTargetInput(target appservice.ChannelRoutingTarget) channelaction.RoutingTarget {
	return channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetType(target.Type), ID: target.ID}
}

// channelRoutingTargetFromRecord 转换渠道记录中的会话流转目标。
func channelRoutingTargetFromRecord(targetType string, targetID *string) appservice.ChannelRoutingTarget {
	id := ""
	if targetID != nil {
		id = *targetID
	}
	return appservice.ChannelRoutingTarget{Type: appservice.ChannelRoutingTargetType(targetType), ID: id}
}

// channelFieldKeys 把渠道校验错误码映射为本地化文案键。
func channelFieldKeys(fields map[string]common.FieldCode) map[string]i18n.Key {
	keys := map[common.FieldCode]i18n.Key{
		channelaction.ValidationTypeInvalid:            i18n.FieldChannelTypeInvalid,
		channelaction.ValidationNameRequired:           i18n.FieldChannelNameRequired,
		channelaction.ValidationNameTooLong:            i18n.FieldChannelNameTooLong,
		channelaction.ValidationDescriptionTooLong:     i18n.FieldChannelDescriptionTooLong,
		channelaction.ValidationDefaultLocaleInvalid:   i18n.FieldChannelDefaultLocaleInvalid,
		channelaction.ValidationRoutingTargetInvalid:   i18n.FieldChannelRoutingTargetInvalid,
		channelaction.ValidationChatTitleRequired:      i18n.FieldChannelChatTitleRequired,
		channelaction.ValidationChatTitleTooLong:       i18n.FieldChannelChatTitleTooLong,
		channelaction.ValidationGreetingTooLong:        i18n.FieldChannelGreetingTooLong,
		channelaction.ValidationThemeColorInvalid:      i18n.FieldChannelThemeColorInvalid,
		channelaction.ValidationHomeGreetingTooLong:    i18n.FieldChannelHomeGreetingTooLong,
		channelaction.ValidationHomeBlocksInvalid:      i18n.FieldChannelHomeBlocksInvalid,
		channelaction.ValidationHomeLinkTitleRequired:  i18n.FieldChannelHomeLinkTitleRequired,
		channelaction.ValidationHomeLinkTitleTooLong:   i18n.FieldChannelHomeLinkTitleTooLong,
		channelaction.ValidationHomeLinkURLInvalid:     i18n.FieldChannelHomeLinkURLInvalid,
		channelaction.ValidationAllowedHostsTooMany:    i18n.FieldChannelAllowedHostsTooMany,
		channelaction.ValidationAllowedHostInvalid:     i18n.FieldChannelAllowedHostInvalid,
		channelaction.ValidationKnowledgeBaseInvalid:   i18n.FieldChannelKnowledgeBaseInvalid,
		channelaction.ValidationTelegramTokenRequired:  i18n.FieldTelegramBotTokenRequired,
		channelaction.ValidationTelegramTokenTooLong:   i18n.FieldTelegramBotTokenTooLong,
		channelaction.ValidationTelegramTokenInvalid:   i18n.FieldTelegramBotTokenInvalid,
		channelaction.ValidationTelegramBaseURLInvalid: i18n.FieldTelegramWebhookBaseURLInvalid,
	}
	return translateValidationFields(fields, keys)
}
