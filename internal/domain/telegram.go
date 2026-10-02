package domain

// TelegramWebhookStatus 表示 Telegram Webhook 当前连接状态。
type TelegramWebhookStatus string

const (
	TelegramWebhookStatusWaiting TelegramWebhookStatus = "waiting"
	TelegramWebhookStatusNormal  TelegramWebhookStatus = "normal"
)

// TelegramConnectionMode 表示 Telegram 渠道接收消息的接入方式。
type TelegramConnectionMode string

const (
	// TelegramConnectionDirect 表示由本服务向 Telegram 注册 Webhook 直接接收消息。
	TelegramConnectionDirect TelegramConnectionMode = "direct"
	// TelegramConnectionGateway 表示由业务系统接收 Telegram 消息后转发，可附带客户签名身份。
	TelegramConnectionGateway TelegramConnectionMode = "gateway"
)

// Valid 判断接入方式是否受支持。
func (m TelegramConnectionMode) Valid() bool {
	return m == TelegramConnectionDirect || m == TelegramConnectionGateway
}
