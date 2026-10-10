//go:build server

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
	"github.com/runforyou-ai/luway/internal/httpcodec"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/support/random"
)

const (
	websiteVisitorHeader    = appservice.WebsiteVisitorTokenHeader
	websiteCustomerHeader   = appservice.CustomerTokenHeader
	websiteVisitorTokenSize = 32
	websiteVisitorBodyLimit = 16 * 1024
	websiteVisitorCookieAge = 365 * 24 * 60 * 60
)

// registerWebsiteVisitorRoutes 注册网站 Messenger 公开路由；路径的其他方法返回带允许方法的公开错误体。
func (s *Service) registerWebsiteVisitorRoutes() {
	if s.websiteVisitor == nil {
		return
	}
	// 注册方法路由，并为同一路径的其他方法注册显式方法错误。
	handle := func(pattern string, handler http.HandlerFunc) {
		method, path, _ := strings.Cut(pattern, " ")
		s.handle(pattern, handler)
		s.handle(path, websiteVisitorMethodNotAllowed(method))
	}
	s.registerGeneratedWebsiteVisitorRoutes(handle)
	const prefix = "/public/website-channels/{channelID}"
	handle(http.MethodGet+" "+prefix+"/messenger", s.initializeWebsiteMessenger)
	handle(http.MethodPost+" "+prefix+"/resume", s.resumeWebsiteVisitor)
}

// authorizeWebsiteVisitor 解析需要访客身份的公开请求并返回调用元信息与访客外部编号：禁止缓存；携带签名身份时验签并以登录用户外部编号访问，否则按 Header、Cookie 读取访客 Token；身份缺失、非法或失效时写入公开错误体并返回 false。
func (s *Service) authorizeWebsiteVisitor(writer http.ResponseWriter, request *http.Request) (appservice.WebsiteVisitorMeta, string, bool) {
	writer.Header().Set("Cache-Control", "no-store")
	if customerToken := strings.TrimSpace(request.Header.Get(websiteCustomerHeader)); customerToken != "" {
		return s.verifyWebsiteCustomer(writer, request, customerToken)
	}
	token, valid := readWebsiteVisitorToken(request, request.PathValue("channelID"))
	if !valid {
		httpcodec.WriteApplicationError(writer, request, invalidWebsiteVisitorTokenError(request))
		return appservice.WebsiteVisitorMeta{}, "", false
	}
	meta := s.websiteVisitorMeta(request)
	meta.Token = token
	return meta, customeridentity.AnonymousExternalID(token), true
}

// publicWebsiteVisitorMeta 为无需访客身份的公开请求禁止缓存并返回调用元信息。
func (s *Service) publicWebsiteVisitorMeta(writer http.ResponseWriter, request *http.Request) appservice.WebsiteVisitorMeta {
	writer.Header().Set("Cache-Control", "no-store")
	return s.websiteVisitorMeta(request)
}

// verifyWebsiteCustomer 校验签名身份并返回带登录用户的调用元信息与其外部编号，失败时写入错误响应并返回 false。
func (s *Service) verifyWebsiteCustomer(writer http.ResponseWriter, request *http.Request, customerToken string) (appservice.WebsiteVisitorMeta, string, bool) {
	meta := s.websiteVisitorMeta(request)
	customer, err := s.websiteVisitor.VerifyCustomer(request.Context(), meta, request.PathValue("channelID"), customerToken)
	if httpcodec.WriteApplicationError(writer, request, err) {
		return appservice.WebsiteVisitorMeta{}, "", false
	}
	meta.Customer, meta.CustomerToken = &customer, customerToken
	return meta, customeridentity.CustomerExternalID(customer.UserID), true
}

// initializeWebsiteMessenger 返回会话列表：携带签名身份时以登录用户初始化，不签发也不轮换访客 Token；匿名访客签发或恢复访客 Token，rotate=1 时忽略已有 Token 重新签发。
func (s *Service) initializeWebsiteMessenger(writer http.ResponseWriter, request *http.Request) {
	meta := s.publicWebsiteVisitorMeta(writer, request)
	channelID := request.PathValue("channelID")
	var externalID, token string
	issued := false
	if customerToken := strings.TrimSpace(request.Header.Get(websiteCustomerHeader)); customerToken != "" {
		var ok bool
		meta, externalID, ok = s.verifyWebsiteCustomer(writer, request, customerToken)
		if !ok {
			return
		}
	} else {
		var valid bool
		token, valid = readWebsiteVisitorToken(request, channelID)
		if !valid || request.URL.Query().Get("rotate") == "1" {
			token, issued = random.Hex(websiteVisitorTokenSize/2), true
		}
		externalID = customeridentity.AnonymousExternalID(token)
	}
	directory, err := s.websiteVisitor.ListConversations(request.Context(), meta, channelID, externalID)
	if httpcodec.WriteApplicationError(writer, request, err) {
		return
	}
	if issued {
		s.setWebsiteVisitorCookie(writer, channelID, token)
	}
	writeWebsiteVisitorResult(writer, request, http.StatusOK, appservice.WebsiteVisitorMessenger{
		VisitorToken: token, Reception: directory.Reception, ReceptionRefreshAt: directory.ReceptionRefreshAt, Conversations: directory.Conversations,
	})
}

