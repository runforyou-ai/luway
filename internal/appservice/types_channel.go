package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

// ChannelType 表示渠道类型。
type ChannelType = domain.ChannelType

// ChannelDelivery 表示渠道把处理人回复送达对方的方式。
type ChannelDelivery = domain.ChannelDelivery

// ChannelAttachmentRule 定义渠道可外发的一类附件。
type ChannelAttachmentRule struct {
	// ContentTypes 是允许的内容类型，为空时不限类型。
	ContentTypes []string `json:"contentTypes"`
	ByteLimit    int64    `json:"byteLimit"`
}

// ChannelCapabilities 定义渠道类型的服务对象与对外收发能力。
type ChannelCapabilities struct {
	Audience ServiceAudience `json:"audience"`
	Delivery ChannelDelivery `json:"delivery"`
	// Typing 表示处理人输入时向对方展示正在输入。
	Typing bool `json:"typing"`
	// ReplyWindow 表示只能在对方互动开启的回复窗口内发送。
	ReplyWindow bool `json:"replyWindow"`
	// TextLimit 是单条对外文本消息的字符上限。
	TextLimit int `json:"textLimit"`
	// TextByteLimit 是单条对外文本消息的 UTF-8 字节上限，为 0 时不限字节数。
	TextByteLimit int `json:"textByteLimit"`
	// CaptionLimit 是附件说明的字符上限，为 0 时附件不带说明。
	CaptionLimit int `json:"captionLimit"`
	// Attachments 是可外发的附件类别，为空时不支持外发附件。
	Attachments []ChannelAttachmentRule `json:"attachments"`
}

// NewChannelCapabilities 返回渠道类型的能力描述。
func NewChannelCapabilities(channelType domain.ChannelType) ChannelCapabilities {
	capabilities := domain.ChannelCapabilitiesOf(channelType)
	attachments := arr.Map(capabilities.Attachments, func(rule domain.ChannelAttachmentRule) ChannelAttachmentRule {
		return ChannelAttachmentRule{ContentTypes: append([]string{}, rule.ContentTypes...), ByteLimit: rule.ByteLimit}
	})
	return ChannelCapabilities{
		Audience: ServiceAudience(capabilities.Audience), Delivery: ChannelDelivery(capabilities.Delivery),
		Typing: capabilities.Typing, ReplyWindow: capabilities.ReplyWindow,
		TextLimit: capabilities.TextLimit, TextByteLimit: capabilities.TextByteLimit, CaptionLimit: capabilities.CaptionLimit, Attachments: attachments,
	}
}

// MessageChannelTypeInfo 定义一种消息渠道类型及其能力。
type MessageChannelTypeInfo struct {
	Type         ChannelType         `json:"type"`
	Capabilities ChannelCapabilities `json:"capabilities"`
}

// MessageChannelTypeList 定义当前支持的消息渠道类型。
type MessageChannelTypeList struct {
	Types []MessageChannelTypeInfo `json:"types"`
}

// ChannelRoutingTargetType 表示渠道会话流转目标类型。
type ChannelRoutingTargetType = domain.ChannelRoutingTargetType

// MessageChannelSummary 定义消息渠道列表项和基础信息。
type MessageChannelSummary struct {
	ID                    string               `json:"id"`
	WorkspaceID           string               `json:"workspaceId"`
	CreatedByUserID       string               `json:"createdByUserId"`
	Type                  ChannelType          `json:"type"`
	Name                  string               `json:"name"`
	Description           *string              `json:"description"`
	DefaultLocale         CustomerLocale       `json:"defaultLocale"`
	NewConversationTarget ChannelRoutingTarget `json:"newConversationTarget"`
	FallbackTarget        ChannelRoutingTarget `json:"fallbackTarget"`
	Enabled               bool                 `json:"enabled"`
	CreatedAt             time.Time            `json:"createdAt"`
	UpdatedAt             time.Time            `json:"updatedAt"`
}

// ChannelRoutingTarget 定义渠道会话流转目标。
type ChannelRoutingTarget struct {
	Type ChannelRoutingTargetType `json:"type"`
	ID   string                   `json:"id"`
}

