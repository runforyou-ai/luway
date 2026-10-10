//go:build server

package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
)

const telegramWebhookBodyLimit = 64 << 10

// TelegramWebhookReceiver 定义公开回调使用的最小应用操作。
type TelegramWebhookReceiver interface {
	Preflight(context.Context, string, string) error
	Execute(context.Context, string, telegramaction.UpdateInput) error
}

// receiveTelegramWebhook 认证 Telegram Update 并返回裸 HTTP 状态码。
func (s *Service) receiveTelegramWebhook(writer http.ResponseWriter, request *http.Request) {
	channelID := request.PathValue("channelID")
	secret := request.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if writeTelegramWebhookError(writer, request, s.telegramWebhook.Preflight(request.Context(), channelID, secret)) {
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, telegramWebhookBodyLimit)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	update, err := telegram.ParseUpdate(body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if update.IgnoredReason != "" {
		slog.InfoContext(request.Context(), "Telegram Update 已按范围忽略", "channel_id", channelID, "update_id", update.ID, "reason", update.IgnoredReason)
	}
	err = s.telegramWebhook.Execute(request.Context(), channelID, telegramaction.UpdateInput{
		Secret: secret, CustomerToken: request.Header.Get(appservice.CustomerTokenHeader), Update: update,
	})
	if writeTelegramWebhookError(writer, request, err) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// writeTelegramWebhookError 映射公开回调错误并返回是否已经响应。
func writeTelegramWebhookError(writer http.ResponseWriter, request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, channelaction.ErrNotFound):
		writer.WriteHeader(http.StatusNotFound)
	case errors.Is(err, telegramaction.ErrWebhookUnauthorized):
		writer.WriteHeader(http.StatusUnauthorized)
	case errors.Is(err, customerchataction.ErrCustomerIdentityInvalid):
		writer.WriteHeader(http.StatusForbidden)
	default:
		if request.Context().Err() == nil {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
	}
	return true
}
