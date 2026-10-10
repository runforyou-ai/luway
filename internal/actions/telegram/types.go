//go:build server

// Package telegram 实现 Telegram 渠道的连接配置、Webhook 接收与平台适配。
package telegram

import (
	"errors"
	"net/url"
	"strings"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
)

var (
	// ErrBotReuseConfirmationRequired 表示保存前需要确认复用其他渠道的机器人。
	ErrBotReuseConfirmationRequired = errors.New("Telegram bot reuse confirmation required")
	// ErrGatewayModeRequired 表示操作只适用于业务系统转发接入的 Telegram 渠道。
	ErrGatewayModeRequired = errors.New("Telegram gateway mode required")
)

// Telegram 连接配置的字段校验码。
const (
	ValidationBaseURLInvalid channelaction.ValidationCode = "TELEGRAM_WEBHOOK_BASE_URL_INVALID"
)

const (
	// maxWebhookBaseURLLength 是 Webhook 基础地址的最大长度。
	maxWebhookBaseURLLength = 2048
)

// ConnectionInput 定义 Telegram 连接可编辑字段。
type ConnectionInput struct {
	ConnectionMode  domain.TelegramConnectionMode
	BotToken        string
	WebhookBaseURL  string
	ConfirmBotReuse bool
}

// ConnectionTestInput 定义 Telegram 草稿连接测试字段。
type ConnectionTestInput struct {
	BotToken string
}

// SettingRecord 定义 Telegram 机器人和 Webhook 传输字段。
type SettingRecord struct {
	ConnectionMode     string     `json:"connectionMode"`
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

// normalizeConnectionInput 规范化 Telegram 保存输入，并校验与规范化 Webhook 基础地址。
func normalizeConnectionInput(input ConnectionInput) (ConnectionInput, map[string]channelaction.ValidationCode) {
	input.BotToken = strings.TrimSpace(input.BotToken)
	fields := make(map[string]channelaction.ValidationCode)
	input.WebhookBaseURL = strings.TrimSpace(input.WebhookBaseURL)
	if len(input.WebhookBaseURL) > maxWebhookBaseURLLength {
		fields["webhookBaseURL"] = ValidationBaseURLInvalid
		return input, fields
	}
	parsed, err := url.ParseRequestURI(input.WebhookBaseURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		fields["webhookBaseURL"] = ValidationBaseURLInvalid
		return input, fields
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	input.WebhookBaseURL = parsed.String()
	return input, fields
}

// buildWebhookURL 使用已保存基础地址生成 Telegram 回调地址。
func buildWebhookURL(baseURL, channelID string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/public/telegram-channels/" + channelID + "/webhook"
	parsed.RawPath = ""
	return parsed.String(), nil
}