// WebsiteChannel 定义网站渠道详情。
type WebsiteChannel struct {
	MessageChannelSummary
	ChatInterface WebsiteChannelChatInterface `json:"chatInterface"`
	Home          WebsiteChannelHome          `json:"home"`
	HelpCenter    WebsiteChannelHelpCenter    `json:"helpCenter"`
	Access        WebsiteChannelAccess        `json:"access"`
}

// TelegramWebhookStatus 表示 Telegram Webhook 的连接状态。
type TelegramWebhookStatus = domain.TelegramWebhookStatus

// TelegramConnectionMode 表示 Telegram 渠道接收消息的接入方式。
type TelegramConnectionMode = domain.TelegramConnectionMode

// TelegramChannel 定义 Telegram 渠道详情。
type TelegramChannel struct {
	MessageChannelSummary
	Connection TelegramChannelConnection `json:"connection"`
}

// TelegramChannelConnection 定义 Telegram 接入方式、机器人和回调信息；网关转发时 WebhookURL 与 WebhookSecret 是业务系统转发使用的地址与密钥。
type TelegramChannelConnection struct {
	ConnectionMode TelegramConnectionMode `json:"connectionMode"`
	BotToken       string                 `json:"botToken"`
	BotID          *string                `json:"botId"`
	BotUsername    *string                `json:"botUsername"`
	BotDisplayName *string                `json:"botDisplayName"`
	WebhookURL     string                 `json:"webhookUrl"`
	WebhookSecret  string                 `json:"webhookSecret"`
	WebhookStatus  *TelegramWebhookStatus `json:"webhookStatus"`
}

// TelegramChannelConnectionInput 定义 Telegram 连接保存输入。
type TelegramChannelConnectionInput struct {
	ConnectionMode  TelegramConnectionMode `json:"connectionMode" validate:"oneof=direct gateway" msg:"field.telegram_connection_mode_invalid"`
	BotToken        string                 `json:"botToken" validate:"notblank,max=512" msg:"notblank=field.telegram_bot_token_required,max=field.telegram_bot_token_too_long"`
	WebhookBaseURL  string                 `json:"webhookBaseURL"`
	ConfirmBotReuse bool                   `json:"confirmBotReuse"`
}

// TelegramChannelConnectionTestInput 定义 Telegram 草稿连接测试输入。
type TelegramChannelConnectionTestInput struct {
	BotToken string `json:"botToken" validate:"notblank,max=512" msg:"notblank=field.telegram_bot_token_required,max=field.telegram_bot_token_too_long"`
}

// ChannelConnectionStatus 表示长连接渠道的平台连接状态。
type ChannelConnectionStatus string

const (
	ChannelConnectionConnecting ChannelConnectionStatus = "connecting"
	ChannelConnectionOnline     ChannelConnectionStatus = "online"
	ChannelConnectionReplaced   ChannelConnectionStatus = "replaced"
	ChannelConnectionRejected   ChannelConnectionStatus = "rejected"
	ChannelConnectionOffline    ChannelConnectionStatus = "offline"
)

// ChannelConnectionState 定义长连接渠道当前的平台连接状态；渠道未启用或尚无服务端实例接管时 Status 为空。
type ChannelConnectionState struct {
	Status      *ChannelConnectionStatus `json:"status"`
	ConnectedAt *time.Time               `json:"connectedAt"`
	UpdatedAt   *time.Time               `json:"updatedAt"`
}

// WeComBotChannel 定义企业微信智能机器人渠道详情。
type WeComBotChannel struct {
	MessageChannelSummary
	Connection WeComBotChannelConnection `json:"connection"`
}

// WeComBotChannelConnection 定义机器人长连接凭据与连接状态。
type WeComBotChannelConnection struct {
	BotID  string                 `json:"botId"`
	Secret string                 `json:"secret"`
	State  ChannelConnectionState `json:"state"`
}

// WeComBotChannelConnectionInput 定义机器人长连接凭据保存输入。
type WeComBotChannelConnectionInput struct {
	BotID  string `json:"botId" validate:"notblank,max=256" msg:"field.wecom_bot_id_required"`
	Secret string `json:"secret" validate:"notblank,max=256" msg:"field.wecom_bot_secret_required"`
}

