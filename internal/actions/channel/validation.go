//go:build server

// Package channel 实现消息渠道领域的应用操作。
package channel

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/common/embedhost"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	// DefaultWebsiteChannelThemeColor 是网站聊天界面的默认主题色。
	DefaultWebsiteChannelThemeColor = "#2563EB"
)

var themeColorPattern = regexp.MustCompile(`^#[0-9A-F]{6}$`)

// ValidationCode 标识渠道字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationTypeInvalid            ValidationCode = "CHANNEL_TYPE_INVALID"
	ValidationNameRequired           ValidationCode = "CHANNEL_NAME_REQUIRED"
	ValidationNameTooLong            ValidationCode = "CHANNEL_NAME_TOO_LONG"
	ValidationDescriptionTooLong     ValidationCode = "CHANNEL_DESCRIPTION_TOO_LONG"
	ValidationDefaultLocaleInvalid   ValidationCode = "CHANNEL_DEFAULT_LOCALE_INVALID"
	ValidationRoutingTargetInvalid   ValidationCode = "CHANNEL_ROUTING_TARGET_INVALID"
	ValidationChatTitleRequired      ValidationCode = "CHANNEL_CHAT_TITLE_REQUIRED"
	ValidationChatTitleTooLong       ValidationCode = "CHANNEL_CHAT_TITLE_TOO_LONG"
	ValidationGreetingTooLong        ValidationCode = "CHANNEL_GREETING_MESSAGE_TOO_LONG"
	ValidationThemeColorInvalid      ValidationCode = "CHANNEL_THEME_COLOR_INVALID"
	ValidationHomeGreetingTooLong    ValidationCode = "CHANNEL_HOME_GREETING_TOO_LONG"
	ValidationHomeBlocksInvalid      ValidationCode = "CHANNEL_HOME_BLOCKS_INVALID"
	ValidationHomeLinkTitleRequired  ValidationCode = "CHANNEL_HOME_LINK_TITLE_REQUIRED"
	ValidationHomeLinkTitleTooLong   ValidationCode = "CHANNEL_HOME_LINK_TITLE_TOO_LONG"
	ValidationHomeLinkURLInvalid     ValidationCode = "CHANNEL_HOME_LINK_URL_INVALID"
	ValidationAllowedHostsTooMany    ValidationCode = "CHANNEL_ALLOWED_HOSTS_TOO_MANY"
	ValidationAllowedHostInvalid     ValidationCode = "CHANNEL_ALLOWED_HOST_INVALID"
	ValidationKnowledgeBaseInvalid   ValidationCode = "CHANNEL_KNOWLEDGE_BASE_INVALID"
	ValidationTelegramTokenRequired  ValidationCode = "TELEGRAM_BOT_TOKEN_REQUIRED"
	ValidationTelegramTokenTooLong   ValidationCode = "TELEGRAM_BOT_TOKEN_TOO_LONG"
	ValidationTelegramTokenInvalid   ValidationCode = "TELEGRAM_BOT_TOKEN_INVALID"
	ValidationTelegramBaseURLInvalid ValidationCode = "TELEGRAM_WEBHOOK_BASE_URL_INVALID"
)

const (
	// maxNameLength 是渠道名称的最大字符数。
	maxNameLength = 100
	// maxDescriptionLength 是渠道描述的最大字符数。
	maxDescriptionLength = 2000
	// maxChatTitleLength 是聊天界面标题的最大字符数。
	maxChatTitleLength = 100
	// maxGreetingMessageLength 是欢迎语的最大字符数。
	maxGreetingMessageLength = 500
	// maxHomeGreetingLength 是首页问候语每行的最大字符数。
	maxHomeGreetingLength = 100
	// maxHomeLinkTitleLength 是首页链接标题的最大字符数。
	maxHomeLinkTitleLength = 100
	// maxHomeLinkURLLength 是首页链接地址的最大长度。
	maxHomeLinkURLLength = 2048
	// maxTelegramBotTokenLength 是 Telegram Bot Token 的最大存储长度。
	maxTelegramBotTokenLength = 512
	// maxTelegramWebhookBaseURLLength 是 Webhook 基础地址的最大长度。
	maxTelegramWebhookBaseURLLength = 2048
)

// ValidationError 表示渠道字段校验失败。
type ValidationError = common.FieldError

// normalizeCreateMessageChannelInput 规范化并校验消息渠道创建输入。
func normalizeCreateMessageChannelInput(input CreateMessageChannelInput) (CreateMessageChannelInput, map[string]ValidationCode) {
	input.Type = domain.ChannelType(strings.TrimSpace(string(input.Type)))
	normalized, fields := normalizeMessageChannelInput(input.MessageChannelInput)
	input.MessageChannelInput = normalized
	if !domain.SupportedMessageChannelType(input.Type) {
		fields["type"] = ValidationTypeInvalid
	}
	return input, fields
}

