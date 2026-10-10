//go:build server

package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
)

const wechatPushBodyLimit = 64 << 10

// WechatPlatformEventReceiver 接收微信开放平台推送到授权事件接收地址的通知。
type WechatPlatformEventReceiver interface {
	Execute(context.Context, wechat.PushQuery, []byte) error
}

// receiveWechatPlatformEvent 接收开放平台通知，处理完成后按微信要求回复 success。
func (s *Service) receiveWechatPlatformEvent(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, wechatPushBodyLimit)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	err = s.wechatPlatformEvents.Execute(request.Context(), wechat.PushQuery{
		Timestamp: request.URL.Query().Get("timestamp"), Nonce: request.URL.Query().Get("nonce"), MsgSignature: request.URL.Query().Get("msg_signature"),
	}, body)
	switch {
	case err == nil:
		writeText(writer, http.StatusOK, "success")
	case errors.Is(err, wechataction.ErrPlatformNotConfigured):
		writer.WriteHeader(http.StatusNotFound)
	case errors.Is(err, wechataction.ErrPushUnauthorized):
		slog.WarnContext(request.Context(), "微信开放平台通知校验失败", "error", err)
		writer.WriteHeader(http.StatusUnauthorized)
	default:
		if request.Context().Err() == nil {
			slog.ErrorContext(request.Context(), "微信开放平台通知处理失败", "error", err)
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
	}
}