// setWebsiteVisitorCookie 设置渠道级长期访客 Cookie。
func (s *Service) setWebsiteVisitorCookie(writer http.ResponseWriter, channelID, token string) {
	secure := s.secureVisitorCookie()
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(writer, &http.Cookie{
		Name: websiteVisitorCookieName(channelID), Value: token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: sameSite, MaxAge: websiteVisitorCookieAge,
	})
}

// resumeWebsiteVisitor 用邮件中的回访令牌换取匿名访客令牌，写入与挂件相同的渠道访客 Cookie，同一浏览器原有的匿名身份被替换。
func (s *Service) resumeWebsiteVisitor(writer http.ResponseWriter, request *http.Request) {
	meta := s.publicWebsiteVisitorMeta(writer, request)
	var input appservice.WebsiteVisitorResumeInput
	if !bindWebsiteVisitorJSON(writer, request, &input) {
		return
	}
	channelID := request.PathValue("channelID")
	result, err := s.websiteVisitor.ResumeVisitor(request.Context(), meta, channelID, input)
	if httpcodec.WriteApplicationError(writer, request, err) {
		return
	}
	s.setWebsiteVisitorCookie(writer, channelID, result.VisitorToken)
	writeWebsiteVisitorResult(writer, request, http.StatusOK, result)
}

// bindWebsiteVisitorJSON 限制并严格解析公开 JSON 请求体。
func bindWebsiteVisitorJSON(writer http.ResponseWriter, request *http.Request, output any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, websiteVisitorBodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			httpcodec.WriteApplicationError(writer, request, appservice.WebsiteVisitorError(websiteVisitorLocale(request), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil).WithStatus(http.StatusRequestEntityTooLarge))
			return false
		}
		httpcodec.WriteApplicationError(writer, request, appservice.WebsiteVisitorError(websiteVisitorLocale(request), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpcodec.WriteApplicationError(writer, request, appservice.WebsiteVisitorError(websiteVisitorLocale(request), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil))
		return false
	}
	return true
}

// readWebsiteVisitorToken 按 Header、Cookie 顺序读取访客 Token。
func readWebsiteVisitorToken(request *http.Request, channelID string) (string, bool) {
	if value := strings.TrimSpace(request.Header.Get(websiteVisitorHeader)); value != "" {
		return value, customeridentity.ValidAnonymousToken(value)
	}
	cookie, err := request.Cookie(websiteVisitorCookieName(channelID))
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(cookie.Value)
	return value, customeridentity.ValidAnonymousToken(value)
}

// websiteVisitorCookieName 返回渠道级访客 Cookie 名称。
func websiteVisitorCookieName(channelID string) string {
	return "visitor_" + channelID
}

// websiteVisitorMeta 构造不含访客身份的调用元信息，含对客语言、浏览器标识与可信代理提供的国家代码。
func (s *Service) websiteVisitorMeta(request *http.Request) appservice.WebsiteVisitorMeta {
	return appservice.WebsiteVisitorMeta{Locale: websiteVisitorLocale(request), UserAgent: request.Header.Get("User-Agent"), Country: appservice.ClientCountry(request.Context())}
}

// websiteVisitorLocale 按请求语言偏好返回访客使用的对客语言。
func websiteVisitorLocale(request *http.Request) appservice.CustomerLocale {
	return i18n.PreferredCustomerLocale(request.Header.Get("Accept-Language"))
}

// invalidWebsiteVisitorTokenError 返回缺失或非法访客 Token 错误。
func invalidWebsiteVisitorTokenError(request *http.Request) *appservice.Error {
	return appservice.WebsiteVisitorError(websiteVisitorLocale(request), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, map[string]i18n.Key{"visitorToken": i18n.VisitorErrorRequestInvalid})
}

// writeWebsiteVisitorResponse 无错误时按 200 写入公开 Messenger 成功响应，否则写入错误响应。
func writeWebsiteVisitorResponse[T any](writer http.ResponseWriter, request *http.Request, result T, err error) {
	if httpcodec.WriteApplicationError(writer, request, err) {
		return
	}
	writeWebsiteVisitorResult(writer, request, http.StatusOK, result)
}

// writeWebsiteVisitorResult 写入公开 Messenger 成功响应和本地化语言。
func writeWebsiteVisitorResult(writer http.ResponseWriter, request *http.Request, status int, value any) {
	writer.Header().Set("Content-Language", string(websiteVisitorLocale(request)))
	writer.Header().Set("Vary", "Accept-Language")
	httpcodec.WriteJSON(writer, request, status, value)
}

// websiteVisitorMethodNotAllowed 返回公开路由的显式方法错误。
func websiteVisitorMethodNotAllowed(allowed string) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Allow", allowed)
		httpcodec.WriteApplicationError(writer, request, appservice.WebsiteVisitorError(websiteVisitorLocale(request), appservice.ErrorKindFailed, i18n.VisitorErrorRequestInvalid, nil).WithStatus(http.StatusMethodNotAllowed))
	}
}
