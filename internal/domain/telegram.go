package domain

// TelegramWebhookStatus 表示 Telegram Webhook 当前连接状态。
type TelegramWebhookStatus string

const (
	TelegramWebhookStatusWaiting TelegramWebhookStatus = "waiting"
	TelegramWebhookStatusNormal  TelegramWebhookStatus = "normal"
)
