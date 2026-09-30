//go:build server

package channel

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// MessageChannelBasicsInput 定义渠道基础信息的编辑字段。
type MessageChannelBasicsInput struct {
	Name          string
	Description   string
	DefaultLocale domain.CustomerLocale
}

// MessageChannelReceptionInput 定义渠道接待设置的编辑字段。
type MessageChannelReceptionInput struct {
	NewConversationTarget RoutingTarget
	FallbackTarget        RoutingTarget
}

// MessageChannelInput 定义消息渠道可编辑的通用字段。
type MessageChannelInput struct {
	Name                  string
	Description           string
	DefaultLocale         domain.CustomerLocale
	NewConversationTarget RoutingTarget
	FallbackTarget        RoutingTarget
}

// CreateMessageChannelInput 定义创建消息渠道所需字段。
type CreateMessageChannelInput struct {
	MessageChannelInput
	Type domain.ChannelType
}

// RoutingTarget 定义渠道会话流转目标。
type RoutingTarget struct {
	Type domain.ChannelRoutingTargetType
	ID   string
}

// WebsiteChannelChatInterfaceInput 定义网站渠道聊天窗口外观与对话功能的可编辑字段。
type WebsiteChannelChatInterfaceInput struct {
	Title              string
	GreetingMessage    string
	ThemeColor         string
	AttachmentsEnabled bool
	EmojiEnabled       bool
	RatingEnabled      bool
	// MultipleConversationsEnabled 为假时同一访客只有一个对话。
	MultipleConversationsEnabled bool
}

// WebsiteChannelHomeInput 定义网站渠道 Messenger 首页的可编辑字段。
type WebsiteChannelHomeInput struct {
	Enabled  bool
	Welcome  string
	Headline string
	Blocks   []domain.WebsiteHomeBlock
	Links    []domain.WebsiteHomeLink
}

// WebsiteChannelAccessInput 定义网站渠道允许使用的网站输入。
type WebsiteChannelAccessInput struct {
	AllowedHosts []string
}

// WebsiteChannelHelpCenterInput 定义网站渠道帮助页签开关与发布的知识库输入。
type WebsiteChannelHelpCenterInput struct {
	Enabled          bool
	KnowledgeBaseIDs []string
}

// TelegramChannelConnectionInput 定义 Telegram 连接可编辑字段。
type TelegramChannelConnectionInput struct {
	BotToken        string
	WebhookBaseURL  string
	ConfirmBotReuse bool
}

// TelegramChannelConnectionTestInput 定义 Telegram 草稿连接测试字段。
type TelegramChannelConnectionTestInput struct {
	BotToken string
}

// MessageChannelRecord 定义消息渠道传输字段。
type MessageChannelRecord struct {
	ID                        string    `json:"id"`
	OrganizationID            string    `json:"organizationId"`
	CreatedByUserID           string    `json:"createdByUserId"`
	Type                      string    `json:"type"`
	Name                      string    `json:"name"`
	Description               *string   `json:"description"`
	DefaultLocale             string    `json:"defaultLocale"`
	InitialRoutingTargetType  string    `json:"initialRoutingTargetType"`
	InitialRoutingTargetID    *string   `json:"initialRoutingTargetId"`
	FallbackRoutingTargetType string    `json:"fallbackRoutingTargetType"`
	FallbackRoutingTargetID   *string   `json:"fallbackRoutingTargetId"`
	Enabled                   bool      `json:"enabled"`
	CreatedAt                 time.Time `json:"createdAt"`
	UpdatedAt                 time.Time `json:"updatedAt"`
}

// WebsiteChannelSettingRecord 定义网站渠道 Messenger 设置传输字段。
type WebsiteChannelSettingRecord struct {
	ChatTitle                    string                    `json:"title"`
	GreetingMessage              *string                   `json:"greetingMessage"`
	ThemeColor                   string                    `json:"themeColor"`
	HomeEnabled                  bool                      `json:"homeEnabled"`
	HelpEnabled                  bool                      `json:"helpEnabled"`
	HomeWelcome                  *string                   `json:"homeWelcome"`
	HomeHeadline                 *string                   `json:"homeHeadline"`
	HomeBlocks                   []domain.WebsiteHomeBlock `json:"homeBlocks"`
	HomeLinks                    []domain.WebsiteHomeLink  `json:"homeLinks"`
	AttachmentsEnabled           bool                      `json:"attachmentsEnabled"`
	EmojiEnabled                 bool                      `json:"emojiEnabled"`
	RatingEnabled                bool                      `json:"ratingEnabled"`
	MultipleConversationsEnabled bool                      `json:"multipleConversationsEnabled"`
	AllowedEmbedHosts            []string                  `json:"allowedHosts"`
}

// TelegramChannelSettingRecord 定义 Telegram 机器人和 Webhook 传输字段。
type TelegramChannelSettingRecord struct {
	BotToken           string     `json:"botToken"`
	BotID              *int64     `json:"botId"`
	BotUsername        *string    `json:"botUsername"`
	BotDisplayName     *string    `json:"botDisplayName"`
	WebhookBaseURL     string     `json:"webhookBaseUrl"`
	WebhookURL         string     `json:"webhookUrl"`
	WebhookSecret      string     `json:"webhookSecret"`
	WebhookStatus      *string    `json:"webhookStatus"`
	WebhookConnectedAt *time.Time `json:"webhookConnectedAt"`
}

// messageChannelRecord 把渠道存储模型转换为传输结构。
func messageChannelRecord(channel *servermodels.Channel) *MessageChannelRecord {
	return &MessageChannelRecord{
		ID:                        channel.ID,
		OrganizationID:            channel.OrganizationID,
		CreatedByUserID:           channel.CreatedByUserID,
		Type:                      channel.Type,
		Name:                      channel.Name,
		Description:               channel.Description,
		DefaultLocale:             channel.DefaultLocale,
		InitialRoutingTargetType:  channel.InitialRoutingTargetType,
		InitialRoutingTargetID:    channel.InitialRoutingTargetID,
		FallbackRoutingTargetType: channel.FallbackRoutingTargetType,
		FallbackRoutingTargetID:   channel.FallbackRoutingTargetID,
		Enabled:                   channel.Enabled,
		CreatedAt:                 channel.CreatedAt,
		UpdatedAt:                 channel.UpdatedAt,
	}
}

// websiteChannelSettingRecord 把网站渠道设置存储模型转换为传输结构。
func websiteChannelSettingRecord(setting *servermodels.WebsiteChannelSetting) WebsiteChannelSettingRecord {
	return WebsiteChannelSettingRecord{
		ChatTitle:                    setting.ChatTitle,
		GreetingMessage:              setting.GreetingMessage,
		ThemeColor:                   setting.ThemeColor,
		HomeEnabled:                  setting.HomeEnabled,
		HelpEnabled:                  setting.HelpEnabled,
		HomeWelcome:                  setting.HomeWelcome,
		HomeHeadline:                 setting.HomeHeadline,
		HomeBlocks:                   setting.HomeBlocks,
		HomeLinks:                    setting.HomeLinks,
		AttachmentsEnabled:           setting.AttachmentsEnabled,
		EmojiEnabled:                 setting.EmojiEnabled,
		RatingEnabled:                setting.RatingEnabled,
		MultipleConversationsEnabled: setting.MultipleConversationsEnabled,
		AllowedEmbedHosts:            setting.AllowedEmbedHosts,
	}
}

// optionalStringValue 返回可空字符串的实际值。
func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
