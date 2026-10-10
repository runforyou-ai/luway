//go:build server

package direct

import (
	"context"
	"log/slog"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	channelbindingaction "github.com/runforyou-ai/luway/internal/actions/channelbinding"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// channelOps 持有消息渠道的 Action 和 Query。
type channelOps struct {
	listMessageChannels               *channelaction.ListMessageChannelsQuery
	getWebsiteChannel                 *channelaction.GetWebsiteChannelQuery
	getMessageChannel                 *channelaction.GetMessageChannelQuery
	createMessageChannel              *channelaction.CreateMessageChannelAction
	updateMessageChannel              *channelaction.UpdateMessageChannelAction
	updateWebsiteChannelChatInterface *channelaction.UpdateWebsiteChannelChatInterfaceAction
	updateWebsiteChannelAccess        *channelaction.UpdateWebsiteChannelAccessAction
	updateWebsiteChannelHelpCenter    *channelaction.UpdateWebsiteChannelHelpCenterAction
	updateWebsiteChannelHome          *channelaction.UpdateWebsiteChannelHomeAction
	updateMessageChannelStatus        *channelaction.UpdateMessageChannelStatusAction
	listChannelOptions                *channelaction.ListChannelOptionsQuery
	getWeComBotChannel                *channelaction.GetWeComBotChannelQuery
	saveWeComBotConnection            *channelaction.SaveWeComBotConnectionAction
	listChannelAccounts               *channelbindingaction.ListAccountsQuery
	manageChannelAccounts             *channelbindingaction.ManageAction
	previewChannelBinding             *channelbindingaction.PreviewQuery
	confirmChannelBinding             *channelbindingaction.ConfirmAction
	// files 解析文件地址。
	files *fileOps
}

// newChannelOps 创建消息渠道的业务实现依赖，statusUpdaters 按渠道类型登记需要维护平台侧连接的启停操作。
func newChannelOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, statusUpdaters map[domain.ChannelType]channelaction.StatusUpdater, files *fileOps) *channelOps {
	return &channelOps{
		listMessageChannels:               channelaction.NewListMessageChannelsQuery(db),
		getWebsiteChannel:                 channelaction.NewGetWebsiteChannelQuery(db),
		getMessageChannel:                 channelaction.NewGetMessageChannelQuery(db),
		createMessageChannel:              channelaction.NewCreateMessageChannelAction(db),
		updateMessageChannel:              channelaction.NewUpdateMessageChannelAction(db),
		updateWebsiteChannelChatInterface: channelaction.NewUpdateWebsiteChannelChatInterfaceAction(db),
		updateWebsiteChannelAccess:        channelaction.NewUpdateWebsiteChannelAccessAction(db),
		updateWebsiteChannelHelpCenter:    channelaction.NewUpdateWebsiteChannelHelpCenterAction(db),
		updateWebsiteChannelHome:          channelaction.NewUpdateWebsiteChannelHomeAction(db),
		updateMessageChannelStatus:        channelaction.NewUpdateMessageChannelStatusAction(db, taskEnqueuer, statusUpdaters),
		listChannelOptions:                channelaction.NewListChannelOptionsQuery(db),
		getWeComBotChannel:                channelaction.NewGetWeComBotChannelQuery(db),
		saveWeComBotConnection:            channelaction.NewSaveWeComBotConnectionAction(db),
		listChannelAccounts:               channelbindingaction.NewListAccountsQuery(db),
		manageChannelAccounts:             channelbindingaction.NewManageAction(db),
		previewChannelBinding:             channelbindingaction.NewPreviewQuery(db),
		confirmChannelBinding:             channelbindingaction.NewConfirmAction(db),
		files:                             files,
	}
}

// ListMessageChannelTypes 返回当前支持的消息渠道类型及其能力。
func (o *channelOps) ListMessageChannelTypes(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.MessageChannelTypeList, error) {
	types := arr.Map(domain.MessageChannelTypes(), func(channelType domain.ChannelType) appservice.MessageChannelTypeInfo {
		return appservice.MessageChannelTypeInfo{Type: channelType, Capabilities: appservice.NewChannelCapabilities(channelType)}
	})
	return appservice.MessageChannelTypeList{Types: types}, nil
}

// ListMessageChannels 返回当前企业的消息渠道。
func (o *channelOps) ListMessageChannels(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.MessageChannelList, error) {
	channels, err := o.listMessageChannels.Execute(ctx, identity)
	if err != nil {
		return appservice.MessageChannelList{}, channelError(meta, err, i18n.ErrorChannelListFailed)
	}
	return appservice.MessageChannelList{Channels: arr.Map(channels, func(channel channelaction.MessageChannelRecord) appservice.MessageChannelSummary {
		return messageChannelFromRecord(&channel)
	})}, nil
}