// ChannelAccount 定义服务员工渠道中的一个外部账号；已绑定成员时 MemberIdentityID 有值并带成员名称与头像。
type ChannelAccount struct {
	ID               string     `json:"id"`
	ExternalID       string     `json:"externalId"`
	DisplayName      *string    `json:"displayName"`
	MemberIdentityID *string    `json:"memberIdentityId"`
	MemberName       string     `json:"memberName"`
	MemberAvatarURL  string     `json:"memberAvatarUrl"`
	LastSeenAt       *time.Time `json:"lastSeenAt"`
	CreatedAt        time.Time  `json:"createdAt"`
}

// ChannelAccountList 定义服务员工渠道中的外部账号列表。
type ChannelAccountList struct {
	Accounts []ChannelAccount `json:"accounts"`
}

// ChannelAccountBindingInput 定义把外部账号绑定到成员的输入。
type ChannelAccountBindingInput struct {
	MemberIdentityID string `json:"memberIdentityId" validate:"uuid" msg:"error.channel_account_member_invalid"`
}

// ChannelBindingStatus 表示渠道绑定链接的状态。
type ChannelBindingStatus = domain.ChannelBindingStatus

// ChannelBindingTokenInput 定义绑定链接中的令牌。
type ChannelBindingTokenInput struct {
	Token string `json:"token"`
}

// ChannelBindingPreview 定义绑定链接可见的工作区、渠道与外部账号。
type ChannelBindingPreview struct {
	WorkspaceName string               `json:"workspaceName"`
	WorkspaceSlug string               `json:"workspaceSlug"`
	ChannelName   string               `json:"channelName"`
	ChannelType   ChannelType          `json:"channelType"`
	ExternalName  string               `json:"externalName"`
	Status        ChannelBindingStatus `json:"status"`
}

// ChannelBindingResult 定义确认绑定后外部账号所属的工作区。
type ChannelBindingResult struct {
	WorkspaceSlug string `json:"workspaceSlug"`
}

// MessageChannelBasicsInput 定义渠道基础信息的编辑字段。
type MessageChannelBasicsInput struct {
	Name          string         `json:"name" validate:"notblank,max=100" msg:"notblank=field.channel_name_required,max=field.channel_name_too_long"`
	Description   string         `json:"description" validate:"max=2000" msg:"field.channel_description_too_long"`
	DefaultLocale CustomerLocale `json:"defaultLocale" validate:"oneof=zh-CN en-US hi-IN" msg:"field.channel_default_locale_invalid"`
}

// MessageChannelReceptionInput 定义渠道接待设置的编辑字段。
type MessageChannelReceptionInput struct {
	NewConversationTarget ChannelRoutingTarget `json:"newConversationTarget"`
	FallbackTarget        ChannelRoutingTarget `json:"fallbackTarget"`
}

// MessageChannelInput 定义消息渠道可编辑的通用字段。
type MessageChannelInput struct {
	Name                  string               `json:"name" validate:"notblank,max=100" msg:"notblank=field.channel_name_required,max=field.channel_name_too_long"`
	Description           string               `json:"description" validate:"max=2000" msg:"field.channel_description_too_long"`
	DefaultLocale         CustomerLocale       `json:"defaultLocale" validate:"oneof=zh-CN en-US hi-IN" msg:"field.channel_default_locale_invalid"`
	NewConversationTarget ChannelRoutingTarget `json:"newConversationTarget"`
	FallbackTarget        ChannelRoutingTarget `json:"fallbackTarget"`
}

// CreateMessageChannelInput 定义创建消息渠道所需字段。
type CreateMessageChannelInput struct {
	MessageChannelInput
	Type ChannelType `json:"type" validate:"oneof=website telegram wechat_official_account_key wechat_official_account_authorization wecom_bot" msg:"field.channel_type_invalid"`
}

// WebsiteChannelChatInterface 定义网站渠道聊天窗口外观与对话功能设置。
type WebsiteChannelChatInterface struct {
	Title              string  `json:"title"`
	GreetingMessage    *string `json:"greetingMessage"`
	ThemeColor         string  `json:"themeColor"`
	AttachmentsEnabled bool    `json:"attachmentsEnabled"`
	EmojiEnabled       bool    `json:"emojiEnabled"`
	RatingEnabled      bool    `json:"ratingEnabled"`
	// MultipleConversationsEnabled 为假时访客界面只提供一个对话。
	MultipleConversationsEnabled bool `json:"multipleConversationsEnabled"`
}

