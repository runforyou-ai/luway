//go:build server

// Package channel 实现消息渠道领域的应用操作。
package channel

import (
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/embedhost"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
)

const (
	// DefaultWebsiteChannelThemeColor 是网站聊天界面的默认主题色。
	DefaultWebsiteChannelThemeColor = "#2563EB"
)

// ValidationCode 标识渠道字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationRoutingTargetInvalid ValidationCode = "CHANNEL_ROUTING_TARGET_INVALID"
	ValidationHomeBlocksInvalid    ValidationCode = "CHANNEL_HOME_BLOCKS_INVALID"
	ValidationAllowedHostInvalid   ValidationCode = "CHANNEL_ALLOWED_HOST_INVALID"
	ValidationKnowledgeBaseInvalid ValidationCode = "CHANNEL_KNOWLEDGE_BASE_INVALID"
)

// ValidationError 表示渠道字段校验失败。
type ValidationError = common.FieldError

// normalizeCreateMessageChannelInput 规范化消息渠道创建输入并校验接待设置。
func normalizeCreateMessageChannelInput(input CreateMessageChannelInput) (CreateMessageChannelInput, map[string]ValidationCode) {
	input.Type = domain.ChannelType(strings.TrimSpace(string(input.Type)))
	normalized, fields := normalizeMessageChannelInput(input.MessageChannelInput)
	input.MessageChannelInput = normalized
	return input, fields
}

// normalizeMessageChannelInput 规范化消息渠道通用输入并校验接待设置。
func normalizeMessageChannelInput(input MessageChannelInput) (MessageChannelInput, map[string]ValidationCode) {
	basics := normalizeMessageChannelBasics(MessageChannelBasicsInput{
		Name:          input.Name,
		Description:   input.Description,
		DefaultLocale: input.DefaultLocale,
	})
	reception, fields := normalizeMessageChannelReception(MessageChannelReceptionInput{
		NewConversationTarget: input.NewConversationTarget,
		FallbackTarget:        input.FallbackTarget,
	})
	return MessageChannelInput{
		Name:                  basics.Name,
		Description:           basics.Description,
		DefaultLocale:         basics.DefaultLocale,
		NewConversationTarget: reception.NewConversationTarget,
		FallbackTarget:        reception.FallbackTarget,
	}, fields
}

// normalizeMessageChannelBasics 规范化渠道基础信息。
func normalizeMessageChannelBasics(input MessageChannelBasicsInput) MessageChannelBasicsInput {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.DefaultLocale = domain.CustomerLocale(strings.TrimSpace(string(input.DefaultLocale)))
	return input
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
		return str.IsUUID(target.ID)
	default:
		return false
	}
}

// normalizeWebsiteChannelChatInterfaceInput 规范化聊天界面输入，主题色统一为大写。
func normalizeWebsiteChannelChatInterfaceInput(input WebsiteChannelChatInterfaceInput) WebsiteChannelChatInterfaceInput {
	input.Title = strings.TrimSpace(input.Title)
	input.GreetingMessage = strings.TrimSpace(input.GreetingMessage)
	input.ThemeColor = strings.ToUpper(strings.TrimSpace(input.ThemeColor))
	return input
}

// normalizeWebsiteChannelHomeInput 规范化首页问候语与链接，并校验卡片包含每种类型各一次。
func normalizeWebsiteChannelHomeInput(input WebsiteChannelHomeInput) (WebsiteChannelHomeInput, map[string]ValidationCode) {
	input.Welcome = strings.TrimSpace(input.Welcome)
	input.Headline = strings.TrimSpace(input.Headline)
	fields := make(map[string]ValidationCode)
	// 首页卡片必须恰好包含每种卡片各一次。
	var seen set.Set[domain.WebsiteHomeBlockType]
	for _, block := range input.Blocks {
		if !seen.Add(block.Type) || !slices.Contains(domain.WebsiteHomeBlockTypes(), block.Type) {
			fields["blocks"] = ValidationHomeBlocksInvalid
		}
	}
	if seen.Len() != len(domain.WebsiteHomeBlockTypes()) {
		fields["blocks"] = ValidationHomeBlocksInvalid
	}
	// 去掉首页链接标题与地址首尾空白。
	links := make([]domain.WebsiteHomeLink, 0, len(input.Links))
	for _, link := range input.Links {
		link.Title = strings.TrimSpace(link.Title)
		link.URL = strings.TrimSpace(link.URL)
		links = append(links, link)
	}
	input.Links = links
	return input, fields
}

// normalizeWebsiteChannelAccessInput 规范化并校验允许使用的网站。
func normalizeWebsiteChannelAccessInput(input WebsiteChannelAccessInput) (WebsiteChannelAccessInput, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	normalized, ok := embedhost.NormalizeAll(input.AllowedHosts)
	if !ok {
		fields["allowedHosts"] = ValidationAllowedHostInvalid
		return input, fields
	}
	input.AllowedHosts = normalized
	return input, fields
}
