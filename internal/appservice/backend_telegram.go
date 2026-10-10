package appservice

import "context"

// TelegramBackend 定义 Telegram 渠道连接的业务调用。
type TelegramBackend interface {
	// GetTelegramChannel 返回 Telegram 渠道详情。
	//appservice:route GET /channels/{channelID:uuid}/telegram perm=customer_service.manage
	GetTelegramChannel(context.Context, RequestMeta, string) (TelegramChannel, error)
	// TestTelegramChannelConnection 测试 Telegram 草稿 Token。
	//appservice:route POST /channels/{channelID:uuid}/telegram/connection/test perm=customer_service.manage
	TestTelegramChannelConnection(context.Context, RequestMeta, string, TelegramChannelConnectionTestInput) error
	// SaveTelegramChannelConnection 保存 Telegram 机器人和 Webhook 设置。
	//appservice:route PUT /channels/{channelID:uuid}/telegram/connection perm=customer_service.manage
	SaveTelegramChannelConnection(context.Context, RequestMeta, string, TelegramChannelConnectionInput) (TelegramChannel, error)
	// RegenerateTelegramGatewaySecret 重新生成业务系统转发 Telegram 消息使用的转发密钥。
	//appservice:route POST /channels/{channelID:uuid}/telegram/connection/gateway-secret perm=customer_service.manage
	RegenerateTelegramGatewaySecret(context.Context, RequestMeta, string) (TelegramChannel, error)
}
