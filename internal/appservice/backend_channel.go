package appservice

import "context"

// ChannelBackend 定义消息渠道、渠道账号与渠道绑定的业务调用。
type ChannelBackend interface {
	// ListMessageChannelTypes 返回当前支持的消息渠道类型及其能力。
	//appservice:route GET /channel-types perm=none
	ListMessageChannelTypes(context.Context, RequestMeta) (MessageChannelTypeList, error)
	// ListMessageChannels 返回消息渠道列表。
	//appservice:route GET /channels perm=customer_service.manage
	ListMessageChannels(context.Context, RequestMeta) (MessageChannelList, error)
	// GetWebsiteChannel 返回网站渠道详情。
	//appservice:route GET /channels/{channelID:uuid}/website perm=customer_service.manage
	GetWebsiteChannel(context.Context, RequestMeta, string) (WebsiteChannel, error)
	// GetWeComBotChannel 返回企业微信智能机器人渠道详情与连接状态。
	//appservice:route GET /channels/{channelID:uuid}/wecom-bot perm=customer_service.manage
	GetWeComBotChannel(context.Context, RequestMeta, string) (WeComBotChannel, error)
	// SaveWeComBotChannelConnection 保存企业微信智能机器人的长连接凭据。
	//appservice:route PUT /channels/{channelID:uuid}/wecom-bot/connection perm=customer_service.manage
	SaveWeComBotChannelConnection(context.Context, RequestMeta, string, WeComBotChannelConnectionInput) (WeComBotChannel, error)
	// ListChannelAccounts 返回服务员工渠道中的外部账号及其绑定成员。
	//appservice:route GET /channels/{channelID:uuid}/accounts perm=customer_service.manage
	ListChannelAccounts(context.Context, RequestMeta, string) (ChannelAccountList, error)
	// BindChannelAccount 把服务员工渠道中的外部账号绑定或改绑到成员。
	//appservice:route PUT /channels/{channelID:uuid}/accounts/{accountID:uuid}/member perm=customer_service.manage
	BindChannelAccount(context.Context, RequestMeta, string, string, ChannelAccountBindingInput) error
	// UnbindChannelAccount 解除外部账号与成员的绑定。
	//appservice:route DELETE /channels/{channelID:uuid}/accounts/{accountID:uuid}/member perm=customer_service.manage
	UnbindChannelAccount(context.Context, RequestMeta, string, string) error
	// GetMessageChannel 返回消息渠道基础信息。
	//appservice:route GET /channels/{channelID:uuid} perm=customer_service.manage
	GetMessageChannel(context.Context, RequestMeta, string) (MessageChannelSummary, error)
	// CreateMessageChannel 创建消息渠道。
	//appservice:route POST /channels status=201 perm=customer_service.manage
	CreateMessageChannel(context.Context, RequestMeta, CreateMessageChannelInput) (MessageChannelSummary, error)
	// UpdateMessageChannel 修改消息渠道基础信息。
	//appservice:route PUT /channels/{channelID:uuid} perm=customer_service.manage
	UpdateMessageChannel(context.Context, RequestMeta, string, MessageChannelBasicsInput) (MessageChannelSummary, error)
	// UpdateMessageChannelReception 修改消息渠道接待设置。
	//appservice:route PUT /channels/{channelID:uuid}/reception perm=customer_service.manage
	UpdateMessageChannelReception(context.Context, RequestMeta, string, MessageChannelReceptionInput) (MessageChannelSummary, error)
	// UpdateWebsiteChannelChatInterface 修改网站渠道聊天窗口外观与对话功能。
	//appservice:route PUT /channels/{channelID:uuid}/website/chat-interface perm=customer_service.manage
	UpdateWebsiteChannelChatInterface(context.Context, RequestMeta, string, WebsiteChannelChatInterfaceInput) (WebsiteChannelChatInterface, error)
	// UpdateWebsiteChannelAccess 修改网站渠道允许使用的网站。
	//appservice:route PUT /channels/{channelID:uuid}/website/access perm=customer_service.manage
	UpdateWebsiteChannelAccess(context.Context, RequestMeta, string, WebsiteChannelAccessInput) (WebsiteChannelAccess, error)
	// UpdateWebsiteChannelHome 修改网站渠道 Messenger 首页。
	//appservice:route PUT /channels/{channelID:uuid}/website/home perm=customer_service.manage
	UpdateWebsiteChannelHome(context.Context, RequestMeta, string, WebsiteChannelHomeInput) (WebsiteChannelHome, error)
	// UpdateWebsiteChannelHelpCenter 修改网站渠道帮助页签开关与发布的知识库。
	//appservice:route PUT /channels/{channelID:uuid}/website/help-center perm=customer_service.manage
	UpdateWebsiteChannelHelpCenter(context.Context, RequestMeta, string, WebsiteChannelHelpCenterInput) (WebsiteChannelHelpCenter, error)
	// DeactivateMessageChannel 停用消息渠道。
	//appservice:route POST /channels/{channelID:uuid}/deactivate perm=customer_service.manage
	DeactivateMessageChannel(context.Context, RequestMeta, string) (MessageChannelSummary, error)
	// ActivateMessageChannel 启用消息渠道。
	//appservice:route POST /channels/{channelID:uuid}/activate perm=customer_service.manage
	ActivateMessageChannel(context.Context, RequestMeta, string) (MessageChannelSummary, error)
	// ListChannelOptions 返回当前企业的渠道选择项。
	//appservice:route GET /channels/options perm=none
	ListChannelOptions(context.Context, RequestMeta) (ChannelOptionList, error)
	// PreviewChannelBinding 返回绑定链接对应的工作区、渠道与外部账号。
	//appservice:route POST /channel-binding-previews auth=public
	PreviewChannelBinding(context.Context, RequestMeta, ChannelBindingTokenInput) (ChannelBindingPreview, error)
	// ConfirmChannelBinding 把绑定链接对应的外部账号绑定到当前账号在该工作区的成员身份。
	//appservice:route POST /channel-bindings auth=account
	ConfirmChannelBinding(context.Context, RequestMeta, ChannelBindingTokenInput) (ChannelBindingResult, error)
}