// normalizeMessageChannelInput 规范化并校验消息渠道通用输入。
func normalizeMessageChannelInput(input MessageChannelInput) (MessageChannelInput, map[string]ValidationCode) {
	basics, fields := normalizeMessageChannelBasics(MessageChannelBasicsInput{
		Name:          input.Name,
		Description:   input.Description,
		DefaultLocale: input.DefaultLocale,
	})
	reception, receptionFields := normalizeMessageChannelReception(MessageChannelReceptionInput{
		NewConversationTarget: input.NewConversationTarget,
		FallbackTarget:        input.FallbackTarget,
	})
	for field, code := range receptionFields {
		fields[field] = code
	}
	return MessageChannelInput{
		Name:                  basics.Name,
		Description:           basics.Description,
		DefaultLocale:         basics.DefaultLocale,
		NewConversationTarget: reception.NewConversationTarget,
		FallbackTarget:        reception.FallbackTarget,
	}, fields
}

// normalizeMessageChannelBasics 规范化并校验渠道基础信息。
func normalizeMessageChannelBasics(input MessageChannelBasicsInput) (MessageChannelBasicsInput, map[string]ValidationCode) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.DefaultLocale = domain.CustomerLocale(strings.TrimSpace(string(input.DefaultLocale)))
	fields := make(map[string]ValidationCode)
	if input.Name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(input.Name) > maxNameLength {
		fields["name"] = ValidationNameTooLong
	}
	if utf8.RuneCountInString(input.Description) > maxDescriptionLength {
		fields["description"] = ValidationDescriptionTooLong
	}
	if !slices.Contains(domain.CustomerLocales, input.DefaultLocale) {
		fields["defaultLocale"] = ValidationDefaultLocaleInvalid
	}
	return input, fields
}

// normalizeMessageChannelReception 规范化并校验渠道接待设置：新会话可交给成员、团队或公共队列，失败去向只能是团队或公共队列。
func normalizeMessageChannelReception(input MessageChannelReceptionInput) (MessageChannelReceptionInput, map[string]ValidationCode) {
	input.NewConversationTarget = normalizeRoutingTarget(input.NewConversationTarget)
	input.FallbackTarget = normalizeRoutingTarget(input.FallbackTarget)
	fields := make(map[string]ValidationCode)
	if !routingTargetShapeValid(input.NewConversationTarget) {
		fields["newConversationTarget"] = ValidationRoutingTargetInvalid
	}
	// 失败去向只接受团队或公共队列。
	if input.FallbackTarget.Type == domain.ChannelRoutingTargetTypeMember || !routingTargetShapeValid(input.FallbackTarget) {
		fields["fallbackTarget"] = ValidationRoutingTargetInvalid
	}
	if input.NewConversationTarget.Type != domain.ChannelRoutingTargetTypePublicQueue && input.NewConversationTarget.Type == input.FallbackTarget.Type && input.NewConversationTarget.ID == input.FallbackTarget.ID {
		fields["fallbackTarget"] = ValidationRoutingTargetInvalid
	}
	return input, fields
}

// normalizeRoutingTarget 规范化会话流转目标。
func normalizeRoutingTarget(target RoutingTarget) RoutingTarget {
	target.Type = domain.ChannelRoutingTargetType(strings.TrimSpace(string(target.Type)))
	target.ID = strings.TrimSpace(target.ID)
	return target
}

// routingTargetShapeValid 校验会话流转目标的字段组合。
func routingTargetShapeValid(target RoutingTarget) bool {
	switch target.Type {
	case domain.ChannelRoutingTargetTypePublicQueue:
		return target.ID == ""
	case domain.ChannelRoutingTargetTypeTeam, domain.ChannelRoutingTargetTypeMember:
		return common.ValidUUID(target.ID)
	default:
		return false
	}
}

// normalizeWebsiteChannelChatInterfaceInput 规范化并校验聊天界面输入。
func normalizeWebsiteChannelChatInterfaceInput(input WebsiteChannelChatInterfaceInput) (WebsiteChannelChatInterfaceInput, map[string]ValidationCode) {
	input.Title = strings.TrimSpace(input.Title)
	input.GreetingMessage = strings.TrimSpace(input.GreetingMessage)
	input.ThemeColor = strings.ToUpper(strings.TrimSpace(input.ThemeColor))

	fields := make(map[string]ValidationCode)
	if input.Title == "" {
		fields["title"] = ValidationChatTitleRequired
	} else if utf8.RuneCountInString(input.Title) > maxChatTitleLength {
		fields["title"] = ValidationChatTitleTooLong
	}
	if utf8.RuneCountInString(input.GreetingMessage) > maxGreetingMessageLength {
		fields["greetingMessage"] = ValidationGreetingTooLong
	}
	if !themeColorPattern.MatchString(input.ThemeColor) {
		fields["themeColor"] = ValidationThemeColorInvalid
	}
	return input, fields
}

