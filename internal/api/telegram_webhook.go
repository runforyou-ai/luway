//go:build server

package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
)

const telegramWebhookBodyLimit = 64 << 10

// TelegramWebhookReceiver 定义公开回调使用的最小应用操作。
type TelegramWebhookReceiver interface {
	Preflight(context.Context, string, string) error
	Execute(context.Context, string, customerchataction.TelegramWebhookInput) error
}

// receiveTelegramWebhook 认证 Telegram Update 并返回裸 HTTP 状态码。
func (s *Service) receiveTelegramWebhook(c *gin.Context) {
	channelID := c.Param("channelID")
	secret := c.GetHeader("X-Telegram-Bot-Api-Secret-Token")
	if writeTelegramWebhookError(c, s.telegramWebhook.Preflight(c.Request.Context(), channelID, secret)) {
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, telegramWebhookBodyLimit)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	update, err := telegram.ParseUpdate(body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if update.IgnoredReason != "" {
		slog.Info("Telegram Update 已按范围忽略", "channel_id", channelID, "update_id", update.ID, "reason", update.IgnoredReason)
	}
	err = s.telegramWebhook.Execute(c.Request.Context(), channelID, customerchataction.TelegramWebhookInput{
		Secret: secret, UpdateID: update.ID, MyChatMember: update.MyChatMember, Message: update.Message,
	})
	if writeTelegramWebhookError(c, err) {
		return
	}
	c.Status(http.StatusNoContent)
}

// writeTelegramWebhookError 映射公开回调错误并返回是否已经响应。
func writeTelegramWebhookError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, channelaction.ErrNotFound):
		c.Status(http.StatusNotFound)
	case errors.Is(err, customerchataction.ErrTelegramWebhookUnauthorized):
		c.Status(http.StatusUnauthorized)
	default:
		if c.Request.Context().Err() == nil {
			c.Status(http.StatusServiceUnavailable)
		}
	}
	return true
}
