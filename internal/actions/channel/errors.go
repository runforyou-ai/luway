//go:build server

package channel

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定消息渠道。
	ErrNotFound = errors.New("message channel not found")
	// ErrTelegramBotReuseConfirmationRequired 表示保存前需要确认复用其他渠道的 Bot。
	ErrTelegramBotReuseConfirmationRequired = errors.New("Telegram bot reuse confirmation required")
	// ErrTelegramGatewayModeRequired 表示操作只适用于业务系统转发接入的 Telegram 渠道。
	ErrTelegramGatewayModeRequired = errors.New("Telegram gateway mode required")
)