// GetWebsiteChannel 返回网站渠道详情。
func (o *channelOps) GetWebsiteChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.WebsiteChannel, error) {
	detail, err := o.getWebsiteChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.WebsiteChannel{}, channelError(meta, err, i18n.ErrorChannelReadFailed)
	}
	return appservice.WebsiteChannel{
		MessageChannelSummary: messageChannelFromRecord(&detail.MessageChannelRecord),
		ChatInterface:         websiteChannelSettingFromRecord(&detail.ChatInterface),
		Access:                websiteChannelAccessFromRecord(&detail.ChatInterface),
		Home:                  websiteChannelHomeFromRecord(&detail.ChatInterface),
		HelpCenter:            appservice.WebsiteChannelHelpCenter{Enabled: detail.ChatInterface.HelpEnabled, KnowledgeBaseIDs: detail.HelpCenterKnowledgeBaseIDs},
	}, nil
}

// GetMessageChannel 返回消息渠道基础信息。
func (o *channelOps) GetMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.MessageChannelSummary, error) {
	channel, err := o.getMessageChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.MessageChannelSummary{}, channelError(meta, err, i18n.ErrorChannelReadFailed)
	}
	return messageChannelFromRecord(channel), nil
}

// CreateMessageChannel 创建消息渠道。
func (o *channelOps) CreateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.CreateMessageChannelInput) (appservice.MessageChannelSummary, error) {
	// 转换消息渠道创建输入。
	actionInput := channelaction.CreateMessageChannelInput{
		MessageChannelInput: channelInput(input.MessageChannelInput),
		Type:                input.Type,
	}
	channel, err := o.createMessageChannel.Execute(ctx, identity, actionInput)
	if err != nil {
		return appservice.MessageChannelSummary{}, channelMutationError(meta, err, i18n.ErrorChannelCreateFailed)
	}
	slog.InfoContext(ctx, "消息渠道创建成功", "channel_id", channel.ID, "channel_type", channel.Type)
	return messageChannelFromRecord(channel), nil
}

// UpdateMessageChannel 修改消息渠道基础信息。
func (o *channelOps) UpdateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.MessageChannelBasicsInput) (appservice.MessageChannelSummary, error) {
	channel, err := o.updateMessageChannel.ExecuteBasics(ctx, identity, channelID, channelaction.MessageChannelBasicsInput{
		Name:          input.Name,
		Description:   input.Description,
		DefaultLocale: input.DefaultLocale,
	})
	if err != nil {
		return appservice.MessageChannelSummary{}, channelMutationError(meta, err, i18n.ErrorChannelUpdateFailed)
	}
	slog.InfoContext(ctx, "消息渠道更新成功", "channel_id", channel.ID, "channel_type", channel.Type)
	return messageChannelFromRecord(channel), nil
}

// UpdateMessageChannelReception 修改消息渠道接待设置。
func (o *channelOps) UpdateMessageChannelReception(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.MessageChannelReceptionInput) (appservice.MessageChannelSummary, error) {
	channel, err := o.updateMessageChannel.ExecuteReception(ctx, identity, channelID, channelaction.MessageChannelReceptionInput{
		NewConversationTarget: channelRoutingTargetInput(input.NewConversationTarget),
		FallbackTarget:        channelRoutingTargetInput(input.FallbackTarget),
	})
	if err != nil {
		return appservice.MessageChannelSummary{}, channelMutationError(meta, err, i18n.ErrorChannelUpdateFailed)
	}
	return messageChannelFromRecord(channel), nil
}

// UpdateWebsiteChannelChatInterface 修改网站渠道聊天窗口外观与对话功能。
func (o *channelOps) UpdateWebsiteChannelChatInterface(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelChatInterfaceInput) (appservice.WebsiteChannelChatInterface, error) {
	setting, err := o.updateWebsiteChannelChatInterface.Execute(ctx, identity, channelID, channelaction.WebsiteChannelChatInterfaceInput{
		Title: input.Title, GreetingMessage: input.GreetingMessage, ThemeColor: input.ThemeColor,
		AttachmentsEnabled: input.AttachmentsEnabled, EmojiEnabled: input.EmojiEnabled, RatingEnabled: input.RatingEnabled,
		MultipleConversationsEnabled: input.MultipleConversationsEnabled,
	})
	if err != nil {
		return appservice.WebsiteChannelChatInterface{}, channelMutationError(meta, err, i18n.ErrorChannelChatInterfaceUpdateFailed)
	}
	slog.InfoContext(ctx, "网站渠道聊天界面更新成功", "channel_id", channelID)
	return websiteChannelSettingFromRecord(setting), nil
}

