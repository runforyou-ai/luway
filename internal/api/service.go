//go:build server

// Package api 将统一应用服务公开为企业服务端 HTTP API。
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/i18n"
)

// WebsiteVisitorRealtime 输出已通过访客授权的网站访客实时事件流。
type WebsiteVisitorRealtime interface {
	// ServeVisitor 按渠道与访客外部编号解析受众并输出事件流，直到事件流结束；登录用户的事件流在签名身份过期时结束。
	ServeVisitor(writer http.ResponseWriter, request *http.Request, meta appservice.WebsiteVisitorMeta, channelID, externalID string)
}

// Service 是企业服务端对外提供的 Gin HTTP 适配器。
type Service struct {
	application         *appservice.Service
	deviceRuns          appservice.DeviceRunBackend
	deviceModels        DeviceModelAuthorizer
	deviceAttachments   DeviceRunAttachmentReader
	websiteVisitor      *appservice.WebsiteVisitorService
	visitorRealtime     WebsiteVisitorRealtime
	telegramWebhook     TelegramWebhookReceiver
	trustForwardedProto bool
	// visitorCountryHeader 是可信反向代理写入访客国家代码的请求头名称，为空时不采集。
	visitorCountryHeader string
	router               *gin.Engine
}

// ServiceOption 配置企业服务端 HTTP API 的独立能力。
type ServiceOption func(*Service)

// WithWebsiteVisitor 注入网站访客应用服务与访客国家代码请求头名称。
func WithWebsiteVisitor(visitor *appservice.WebsiteVisitorService, trustForwardedProto bool, visitorCountryHeader string) ServiceOption {
	return func(service *Service) {
		service.websiteVisitor = visitor
		service.trustForwardedProto = trustForwardedProto
		service.visitorCountryHeader = visitorCountryHeader
	}
}

// WithWebsiteVisitorRealtime 注入网站访客实时事件流。
func WithWebsiteVisitorRealtime(realtime WebsiteVisitorRealtime) ServiceOption {
	return func(service *Service) {
		service.visitorRealtime = realtime
	}
}

// WithDeviceRuns 注入本机设备执行 Agent 运行的运行期调用。
func WithDeviceRuns(backend appservice.DeviceRunBackend) ServiceOption {
	return func(service *Service) {
		service.deviceRuns = backend
	}
}

// WithTelegramWebhook 注入 Telegram 公开回调接收能力。
func WithTelegramWebhook(receiver TelegramWebhookReceiver) ServiceOption {
	return func(service *Service) {
		service.telegramWebhook = receiver
	}
}

// NewService 创建企业服务端 HTTP API。
func NewService(application *appservice.Service, options ...ServiceOption) *Service {
	service := &Service{application: application}
	for _, option := range options {
		option(service)
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	service.registerGeneratedRoutes(router)
	if service.deviceRuns != nil {
		service.registerGeneratedDeviceRunRoutes(router)
	}
	// 注册设备运行的模型代理，请求路径在代理入口之后按上游模型服务的接口拼接。
	if service.deviceModels != nil {
		router.POST("/agent-runs/:runID/model/*path", service.proxyDeviceModel)
	}
	// 注册设备运行读取会话附件的原始内容入口。
	if service.deviceAttachments != nil {
		router.GET("/agent-runs/:runID/attachments/:messageID", service.readDeviceRunAttachment)
	}
	// 创建企业管理员并返回登录令牌。
	router.POST("/install", func(c *gin.Context) {
		var input appservice.InstallWorkspaceInput
		if !bindJSON(c, &input) {
			return
		}
		auth, err := service.application.InstallWorkspace(c.Request.Context(), requestMeta(c), input)
		if writeApplicationError(c, err) {
			return
		}
		c.JSON(http.StatusCreated, auth)
	})
	service.registerWebsiteVisitorRoutes(router)
	// 注册通过渠道密钥认证的 Telegram 回调。
	if service.telegramWebhook != nil {
		router.POST("/public/telegram-channels/:channelID/webhook", service.receiveTelegramWebhook)
	}

	service.router = router
	return service
}

// ServeHTTP 将 HTTP 请求交给 Gin 路由处理。
func (s *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.router.ServeHTTP(writer, request)
}

// optionalEnum 把非空查询值转换成对应枚举指针，空值返回 nil。
func optionalEnum[T ~string](value string) *T {
	if value == "" {
		return nil
	}
	typed := T(value)
	return &typed
}

// enumList 把重复查询参数转换成枚举切片，缺省时返回空切片。
func enumList[T ~string](values []string) []T {
	list := make([]T, 0, len(values))
	for _, value := range values {
		list = append(list, T(value))
	}
	return list
}

// requestMeta 从请求头提取令牌、目标工作区、语言和设备编号，构造应用服务请求元数据。
func requestMeta(c *gin.Context) appservice.RequestMeta {
	return appservice.RequestMetaFromHTTP(c.Request.Header)
}

// bindJSON 绑定 JSON 请求体，失败时写入校验错误响应并返回 false。
func bindJSON(c *gin.Context, output any) bool {
	if err := c.ShouldBindJSON(output); err != nil {
		writeApplicationError(c, appservice.InvalidError(requestMeta(c), i18n.ErrorValidationFailed, nil))
		return false
	}
	return true
}

// positiveQueryInteger 解析正整数查询参数，缺省时返回默认值，非法时写入校验错误响应。
func positiveQueryInteger(c *gin.Context, name string, defaultValue int) (int, bool) {
	value := c.Query(name)
	if value == "" {
		return defaultValue, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		writeApplicationError(c, appservice.InvalidError(requestMeta(c), i18n.ErrorValidationFailed, map[string]i18n.Key{name: i18n.FieldQueryPositiveInteger}))
		return 0, false
	}
	return parsed, true
}

// writeResult 无错误时按状态码写入 JSON 结果，否则写入错误响应。
func writeResult(c *gin.Context, status int, result any, err error) {
	if writeApplicationError(c, err) {
		return
	}
	c.JSON(status, result)
}

// writeEmpty 无错误时返回 204 空响应，否则写入错误响应。
func writeEmpty(c *gin.Context, err error) {
	if writeApplicationError(c, err) {
		return
	}
	c.Status(http.StatusNoContent)
}

// writeApplicationError 把应用服务错误写成结构化错误响应，返回是否已处理错误。
func writeApplicationError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if c.Request.Context().Err() != nil {
		return true
	}
	if applicationError, ok := errors.AsType[*appservice.Error](err); ok {
		appservice.WriteHTTPError(c.Writer, c.Request, applicationError)
		return true
	}
	slog.Warn("应用服务调用失败", "error", err)
	appservice.WriteHTTPError(c.Writer, c.Request, appservice.FailedError(requestMeta(c), i18n.ErrorInternal))
	return true
}
