//go:build server

// Package api 将应用契约公开为企业服务端 HTTP API。
package api

import (
	"log/slog"
	"net/http"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/support"
)

// Service 是企业服务端对外提供的 HTTP 适配器，路由路径相对服务端 /api。
type Service struct {
	backend                  appservice.Backend
	computers                appservice.ComputerBackend
	websiteVisitor           appservice.WebsiteVisitorService
	telegramWebhook          TelegramWebhookReceiver
	channelIdentityAssertion ChannelIdentityAsserter
	wechatPlatformEvents     WechatPlatformEventReceiver
	wechatMessages           WechatMessageReceiver
	// wechatAuthorizationPage 与 wechatAuthorizationComplete 提供公众号授权发起页与授权回跳。
	wechatAuthorizationPage     WechatAuthorizationPageResolver
	wechatAuthorizationComplete WechatAuthorizationCompleter
	secureVisitorCookie         func() bool
	// routes 是其他模块在核心路由之后注册的路由。
	routes []func(handle func(string, http.HandlerFunc))
	mux    *http.ServeMux
}

// ServiceOption 配置企业服务端 HTTP API 的独立能力。
type ServiceOption func(*Service)

// WithWebsiteVisitor 注入网站访客应用服务与访客 Cookie 是否带 Secure 属性的判断；secureCookie 在部署地址为 HTTPS 时返回真。
func WithWebsiteVisitor(visitor appservice.WebsiteVisitorService, secureCookie func() bool) ServiceOption {
	return func(service *Service) {
		service.websiteVisitor = visitor
		service.secureVisitorCookie = secureCookie
	}
}

// WithComputers 注入执行器以电脑身份调用的服务端契约。
func WithComputers(backend appservice.ComputerBackend) ServiceOption {
	return func(service *Service) {
		service.computers = backend
	}
}

// WithTelegramWebhook 注入 Telegram 公开回调接收能力。
func WithTelegramWebhook(receiver TelegramWebhookReceiver) ServiceOption {
	return func(service *Service) {
		service.telegramWebhook = receiver
	}
}

// WithChannelIdentityAssertion 注入业务系统设定与撤销渠道身份核验的能力。
func WithChannelIdentityAssertion(asserter ChannelIdentityAsserter) ServiceOption {
	return func(service *Service) {
		service.channelIdentityAssertion = asserter
	}
}

// WithWechatPlatformEvents 注入微信开放平台通知接收能力。
func WithWechatPlatformEvents(receiver WechatPlatformEventReceiver) ServiceOption {
	return func(service *Service) {
		service.wechatPlatformEvents = receiver
	}
}

// WithWechatMessages 注入公众号消息推送接收能力。
func WithWechatMessages(receiver WechatMessageReceiver) ServiceOption {
	return func(service *Service) {
		service.wechatMessages = receiver
	}
}

// WithWechatAuthorization 注入公众号授权发起页与授权回跳处理能力。
func WithWechatAuthorization(page WechatAuthorizationPageResolver, complete WechatAuthorizationCompleter) ServiceOption {
	return func(service *Service) {
		service.wechatAuthorizationPage = page
		service.wechatAuthorizationComplete = complete
	}
}

// WithRoutes 注入其他模块的路由注册函数，其路由与核心路由共用请求处理与日志作用域。
func WithRoutes(register func(handle func(string, http.HandlerFunc))) ServiceOption {
	return func(service *Service) {
		service.routes = append(service.routes, register)
	}
}

// NewService 创建企业服务端 HTTP API。
func NewService(backend appservice.Backend, options ...ServiceOption) *Service {
	service := &Service{backend: backend, mux: http.NewServeMux()}
	for _, option := range options {
		option(service)
	}
	service.registerGeneratedRoutes(service.handle)
	for _, register := range service.routes {
		register(service.handle)
	}
	if service.computers != nil {
		service.registerGeneratedComputerRoutes(service.handle)
	}
	service.registerWebsiteVisitorRoutes()
	// 注册通过渠道密钥认证的 Telegram 回调。
	if service.telegramWebhook != nil {
		service.handle("POST /public/telegram-channels/{channelID}/webhook", service.receiveTelegramWebhook)
	}
	// 注册以客户身份密钥签名认证的渠道身份断言。
	if service.channelIdentityAssertion != nil {
		service.handle("PUT /public/channels/{channelID}/identities/{externalID}/verification", service.assertChannelIdentity)
		service.handle("DELETE /public/channels/{channelID}/identities/{externalID}/verification", service.revokeChannelIdentity)
	}
	// 注册以第三方平台消息校验 Token 签名认证的微信开放平台通知。
	if service.wechatPlatformEvents != nil {
		service.handle("POST /public/wechat/events", service.receiveWechatPlatformEvent)
	}
	// 注册以渠道消息校验 Token 或第三方平台凭据签名认证的公众号消息推送。
	if service.wechatMessages != nil {
		service.handle("GET /public/wechat/channels/{channelID}/messages", service.verifyWechatKeyServer)
		service.handle("POST /public/wechat/channels/{channelID}/messages", service.receiveWechatKeyMessage)
		service.handle("POST /public/wechat/accounts/{appID}/messages", service.receiveWechatAuthorizationMessage)
	}
	// 注册以随机授权标识定位授权意图的公众号授权发起页与授权回跳。
	if service.wechatAuthorizationPage != nil {
		service.handle("GET /public/wechat/authorizations/{state}", service.showWechatAuthorization)
		service.handle("GET /public/wechat/authorizations/{state}/callback", service.completeWechatAuthorization)
	}
	return service
}

// handle 注册路由，处理请求时以路由作为日志作用域中的业务入口，经业务入口的请求由分发层改为方法名。
func (s *Service) handle(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, func(writer http.ResponseWriter, request *http.Request) {
		handler(writer, request.WithContext(logscope.WithOperation(request.Context(), pattern)))
	})
}

// ServeHTTP 分派 HTTP 请求；处理请求时发生的 panic 记录为带调用栈的错误并返回 500，未匹配路由时以请求方法作为业务入口。
func (s *Service) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == http.ErrAbortHandler { //nolint:errorlint // net/http 以原值 panic 中止响应。
				panic(recovered)
			}
			slog.ErrorContext(request.Context(), "处理接口请求时发生 panic", "error", support.NewPanicError(recovered))
			writer.WriteHeader(http.StatusInternalServerError)
		}
	}()
	s.mux.ServeHTTP(writer, request.WithContext(logscope.WithOperation(request.Context(), request.Method)))
}

// writeText 按状态码写入纯文本响应。
func writeText(writer http.ResponseWriter, status int, text string) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(text))
}