// UpdateWebsiteChannelHome 修改网站渠道 Messenger 首页。
func (o *channelOps) UpdateWebsiteChannelHome(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelHomeInput) (appservice.WebsiteChannelHome, error) {
	blocks := arr.OrEmpty(arr.Map(input.Blocks, func(block appservice.WebsiteChannelHomeBlock) domain.WebsiteHomeBlock {
		return domain.WebsiteHomeBlock{Type: block.Type, Enabled: block.Enabled}
	}))
	links := arr.OrEmpty(arr.Map(input.Links, func(link appservice.WebsiteChannelHomeLink) domain.WebsiteHomeLink {
		return domain.WebsiteHomeLink{Title: link.Title, URL: link.URL}
	}))
	setting, err := o.updateWebsiteChannelHome.Execute(ctx, identity, channelID, channelaction.WebsiteChannelHomeInput{
		Enabled: input.Enabled, Welcome: input.Welcome, Headline: input.Headline, Blocks: blocks, Links: links,
	})
	if err != nil {
		return appservice.WebsiteChannelHome{}, channelMutationError(meta, err, i18n.ErrorChannelHomeUpdateFailed)
	}
	return websiteChannelHomeFromRecord(setting), nil
}

// UpdateWebsiteChannelHelpCenter 修改网站渠道帮助页签开关与发布的知识库。
func (o *channelOps) UpdateWebsiteChannelHelpCenter(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelHelpCenterInput) (appservice.WebsiteChannelHelpCenter, error) {
	record, err := o.updateWebsiteChannelHelpCenter.Execute(ctx, identity, channelID, channelaction.WebsiteChannelHelpCenterInput{Enabled: input.Enabled, KnowledgeBaseIDs: input.KnowledgeBaseIDs})
	if err != nil {
		return appservice.WebsiteChannelHelpCenter{}, channelMutationError(meta, err, i18n.ErrorChannelHelpCenterUpdateFailed)
	}
	return appservice.WebsiteChannelHelpCenter{Enabled: record.Enabled, KnowledgeBaseIDs: record.KnowledgeBaseIDs}, nil
}

// UpdateWebsiteChannelAccess 修改网站渠道允许使用的网站。
func (o *channelOps) UpdateWebsiteChannelAccess(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.WebsiteChannelAccessInput) (appservice.WebsiteChannelAccess, error) {
	setting, err := o.updateWebsiteChannelAccess.Execute(ctx, identity, channelID, channelaction.WebsiteChannelAccessInput{
		AllowedHosts: input.AllowedHosts,
	})
	if err != nil {
		return appservice.WebsiteChannelAccess{}, channelMutationError(meta, err, i18n.ErrorChannelAccessUpdateFailed)
	}
	slog.InfoContext(ctx, "网站渠道允许使用的网站更新成功", "channel_id", channelID)
	return websiteChannelAccessFromRecord(setting), nil
}

// DeactivateMessageChannel 停用消息渠道。
func (o *channelOps) DeactivateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.MessageChannelSummary, error) {
	return o.setMessageChannelEnabled(ctx, meta, identity, channelID, false)
}

// ActivateMessageChannel 启用消息渠道。
func (o *channelOps) ActivateMessageChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.MessageChannelSummary, error) {
	return o.setMessageChannelEnabled(ctx, meta, identity, channelID, true)
}

// setMessageChannelEnabled 修改消息渠道的启用状态。
func (o *channelOps) setMessageChannelEnabled(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, enabled bool) (appservice.MessageChannelSummary, error) {
	channel, err := o.updateMessageChannelStatus.Execute(ctx, identity, channelID, enabled)
	if err != nil {
		return appservice.MessageChannelSummary{}, channelError(meta, err, i18n.ErrorChannelUpdateFailed)
	}
	slog.InfoContext(ctx, "消息渠道状态已更新", "channel_id", channel.ID, "channel_type", channel.Type, "enabled", enabled)
	return messageChannelFromRecord(channel), nil
}