// normalizeWebsiteChannelHomeInput 规范化并校验首页问候语、卡片顺序与链接。
func normalizeWebsiteChannelHomeInput(input WebsiteChannelHomeInput) (WebsiteChannelHomeInput, map[string]ValidationCode) {
	input.Welcome = strings.TrimSpace(input.Welcome)
	input.Headline = strings.TrimSpace(input.Headline)
	fields := make(map[string]ValidationCode)
	if utf8.RuneCountInString(input.Welcome) > maxHomeGreetingLength {
		fields["welcome"] = ValidationHomeGreetingTooLong
	}
	if utf8.RuneCountInString(input.Headline) > maxHomeGreetingLength {
		fields["headline"] = ValidationHomeGreetingTooLong
	}
	// 首页卡片必须恰好包含每种卡片各一次。
	seen := make(map[domain.WebsiteHomeBlockType]bool, len(input.Blocks))
	for _, block := range input.Blocks {
		if !slices.Contains(domain.WebsiteHomeBlockTypes(), block.Type) || seen[block.Type] {
			fields["blocks"] = ValidationHomeBlocksInvalid
		}
		seen[block.Type] = true
	}
	if len(seen) != len(domain.WebsiteHomeBlockTypes()) {
		fields["blocks"] = ValidationHomeBlocksInvalid
	}
	// 规范化首页链接，标题必填，地址为 HTTP(S) 绝对地址。
	links := make([]domain.WebsiteHomeLink, 0, len(input.Links))
	for _, link := range input.Links {
		link.Title = strings.TrimSpace(link.Title)
		link.URL = strings.TrimSpace(link.URL)
		switch {
		case link.Title == "":
			fields["links"] = ValidationHomeLinkTitleRequired
		case utf8.RuneCountInString(link.Title) > maxHomeLinkTitleLength:
			fields["links"] = ValidationHomeLinkTitleTooLong
		case !validHomeLinkURL(link.URL):
			fields["links"] = ValidationHomeLinkURLInvalid
		}
		links = append(links, link)
	}
	input.Links = links
	return input, fields
}

// validHomeLinkURL 判断首页链接地址是否为不超长的 HTTP(S) 绝对地址。
func validHomeLinkURL(value string) bool {
	if len(value) > maxHomeLinkURLLength {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// normalizeWebsiteChannelAccessInput 规范化并校验允许使用的网站。
func normalizeWebsiteChannelAccessInput(input WebsiteChannelAccessInput) (WebsiteChannelAccessInput, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	if len(input.AllowedHosts) > embedhost.MaxHosts {
		fields["allowedHosts"] = ValidationAllowedHostsTooMany
		return input, fields
	}
	normalized, ok := embedhost.NormalizeAll(input.AllowedHosts)
	if !ok {
		fields["allowedHosts"] = ValidationAllowedHostInvalid
		return input, fields
	}
	input.AllowedHosts = normalized
	return input, fields
}

// normalizeTelegramConnectionTestInput 规范化并校验 Telegram 测试输入。
func normalizeTelegramConnectionTestInput(input TelegramChannelConnectionTestInput) (TelegramChannelConnectionTestInput, map[string]ValidationCode) {
	input.BotToken = strings.TrimSpace(input.BotToken)
	fields := make(map[string]ValidationCode)
	if input.BotToken == "" {
		fields["botToken"] = ValidationTelegramTokenRequired
	} else if len(input.BotToken) > maxTelegramBotTokenLength {
		fields["botToken"] = ValidationTelegramTokenTooLong
	}
	return input, fields
}

// normalizeTelegramConnectionInput 规范化并校验 Telegram 保存输入。
func normalizeTelegramConnectionInput(input TelegramChannelConnectionInput) (TelegramChannelConnectionInput, map[string]ValidationCode) {
	testInput, fields := normalizeTelegramConnectionTestInput(TelegramChannelConnectionTestInput{BotToken: input.BotToken})
	input.BotToken = testInput.BotToken
	input.WebhookBaseURL = strings.TrimSpace(input.WebhookBaseURL)
	if len(input.WebhookBaseURL) > maxTelegramWebhookBaseURLLength {
		fields["webhookBaseURL"] = ValidationTelegramBaseURLInvalid
		return input, fields
	}
	parsed, err := url.ParseRequestURI(input.WebhookBaseURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		fields["webhookBaseURL"] = ValidationTelegramBaseURLInvalid
		return input, fields
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	input.WebhookBaseURL = parsed.String()
	return input, fields
}

// telegramWebhookURL 使用已保存基础地址生成 Telegram 回调地址。
func telegramWebhookURL(baseURL, channelID string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/public/telegram-channels/" + channelID + "/webhook"
	parsed.RawPath = ""
	return parsed.String(), nil
}
