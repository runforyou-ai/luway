//go:build server

package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	commerceaction "github.com/runforyou-ai/luway/internal/actions/commerce"
)

const commerceNotificationBodyLimit = 64 << 10

// CommerceNotificationReceiver 验签商业服务的变更通知并触发读取变更源。
type CommerceNotificationReceiver interface {
	Execute(context.Context, *http.Request, []byte) error
}

// receiveCommerceNotification 接收商业服务的变更通知并返回裸 HTTP 状态码。
func (s *Service) receiveCommerceNotification(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, commerceNotificationBodyLimit)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	err = s.commerceNotifications.Execute(c.Request.Context(), c.Request, body)
	switch {
	case err == nil:
		c.Status(http.StatusAccepted)
	case errors.Is(err, commerceaction.ErrNotificationUnauthorized):
		slog.Warn("商业服务通知验签失败", "error", err)
		c.Status(http.StatusUnauthorized)
	default:
		if c.Request.Context().Err() == nil {
			slog.Error("商业服务通知处理失败", "error", err)
			c.Status(http.StatusServiceUnavailable)
		}
	}
}
