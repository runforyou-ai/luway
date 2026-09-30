//go:build server

package api

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/cervi/internal/appservice"
)

// DeviceRunAttachmentReader 读取设备运行所属会话中的附件内容。
type DeviceRunAttachmentReader interface {
	// ReadDeviceRunAttachment 校验请求来自持有该运行有效租约的本人未撤销设备，并返回指定附件消息的文件内容。
	ReadDeviceRunAttachment(ctx context.Context, meta appservice.RequestMeta, runID, messageID string) ([]byte, error)
}

// WithDeviceRunAttachments 注入设备运行的附件读取。
func WithDeviceRunAttachments(reader DeviceRunAttachmentReader) ServiceOption {
	return func(service *Service) {
		service.deviceAttachments = reader
	}
}

// readDeviceRunAttachment 以原始字节输出设备运行所属会话中指定附件消息的文件内容。
func (s *Service) readDeviceRunAttachment(c *gin.Context) {
	content, err := s.deviceAttachments.ReadDeviceRunAttachment(c.Request.Context(), requestMeta(c), c.Param("runID"), c.Param("messageID"))
	if writeApplicationError(c, err) {
		return
	}
	c.Data(http.StatusOK, "application/octet-stream", content)
}
