//go:build server

package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
)

// WechatMessageReceiver 接收公众号推送到消息接收地址的请求。
type WechatMessageReceiver interface {
	VerifyKeyServer(context.Context, string, wechat.PushQuery) error
	ReceiveKeyMessage(context.Context, string, wechat.PushQuery, []byte) error
	ReceiveAuthorizationMessage(context.Context, string, wechat.PushQuery, []byte) ([]byte, error)
}

// wechatPushQuery 读取推送请求的签名查询参数。
func wechatPushQuery(request *http.Request) wechat.PushQuery {
	return wechat.PushQuery{
		Timestamp: request.URL.Query().Get("timestamp"), Nonce: request.URL.Query().Get("nonce"), Signature: request.URL.Query().Get("signature"), MsgSignature: request.URL.Query().Get("msg_signature"),
	}
}

// verifyWechatKeyServer 响应微信验证密钥接入渠道服务器地址的请求，校验通过后原样返回 echostr。
func (s *Service) verifyWechatKeyServer(writer http.ResponseWriter, request *http.Request) {
	err := s.wechatMessages.VerifyKeyServer(request.Context(), request.PathValue("channelID"), wechatPushQuery(request))
	if writeWechatMessageError(writer, request, err) {
		return
	}
	writeText(writer, http.StatusOK, request.URL.Query().Get("echostr"))
}

// receiveWechatKeyMessage 接收密钥接入渠道的公众号消息推送。
func (s *Service) receiveWechatKeyMessage(writer http.ResponseWriter, request *http.Request) {
	body, ok := readWechatPush(writer, request)
	if !ok {
		return
	}
	err := s.wechatMessages.ReceiveKeyMessage(request.Context(), request.PathValue("channelID"), wechatPushQuery(request), body)
	if writeWechatMessageError(writer, request, err) {
		return
	}
	writeText(writer, http.StatusOK, "success")
}

// receiveWechatAuthorizationMessage 接收第三方平台转发的授权公众号消息推送，有被动回复时以回复为响应正文。
func (s *Service) receiveWechatAuthorizationMessage(writer http.ResponseWriter, request *http.Request) {
	body, ok := readWechatPush(writer, request)
	if !ok {
		return
	}
	reply, err := s.wechatMessages.ReceiveAuthorizationMessage(request.Context(), request.PathValue("appID"), wechatPushQuery(request), body)
	if writeWechatMessageError(writer, request, err) {
		return
	}
	if reply != nil {
		writer.Header().Set("Content-Type", "text/xml; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(reply)
		return
	}
	writeText(writer, http.StatusOK, "success")
}

// readWechatPush 读取限定大小的推送请求体，读取失败时响应 400。
func readWechatPush(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, wechatPushBodyLimit)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// writeWechatMessageError 映射公众号消息推送错误并返回是否已经响应。
func writeWechatMessageError(writer http.ResponseWriter, request *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, channelaction.ErrNotFound), errors.Is(err, wechataction.ErrPlatformNotConfigured):
		writer.WriteHeader(http.StatusNotFound)
	case errors.Is(err, wechataction.ErrPushUnauthorized):
		slog.WarnContext(request.Context(), "公众号消息推送校验失败", "path", request.Pattern, "error", err)
		writer.WriteHeader(http.StatusUnauthorized)
	default:
		if request.Context().Err() == nil {
			slog.ErrorContext(request.Context(), "公众号消息推送处理失败", "path", request.Pattern, "error", err)
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
	}
	return true
}