// ListChannelOptions 返回当前企业的渠道选择项。
func (o *channelOps) ListChannelOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.ChannelOptionList, error) {
	channels, err := o.listChannelOptions.Execute(ctx, identity)
	if err != nil {
		return appservice.ChannelOptionList{}, channelError(meta, err, i18n.ErrorChannelSummaryListFailed)
	}
	result := arr.Map(channels, func(channel channelaction.Option) appservice.ChannelOption {
		return appservice.ChannelOption{ID: channel.ID, Type: channel.Type, Name: channel.Name}
	})
	return appservice.ChannelOptionList{Channels: result}, nil
}

// channelMutationError 转换渠道写入校验和操作错误。
func channelMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return channelMutationErrors.Translate(meta, err, failureKey)
}

// channelErrors 是渠道读取和状态修改的错误转换规则。
var channelErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(channelaction.ErrNotFound, dispatch.NotFound(i18n.ErrorChannelNotFound)),
	dispatch.Is(channelaction.ErrWechatAccountEnabledElsewhere, dispatch.Conflict(i18n.ErrorWechatAccountEnabledElsewhere, "wechat_account_enabled_elsewhere")),
})

// channelMutationErrors 是渠道写入的错误转换规则。
var channelMutationErrors = dispatch.Catalogs(dispatch.Catalog{
	dispatch.FieldRule(channelFieldKeys),
	dispatch.Is(channelaction.ErrWechatPlatformRequired, dispatch.Conflict(i18n.ErrorWechatPlatformNotConfigured, "wechat_platform_not_configured")),
}, channelErrors)

// channelError 转换渠道读取和状态修改错误。
func channelError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return channelErrors.Translate(meta, err, failureKey)
}

// messageChannelFromRecord 转换消息渠道传输结构。
func messageChannelFromRecord(channel *channelaction.MessageChannelRecord) appservice.MessageChannelSummary {
	return appservice.MessageChannelSummary{
		ID: channel.ID, WorkspaceID: channel.WorkspaceID, CreatedByUserID: channel.CreatedByUserID,
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
	return appservice.WebsiteChannelHome{
		Enabled: setting.HomeEnabled,
		Welcome: support.Deref(setting.HomeWelcome), Headline: support.Deref(setting.HomeHeadline),
		Blocks: arr.Map(setting.HomeBlocks, func(block domain.WebsiteHomeBlock) appservice.WebsiteChannelHomeBlock {
			return appservice.WebsiteChannelHomeBlock{Type: block.Type, Enabled: block.Enabled}
		}),
		Links: arr.Map(setting.HomeLinks, func(link domain.WebsiteHomeLink) appservice.WebsiteChannelHomeLink {
			return appservice.WebsiteChannelHomeLink{Title: link.Title, URL: link.URL}
		}),
	}
}

// websiteChannelAccessFromRecord 转换网站渠道允许使用的网站。
func websiteChannelAccessFromRecord(setting *channelaction.WebsiteChannelSettingRecord) appservice.WebsiteChannelAccess {
	return appservice.WebsiteChannelAccess{AllowedHosts: setting.AllowedEmbedHosts}
}

// channelInput 转换消息渠道修改输入。
func channelInput(input appservice.MessageChannelInput) channelaction.MessageChannelInput {
	return channelaction.MessageChannelInput{
		Name: input.Name, Description: input.Description, DefaultLocale: input.DefaultLocale,
		NewConversationTarget: channelRoutingTargetInput(input.NewConversationTarget),
		FallbackTarget:        channelRoutingTargetInput(input.FallbackTarget),
	}
}

// channelRoutingTargetInput 转换渠道会话流转目标输入。
func channelRoutingTargetInput(target appservice.ChannelRoutingTarget) channelaction.RoutingTarget {
	return channelaction.RoutingTarget{Type: target.Type, ID: target.ID}
}

// channelRoutingTargetFromRecord 转换渠道记录中的会话流转目标。
func channelRoutingTargetFromRecord(targetType string, targetID *string) appservice.ChannelRoutingTarget {
	return appservice.ChannelRoutingTarget{Type: appservice.ChannelRoutingTargetType(targetType), ID: support.Deref(targetID)}
}

// channelFieldKeys 把渠道校验错误码映射为本地化文案键。
var channelFieldKeys = map[common.FieldCode]i18n.Key{
	channelaction.ValidationRoutingTargetInvalid: i18n.FieldChannelRoutingTargetInvalid,
	channelaction.ValidationHomeBlocksInvalid:    i18n.FieldChannelHomeBlocksInvalid,
	channelaction.ValidationAllowedHostInvalid:   i18n.FieldChannelAllowedHostInvalid,
	channelaction.ValidationKnowledgeBaseInvalid: i18n.FieldChannelKnowledgeBaseInvalid,
}
