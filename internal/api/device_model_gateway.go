//go:build server

package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/integration/modelgateway"
)

// DeviceModelGateway 校验设备模型网关请求并返回运行锁定的对话模型组件工厂。
type DeviceModelGateway interface {
	// DeviceRunModels 校验请求来自持有该运行有效租约的本人未撤销设备，并返回经统一调用入口记为该运行调用的模型组件工厂。
	DeviceRunModels(ctx context.Context, meta appservice.RequestMeta, runID string) (agentruntime.ModelFactory, error)
}

// WithDeviceModelGateway 注入设备模型网关的请求授权。
func WithDeviceModelGateway(gateway DeviceModelGateway) ServiceOption {
	return func(service *Service) {
		service.deviceModels = gateway
	}
}

// serveDeviceModel 执行设备运行的模型请求：按运行锁定的模型经统一调用入口请求上游，并把输出逐帧返回设备。
func (s *Service) serveDeviceModel(c *gin.Context) {
	meta := requestMeta(c)
	models, err := s.deviceModels.DeviceRunModels(c.Request.Context(), meta, c.Param("runID"))
	if writeApplicationError(c, err) {
		return
	}
	request, err := modelgateway.DecodeRequest(http.MaxBytesReader(c.Writer, c.Request.Body, modelgateway.MaxRequestBytes))
	if err != nil {
		writeApplicationError(c, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil))
		return
	}
	if err := modelgateway.Serve(c.Request.Context(), c.Writer, models, request); err != nil {
		slog.Warn("设备模型请求无效", "agent_run_id", c.Param("runID"), "error", err)
		writeApplicationError(c, appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil))
	}
}