// WebsiteChannelChatInterfaceInput 定义网站渠道聊天窗口外观与对话功能输入。
type WebsiteChannelChatInterfaceInput struct {
	Title                        string `json:"title" validate:"notblank,max=100" msg:"notblank=field.channel_chat_title_required,max=field.channel_chat_title_too_long"`
	GreetingMessage              string `json:"greetingMessage" validate:"max=500" msg:"field.channel_greeting_too_long"`
	ThemeColor                   string `json:"themeColor" validate:"len=7,hexcolor" msg:"field.channel_theme_color_invalid"`
	AttachmentsEnabled           bool   `json:"attachmentsEnabled"`
	EmojiEnabled                 bool   `json:"emojiEnabled"`
	RatingEnabled                bool   `json:"ratingEnabled"`
	MultipleConversationsEnabled bool   `json:"multipleConversationsEnabled"`
}

// WebsiteHomeBlockType 定义网站 Messenger 首页卡片类型。
type WebsiteHomeBlockType = domain.WebsiteHomeBlockType

// WebsiteChannelHomeBlock 定义网站 Messenger 首页的一张卡片及其开关。
type WebsiteChannelHomeBlock struct {
	Type    WebsiteHomeBlockType `json:"type"`
	Enabled bool                 `json:"enabled"`
}

// WebsiteChannelHomeLink 定义网站 Messenger 首页链接卡片中的一条链接。
type WebsiteChannelHomeLink struct {
	Title string `json:"title" validate:"notblank,max=100" msg:"notblank=field.channel_home_link_title_required,max=field.channel_home_link_title_too_long"`
	URL   string `json:"url" validate:"http_url,max=2048" msg:"field.channel_home_link_url_invalid"`
}

// WebsiteChannelHome 定义网站 Messenger 首页设置；问候语为空时访客端使用默认文案。
type WebsiteChannelHome struct {
	// Enabled 为假时访客界面不显示首页，打开后直接进入对话。
	Enabled  bool                      `json:"enabled"`
	Welcome  string                    `json:"welcome"`
	Headline string                    `json:"headline"`
	Blocks   []WebsiteChannelHomeBlock `json:"blocks"`
	Links    []WebsiteChannelHomeLink  `json:"links"`
}

// WebsiteChannelHomeInput 定义网站 Messenger 首页输入，卡片须包含每种类型各一次。
type WebsiteChannelHomeInput struct {
	Enabled  bool                      `json:"enabled"`
	Welcome  string                    `json:"welcome" validate:"max=100" msg:"field.channel_home_greeting_too_long"`
	Headline string                    `json:"headline" validate:"max=100" msg:"field.channel_home_greeting_too_long"`
	Blocks   []WebsiteChannelHomeBlock `json:"blocks"`
	Links    []WebsiteChannelHomeLink  `json:"links" validate:"dive"`
}

// WebsiteChannelAccess 定义网站渠道允许使用的网站。
type WebsiteChannelAccess struct {
	AllowedHosts []string `json:"allowedHosts"`
}

// WebsiteChannelAccessInput 定义网站渠道允许使用的网站输入。
type WebsiteChannelAccessInput struct {
	AllowedHosts []string `json:"allowedHosts" validate:"max=50" msg:"field.channel_allowed_hosts_too_many"`
}

// WebsiteChannelHelpCenter 定义网站渠道帮助页签开关与发布的知识库，知识库按名称排序；开启且有文章时访客端显示帮助页签。
type WebsiteChannelHelpCenter struct {
	Enabled          bool     `json:"enabled"`
	KnowledgeBaseIDs []string `json:"knowledgeBaseIds"`
}

// WebsiteChannelHelpCenterInput 定义网站渠道帮助页签开关与发布的知识库输入。
type WebsiteChannelHelpCenterInput struct {
	Enabled          bool     `json:"enabled"`
	KnowledgeBaseIDs []string `json:"knowledgeBaseIds" validate:"dive,uuid" msg:"field.channel_knowledge_base_invalid"`
}

// ChannelOption 定义渠道选择项。
type ChannelOption struct {
	ID   string      `json:"id"`
	Type ChannelType `json:"type"`
	Name string      `json:"name"`
}

// ChannelOptionList 定义渠道选择项列表。
type ChannelOptionList struct {
	Channels []ChannelOption `json:"channels"`
}

// MessageChannelList 定义消息渠道列表。
type MessageChannelList struct {
	Channels []MessageChannelSummary `json:"channels"`
}
