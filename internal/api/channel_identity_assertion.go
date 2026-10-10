//go:build server

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	"github.com/runforyou-ai/luway/internal/appservice"
)

// ChannelIdentityAsserter 定义业务系统设定与撤销渠道身份核验的应用操作。
type ChannelIdentityAsserter interface {
	Verify(ctx context.Context, channelID, externalID, token string) error
	Revoke(ctx context.Context, channelID, externalID, token string) error
}

// assertChannelIdentity 按 Bearer 签名身份设定渠道身份的核验用户。
func (s *Service) assertChannelIdentity(writer http.ResponseWriter, request *http.Request) {
	s.handleChannelIdentityAssertion(writer, request, s.channelIdentityAssertion.Verify)
}

// revokeChannelIdentity 按 Bearer 签名身份撤销渠道身份的核验用户。
func (s *Service) revokeChannelIdentity(writer http.ResponseWriter, request *http.Request) {
	s.handleChannelIdentityAssertion(writer, request, s.channelIdentityAssertion.Revoke)
}

// handleChannelIdentityAssertion 读取路径与 Bearer 签名身份，执行断言操作并返回裸 HTTP 状态码。
func (s *Service) handleChannelIdentityAssertion(writer http.ResponseWriter, request *http.Request, execute func(context.Context, string, string, string) error) {
	token := appservice.BearerToken(request.Header.Get("Authorization"))
	if token == "" {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	channelID := request.PathValue("channelID")
	err := execute(request.Context(), channelID, request.PathValue("externalID"), token)
	var validation *conversationaction.ValidationError
	switch {
	case err == nil:
		writer.WriteHeader(http.StatusNoContent)
	case errors.As(err, &validation):
		writer.WriteHeader(http.StatusBadRequest)
	case errors.Is(err, channelaction.ErrNotFound):
		writer.WriteHeader(http.StatusNotFound)
	case errors.Is(err, customerchataction.ErrCustomerIdentityInvalid):
		writer.WriteHeader(http.StatusUnauthorized)
	case request.Context().Err() != nil:
	default:
		slog.ErrorContext(request.Context(), "处理渠道身份断言失败", "channel_id", channelID, "error", err)
		writer.WriteHeader(http.StatusInternalServerError)
	}
}
