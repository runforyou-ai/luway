//go:build server

package channel

import (
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/common/embedhost"
	"github.com/runforyou-ai/luway/internal/domain"
)

// TestNormalizeCreateMessageChannelInput 验证消息渠道创建字段规范化和长度限制。
func TestNormalizeCreateMessageChannelInput(t *testing.T) {
	normalized, fields := normalizeCreateMessageChannelInput(CreateMessageChannelInput{
		Type:                  domain.ChannelTypeWebsite,
		Name:                  "  产品官网  ",
		Description:           "  接收访客咨询  ",
		DefaultLocale:         " zh-CN ",
		NewConversationTarget: RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if len(fields) != 0 {
		t.Fatalf("validation fields = %#v, want empty", fields)
	}
	if normalized.Name != "产品官网" || normalized.Description != "接收访客咨询" || normalized.DefaultLocale != domain.CustomerLocaleChineseSimplified {
		t.Fatalf("unexpected normalized input: %#v", normalized)
	}

	_, fields = normalizeCreateMessageChannelInput(CreateMessageChannelInput{
		Type:                  domain.ChannelTypeTelegram,
		Name:                  "Telegram 客服",
		DefaultLocale:         domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if len(fields) != 0 {
		t.Fatalf("telegram validation fields = %#v, want empty", fields)
	}

	_, fields = normalizeCreateMessageChannelInput(CreateMessageChannelInput{
		Type:                  domain.ChannelTypeWeChatOfficialAccount,
		Name:                  "微信公众号客服",
		DefaultLocale:         domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if len(fields) != 0 {
		t.Fatalf("wechat official account validation fields = %#v, want empty", fields)
	}

	_, fields = normalizeCreateMessageChannelInput(CreateMessageChannelInput{
		Type:                  domain.ChannelTypeWebsite,
		Name:                  "失败去向指定成员",
		DefaultLocale:         domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        RoutingTarget{Type: domain.ChannelRoutingTargetTypeMember, ID: "0199b0d2-6f55-7c11-8e9a-9b7d6c5e4f30"},
	})
	if fields["fallbackTarget"] != ValidationRoutingTargetInvalid {
		t.Fatalf("member fallback validation = %q, want %q", fields["fallbackTarget"], ValidationRoutingTargetInvalid)
	}

	_, fields = normalizeCreateMessageChannelInput(CreateMessageChannelInput{
		Type:          "email",
		Name:          strings.Repeat("鹿", 101),
		Description:   strings.Repeat("行", 2001),
		DefaultLocale: "fr-FR",
	})
	if fields["type"] != ValidationTypeInvalid {
		t.Fatalf("type validation = %q, want %q", fields["type"], ValidationTypeInvalid)
	}
	if fields["name"] != ValidationNameTooLong {
		t.Fatalf("name validation = %q, want %q", fields["name"], ValidationNameTooLong)
	}
	if fields["description"] != ValidationDescriptionTooLong {
		t.Fatalf("description validation = %q, want %q", fields["description"], ValidationDescriptionTooLong)
	}
	if fields["defaultLocale"] != ValidationDefaultLocaleInvalid {
		t.Fatalf("default locale validation = %q, want %q", fields["defaultLocale"], ValidationDefaultLocaleInvalid)
	}
}

// TestNormalizeWebsiteChannelChatInterfaceInput 验证聊天界面字段规范化和校验。
func TestNormalizeWebsiteChannelChatInterfaceInput(t *testing.T) {
	normalized, fields := normalizeWebsiteChannelChatInterfaceInput(WebsiteChannelChatInterfaceInput{
		Title:           " 在线咨询 ",
		GreetingMessage: " 你好 ",
		ThemeColor:      " #16a34a ",
	})
	if len(fields) != 0 {
		t.Fatalf("validation fields = %#v, want empty", fields)
	}
	if normalized.Title != "在线咨询" || normalized.GreetingMessage != "你好" || normalized.ThemeColor != "#16A34A" {
		t.Fatalf("unexpected normalized chat interface: %#v", normalized)
	}

	_, fields = normalizeWebsiteChannelChatInterfaceInput(WebsiteChannelChatInterfaceInput{
		Title:           strings.Repeat("鹿", 101),
		GreetingMessage: strings.Repeat("聊", 501),
		ThemeColor:      "blue",
	})
	if fields["title"] != ValidationChatTitleTooLong || fields["greetingMessage"] != ValidationGreetingTooLong || fields["themeColor"] != ValidationThemeColorInvalid {
		t.Fatalf("unexpected validation fields: %#v", fields)
	}
}

// TestNormalizeWebsiteChannelAccessInput 验证允许网站配置的规范化和数量限制。
func TestNormalizeWebsiteChannelAccessInput(t *testing.T) {
	normalized, fields := normalizeWebsiteChannelAccessInput(WebsiteChannelAccessInput{
		AllowedHosts: []string{" Example.COM ", "*.example.com"},
	})
	if len(fields) != 0 || len(normalized.AllowedHosts) != 2 || normalized.AllowedHosts[0] != "example.com" {
		t.Fatalf("normalized access = %#v, fields = %#v", normalized, fields)
	}

	_, fields = normalizeWebsiteChannelAccessInput(WebsiteChannelAccessInput{AllowedHosts: []string{"not a host"}})
	if fields["allowedHosts"] != ValidationAllowedHostInvalid {
		t.Fatalf("invalid host validation = %#v", fields)
	}

	tooMany := make([]string, embedhost.MaxHosts+1)
	_, fields = normalizeWebsiteChannelAccessInput(WebsiteChannelAccessInput{AllowedHosts: tooMany})
	if fields["allowedHosts"] != ValidationAllowedHostsTooMany {
		t.Fatalf("host count validation = %#v", fields)
	}
}

// TestNormalizeTelegramConnectionInput 验证本地、内网和带路径的回调基础地址可保存。
func TestNormalizeTelegramConnectionInput(t *testing.T) {
	normalized, fields := normalizeTelegramConnectionInput(TelegramChannelConnectionInput{
		BotToken:       " 123456:test_token ",
		WebhookBaseURL: " http://127.0.0.1:34115/app/ ",
	})
	if len(fields) != 0 {
		t.Fatalf("validation fields = %#v, want empty", fields)
	}
	if normalized.BotToken != "123456:test_token" || normalized.WebhookBaseURL != "http://127.0.0.1:34115/app" {
		t.Fatalf("unexpected normalized Telegram connection: %#v", normalized)
	}
	webhookURL, err := telegramWebhookURL(normalized.WebhookBaseURL, "channel-id")
	if err != nil {
		t.Fatal(err)
	}
	if webhookURL != "http://127.0.0.1:34115/app/api/public/telegram-channels/channel-id/webhook" {
		t.Fatalf("webhook URL = %q", webhookURL)
	}
}

// TestNormalizeTelegramConnectionInputRejectsUnsafeBaseURL 验证回调基础地址不接受凭据、查询或片段。
func TestNormalizeTelegramConnectionInputRejectsUnsafeBaseURL(t *testing.T) {
	tests := []string{
		"",
		"ftp://example.com",
		"https://user:password@example.com",
		"https://example.com?tenant=demo",
		"https://example.com#webhook",
		"example.com",
	}
	for _, baseURL := range tests {
		_, fields := normalizeTelegramConnectionInput(TelegramChannelConnectionInput{
			BotToken:       "123456:test_token",
			WebhookBaseURL: baseURL,
		})
		if fields["webhookBaseURL"] != ValidationTelegramBaseURLInvalid {
			t.Fatalf("base URL %q validation = %#v", baseURL, fields)
		}
	}
}
