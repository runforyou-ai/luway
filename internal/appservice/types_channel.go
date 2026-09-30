package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// ChannelType 表示渠道类型。
type ChannelType string

const (
	ChannelTypeWebsite               ChannelType = ChannelType(domain.ChannelTypeWebsite)
	ChannelTypeTelegram              ChannelType = ChannelType(domain.ChannelTypeTelegram)
	ChannelTypeWeChatOfficialAccount ChannelType = ChannelType(domain.ChannelTypeWeChatOfficialAccount)
)

// ChannelRoutingTargetType 表示渠道会话流转目标类型。
type ChannelRoutingTargetType string

const (
	ChannelRoutingTargetTypePublicQueue ChannelRoutingTargetType = ChannelRoutingTargetType(domain.ChannelRoutingTargetTypePublicQueue)
	ChannelRoutingTargetTypeTeam        ChannelRoutingTargetType = ChannelRoutingTargetType(domain.ChannelRoutingTargetTypeTeam)
	ChannelRoutingTargetTypeMember      ChannelRoutingTargetType = ChannelRoutingTargetType(domain.ChannelRoutingTargetTypeMember)
)

// MessageChannelSummary 定义消息渠道列表项和基础信息。
type MessageChannelSummary struct {
	ID                    string               `json:"id"`
	OrganizationID        string               `json:"organizationId"`
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
type TelegramWebhookStatus string

const (
	TelegramWebhookStatusWaiting TelegramWebhookStatus = TelegramWebhookStatus(domain.TelegramWebhookStatusWaiting)
	TelegramWebhookStatusNormal  TelegramWebhookStatus = TelegramWebhookStatus(domain.TelegramWebhookStatusNormal)
)

// TelegramChannel 定义 Telegram 渠道详情。
type TelegramChannel struct {
	MessageChannelSummary
	Connection TelegramChannelConnection `json:"connection"`
}

// TelegramChannelConnection 定义 Telegram 机器人和 Webhook 信息。
type TelegramChannelConnection struct {
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
	BotToken        string `json:"botToken"`
	WebhookBaseURL  string `json:"webhookBaseURL"`
	ConfirmBotReuse bool   `json:"confirmBotReuse"`
}

// TelegramChannelConnectionTestInput 定义 Telegram 草稿连接测试输入。
type TelegramChannelConnectionTestInput struct {
	BotToken string `json:"botToken"`
}

// MessageChannelBasicsInput 定义渠道基础信息的编辑字段。
type MessageChannelBasicsInput struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	DefaultLocale CustomerLocale `json:"defaultLocale"`
}

// MessageChannelReceptionInput 定义渠道接待设置的编辑字段。
type MessageChannelReceptionInput struct {
	NewConversationTarget ChannelRoutingTarget `json:"newConversationTarget"`
	FallbackTarget        ChannelRoutingTarget `json:"fallbackTarget"`
}

// MessageChannelInput 定义消息渠道可编辑的通用字段。
type MessageChannelInput struct {
	Name                  string               `json:"name"`
	Description           string               `json:"description"`
	DefaultLocale         CustomerLocale       `json:"defaultLocale"`
	NewConversationTarget ChannelRoutingTarget `json:"newConversationTarget"`
	FallbackTarget        ChannelRoutingTarget `json:"fallbackTarget"`
}

// CreateMessageChannelInput 定义创建消息渠道所需字段。
type CreateMessageChannelInput struct {
	MessageChannelInput
	Type ChannelType `json:"type"`
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
	Title                        string `json:"title"`
	GreetingMessage              string `json:"greetingMessage"`
	ThemeColor                   string `json:"themeColor"`
	AttachmentsEnabled           bool   `json:"attachmentsEnabled"`
	EmojiEnabled                 bool   `json:"emojiEnabled"`
	RatingEnabled                bool   `json:"ratingEnabled"`
	MultipleConversationsEnabled bool   `json:"multipleConversationsEnabled"`
}

// WebsiteHomeBlockType 定义网站 Messenger 首页卡片类型。
type WebsiteHomeBlockType string

const (
	WebsiteHomeBlockRecentConversation WebsiteHomeBlockType = WebsiteHomeBlockType(domain.WebsiteHomeBlockRecentConversation)
	WebsiteHomeBlockStartConversation  WebsiteHomeBlockType = WebsiteHomeBlockType(domain.WebsiteHomeBlockStartConversation)
	WebsiteHomeBlockLinks              WebsiteHomeBlockType = WebsiteHomeBlockType(domain.WebsiteHomeBlockLinks)
)

// WebsiteChannelHomeBlock 定义网站 Messenger 首页的一张卡片及其开关。
type WebsiteChannelHomeBlock struct {
	Type    WebsiteHomeBlockType `json:"type"`
	Enabled bool                 `json:"enabled"`
}

// WebsiteChannelHomeLink 定义网站 Messenger 首页链接卡片中的一条链接。
type WebsiteChannelHomeLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
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
	Welcome  string                    `json:"welcome"`
	Headline string                    `json:"headline"`
	Blocks   []WebsiteChannelHomeBlock `json:"blocks"`
	Links    []WebsiteChannelHomeLink  `json:"links"`
}

// WebsiteChannelAccess 定义网站渠道允许使用的网站。
type WebsiteChannelAccess struct {
	AllowedHosts []string `json:"allowedHosts"`
}

// WebsiteChannelAccessInput 定义网站渠道允许使用的网站输入。
type WebsiteChannelAccessInput struct {
	AllowedHosts []string `json:"allowedHosts"`
}

// WebsiteChannelHelpCenter 定义网站渠道帮助页签开关与发布的知识库，知识库按名称排序；开启且有文章时访客端显示帮助页签。
type WebsiteChannelHelpCenter struct {
	Enabled          bool     `json:"enabled"`
	KnowledgeBaseIDs []string `json:"knowledgeBaseIds"`
}

// WebsiteChannelHelpCenterInput 定义网站渠道帮助页签开关与发布的知识库输入。
type WebsiteChannelHelpCenterInput struct {
	Enabled          bool     `json:"enabled"`
	KnowledgeBaseIDs []string `json:"knowledgeBaseIds"`
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
