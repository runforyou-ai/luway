//go:build server

package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common/customeridentity"
	"github.com/runforyou-ai/cervi/internal/i18n"
)

const (
	websiteVisitorHeader      = appservice.WebsiteVisitorTokenHeader
	websiteCustomerHeader     = appservice.WebsiteCustomerTokenHeader
	websiteCustomerKey        = "website_customer"
	websiteVisitorTokenSize   = 32
	websiteVisitorBodyLimit   = 16 * 1024
	websiteVisitorCookieAge   = 365 * 24 * 60 * 60
	websiteVisitorExternalKey = "website_visitor_external_id"
	websiteVisitorTokenKey    = "website_visitor_token"
)

// registerWebsiteVisitorRoutes 注册网站 Messenger 公开路由。
func (s *Service) registerWebsiteVisitorRoutes(router *gin.Engine) {
	if s.websiteVisitor == nil {
		return
	}
	const messengerPath = "/public/website-channels/:channelID/messenger"
	const messagesPath = "/public/website-channels/:channelID/messages"
	const directoryPath = "/public/website-channels/:channelID/conversations"
	const historyPath = "/public/website-channels/:channelID/conversations/:conversationID/messages"
	const typingPath = "/public/website-channels/:channelID/conversations/:conversationID/typing"
	const ratingPath = "/public/website-channels/:channelID/conversations/:conversationID/service-sessions/:serviceSessionID/rating"
	const readPath = "/public/website-channels/:channelID/conversations/:conversationID/read"
	const resumePath = "/public/website-channels/:channelID/resume"
	const realtimePath = "/public/website-channels/:channelID/realtime"
	const attachmentsPath = "/public/website-channels/:channelID/attachments"
	const attachmentUploadPath = "/public/website-channels/:channelID/attachments/:fileID"
	const attachmentMessagesPath = "/public/website-channels/:channelID/attachment-messages"
	const messageAttachmentPath = "/public/website-channels/:channelID/conversations/:conversationID/messages/:messageID/attachment"
	const helpCenterPath = "/public/website-channels/:channelID/help-center"
	const helpArticlePath = "/public/website-channels/:channelID/help-center/articles/:articleID"
	const helpSearchPath = "/public/website-channels/:channelID/help-center/search"
	router.GET(messengerPath, s.initializeWebsiteMessenger)
	router.GET(helpCenterPath, s.getWebsiteHelpCenter)
	router.GET(helpArticlePath, s.getWebsiteHelpArticle)
	router.GET(helpSearchPath, s.searchWebsiteHelpCenter)
	router.POST(messagesPath, s.authorizeWebsiteVisitor, s.sendWebsiteVisitorMessage)
	router.GET(directoryPath, s.authorizeWebsiteVisitor, s.listWebsiteVisitorConversations)
	router.GET(historyPath, s.authorizeWebsiteVisitor, s.listWebsiteVisitorMessages)
	router.POST(attachmentsPath, s.authorizeWebsiteVisitor, s.createWebsiteVisitorAttachmentUpload)
	router.POST(attachmentUploadPath, s.authorizeWebsiteVisitor, s.completeWebsiteVisitorAttachmentUpload)
	router.POST(attachmentMessagesPath, s.authorizeWebsiteVisitor, s.sendWebsiteVisitorAttachmentMessage)
	router.GET(messageAttachmentPath, s.authorizeWebsiteVisitor, s.getWebsiteVisitorMessageAttachment)
	router.POST(typingPath, s.authorizeWebsiteVisitor, s.reportWebsiteVisitorTyping)
	router.POST(ratingPath, s.authorizeWebsiteVisitor, s.rateWebsiteVisitorServiceSession)
	router.POST(readPath, s.authorizeWebsiteVisitor, s.markWebsiteVisitorConversationRead)
	router.POST(resumePath, s.resumeWebsiteVisitor)
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, attachmentsPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, attachmentUploadPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, attachmentMessagesPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, messageAttachmentPath, websiteVisitorMethodNotAllowed(http.MethodGet))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, messengerPath, websiteVisitorMethodNotAllowed(http.MethodGet))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, messagesPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, directoryPath, websiteVisitorMethodNotAllowed(http.MethodGet))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, historyPath, websiteVisitorMethodNotAllowed(http.MethodGet))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, typingPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, ratingPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, readPath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, resumePath, websiteVisitorMethodNotAllowed(http.MethodPost))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, helpCenterPath, websiteVisitorMethodNotAllowed(http.MethodGet))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, helpArticlePath, websiteVisitorMethodNotAllowed(http.MethodGet))
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, helpSearchPath, websiteVisitorMethodNotAllowed(http.MethodGet))
	if s.visitorRealtime == nil {
		return
	}
	router.GET(realtimePath, s.authorizeWebsiteVisitor, s.serveWebsiteVisitorRealtime)
	router.Match([]string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace}, realtimePath, websiteVisitorMethodNotAllowed(http.MethodGet))
}

// serveWebsiteVisitorRealtime 输出网站访客实时事件流，事件流结束前由网关独占响应写入。
func (s *Service) serveWebsiteVisitorRealtime(c *gin.Context) {
	s.visitorRealtime.ServeVisitor(c.Writer, c.Request, s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey))
}

// authorizeWebsiteVisitor 统一处理需要访客身份的公开路由：禁止缓存；携带签名身份时验签并以登录用户外部编号访问，否则按 Header、Cookie 读取访客 Token；身份缺失、非法或失效时按公开错误体拒绝。
func (s *Service) authorizeWebsiteVisitor(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if customerToken := strings.TrimSpace(c.GetHeader(websiteCustomerHeader)); customerToken != "" {
		if !s.verifyWebsiteCustomer(c, customerToken) {
			c.Abort()
		}
		return
	}
	token, valid := readWebsiteVisitorToken(c, c.Param("channelID"))
	if !valid {
		writeApplicationError(c, invalidWebsiteVisitorTokenError(c))
		c.Abort()
		return
	}
	c.Set(websiteVisitorExternalKey, customeridentity.AnonymousExternalID(token))
	c.Set(websiteVisitorTokenKey, token)
}

// verifyWebsiteCustomer 校验签名身份并写入登录用户外部编号，失败时写入错误响应并返回 false。
func (s *Service) verifyWebsiteCustomer(c *gin.Context, customerToken string) bool {
	customer, err := s.websiteVisitor.VerifyCustomer(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), customerToken)
	if writeApplicationError(c, err) {
		return false
	}
	c.Set(websiteVisitorExternalKey, customeridentity.CustomerExternalID(customer.UserID))
	c.Set(websiteCustomerKey, &customer)
	return true
}

// initializeWebsiteMessenger 返回会话列表：携带签名身份时以登录用户初始化，不签发也不轮换访客 Token；匿名访客签发或恢复访客 Token，rotate=1 时忽略已有 Token 重新签发。
func (s *Service) initializeWebsiteMessenger(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	channelID := c.Param("channelID")
	if customerToken := strings.TrimSpace(c.GetHeader(websiteCustomerHeader)); customerToken != "" {
		if !s.verifyWebsiteCustomer(c, customerToken) {
			return
		}
		result, err := s.websiteVisitor.InitializeMessenger(c.Request.Context(), s.websiteVisitorMeta(c), channelID, c.GetString(websiteVisitorExternalKey), "")
		if writeApplicationError(c, err) {
			return
		}
		writeWebsiteVisitorResult(c, http.StatusOK, result)
		return
	}
	token, valid := readWebsiteVisitorToken(c, channelID)
	issued := false
	if c.Query("rotate") == "1" {
		valid = false
	}
	if !valid {
		var err error
		token, err = generateWebsiteVisitorToken()
		if err != nil {
			slog.Warn("生成网站访客令牌失败", "channel_id", channelID, "error", err)
			writeApplicationError(c, appservice.WebsiteVisitorError(websiteVisitorLocale(c), appservice.ErrorKindFailed, i18n.VisitorErrorLoadFailed, nil))
			return
		}
		issued = true
	}
	result, err := s.websiteVisitor.InitializeMessenger(c.Request.Context(), s.websiteVisitorMeta(c), channelID, customeridentity.AnonymousExternalID(token), token)
	if writeApplicationError(c, err) {
		return
	}
	if issued {
		s.setWebsiteVisitorCookie(c, channelID, token)
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// setWebsiteVisitorCookie 设置渠道级长期访客 Cookie。
func (s *Service) setWebsiteVisitorCookie(c *gin.Context, channelID, token string) {
	secure := c.Request.TLS != nil || (s.trustForwardedProto && strings.EqualFold(strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]), "https"))
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: websiteVisitorCookieName(channelID), Value: token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: sameSite, MaxAge: websiteVisitorCookieAge,
	})
}

// resumeWebsiteVisitor 用邮件中的回访令牌换取匿名访客令牌，写入与挂件相同的渠道访客 Cookie，同一浏览器原有的匿名身份被替换。
func (s *Service) resumeWebsiteVisitor(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input appservice.WebsiteVisitorResumeInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	channelID := c.Param("channelID")
	result, err := s.websiteVisitor.ResumeVisitor(c.Request.Context(), s.websiteVisitorMeta(c), channelID, input)
	if writeApplicationError(c, err) {
		return
	}
	s.setWebsiteVisitorCookie(c, channelID, result.VisitorToken)
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// getWebsiteHelpCenter 返回网站渠道帮助中心的文章合集，无需访客身份。
func (s *Service) getWebsiteHelpCenter(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := s.websiteVisitor.GetHelpCenter(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"))
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// getWebsiteHelpArticle 返回网站渠道帮助中心的文章详情，无需访客身份。
func (s *Service) getWebsiteHelpArticle(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := s.websiteVisitor.GetHelpArticle(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.Param("articleID"))
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// searchWebsiteHelpCenter 在网站渠道帮助中心检索 q 参数的内容并返回相关文章，无需访客身份。
func (s *Service) searchWebsiteHelpCenter(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	result, err := s.websiteVisitor.SearchHelpCenter(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), appservice.WebsiteVisitorHelpSearchInput{Query: c.Query("q")})
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// markWebsiteVisitorConversationRead 记录网站访客在客户线程中已读到的位置。
func (s *Service) markWebsiteVisitorConversationRead(c *gin.Context) {
	var input appservice.WebsiteVisitorReadInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	err := s.websiteVisitor.MarkConversationRead(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), c.Param("conversationID"), input)
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, struct{}{})
}

// sendWebsiteVisitorMessage 接收网站访客文本消息。
func (s *Service) sendWebsiteVisitorMessage(c *gin.Context) {
	var input appservice.WebsiteVisitorTextMessageInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	result, err := s.websiteVisitor.SendTextMessage(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), input)
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// createWebsiteVisitorAttachmentUpload 创建网站访客附件的上传请求。
func (s *Service) createWebsiteVisitorAttachmentUpload(c *gin.Context) {
	var input appservice.WebsiteVisitorUploadInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	result, err := s.websiteVisitor.CreateAttachmentUpload(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), input)
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// reportWebsiteVisitorTyping 向企业客服发布网站访客的输入状态。
func (s *Service) reportWebsiteVisitorTyping(c *gin.Context) {
	var input appservice.WebsiteVisitorTypingInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	err := s.websiteVisitor.ReportTyping(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), c.Param("conversationID"), input)
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, struct{}{})
}

// rateWebsiteVisitorServiceSession 保存网站访客对已关闭客服处理周期的评价。
func (s *Service) rateWebsiteVisitorServiceSession(c *gin.Context) {
	var input appservice.WebsiteVisitorRatingInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	result, err := s.websiteVisitor.RateServiceSession(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), c.Param("conversationID"), c.Param("serviceSessionID"), input)
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// completeWebsiteVisitorAttachmentUpload 核验网站访客上传的附件内容。
func (s *Service) completeWebsiteVisitorAttachmentUpload(c *gin.Context) {
	err := s.websiteVisitor.CompleteAttachmentUpload(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), c.Param("fileID"))
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, struct{}{})
}

// sendWebsiteVisitorAttachmentMessage 接收网站访客附件消息。
func (s *Service) sendWebsiteVisitorAttachmentMessage(c *gin.Context) {
	var input appservice.WebsiteVisitorAttachmentMessageInput
	if !bindWebsiteVisitorJSON(c, &input) {
		return
	}
	result, err := s.websiteVisitor.SendAttachmentMessage(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), input)
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// getWebsiteVisitorMessageAttachment 重新签发网站访客消息附件的预览与下载地址。
func (s *Service) getWebsiteVisitorMessageAttachment(c *gin.Context) {
	result, err := s.websiteVisitor.GetMessageAttachment(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), c.Param("conversationID"), c.Param("messageID"))
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// listWebsiteVisitorConversations 返回网站访客当前渠道身份的线程目录。
func (s *Service) listWebsiteVisitorConversations(c *gin.Context) {
	result, err := s.websiteVisitor.ListConversations(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey))
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// listWebsiteVisitorMessages 返回网站访客指定线程的消息历史。
func (s *Service) listWebsiteVisitorMessages(c *gin.Context) {
	result, err := s.websiteVisitor.ListMessages(c.Request.Context(), s.websiteVisitorMeta(c), c.Param("channelID"), c.GetString(websiteVisitorExternalKey), c.Param("conversationID"), appservice.WebsiteVisitorMessageHistoryInput{
		Before: c.Query("before"), After: c.Query("after"),
	})
	if writeApplicationError(c, err) {
		return
	}
	writeWebsiteVisitorResult(c, http.StatusOK, result)
}

// bindWebsiteVisitorJSON 限制并严格解析公开 JSON 请求体。
func bindWebsiteVisitorJSON(c *gin.Context, output any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, websiteVisitorBodyLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeApplicationError(c, appservice.WebsiteVisitorError(websiteVisitorLocale(c), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil).WithStatus(http.StatusRequestEntityTooLarge))
			return false
		}
		writeApplicationError(c, appservice.WebsiteVisitorError(websiteVisitorLocale(c), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeApplicationError(c, appservice.WebsiteVisitorError(websiteVisitorLocale(c), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, nil))
		return false
	}
	return true
}

// readWebsiteVisitorToken 按 Header、Cookie 顺序读取访客 Token。
func readWebsiteVisitorToken(c *gin.Context, channelID string) (string, bool) {
	if value := strings.TrimSpace(c.GetHeader(websiteVisitorHeader)); value != "" {
		return value, customeridentity.ValidAnonymousToken(value)
	}
	cookie, err := c.Request.Cookie(websiteVisitorCookieName(channelID))
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(cookie.Value)
	return value, customeridentity.ValidAnonymousToken(value)
}

// generateWebsiteVisitorToken 生成 32 位小写十六进制访客 Token。
func generateWebsiteVisitorToken() (string, error) {
	value := make([]byte, websiteVisitorTokenSize/2)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// websiteVisitorCookieName 返回渠道级访客 Cookie 名称。
func websiteVisitorCookieName(channelID string) string {
	return "visitor_" + channelID
}

// websiteVisitorMeta 构造不含成员认证的访客调用元信息，含已验签的登录用户、浏览器标识与可信代理提供的国家代码。
func (s *Service) websiteVisitorMeta(c *gin.Context) appservice.WebsiteVisitorMeta {
	meta := appservice.WebsiteVisitorMeta{
		Locale: websiteVisitorLocale(c), Token: c.GetString(websiteVisitorTokenKey),
		UserAgent: c.GetHeader("User-Agent"),
	}
	if s.visitorCountryHeader != "" {
		meta.Country = c.GetHeader(s.visitorCountryHeader)
	}
	if customer, ok := c.Get(websiteCustomerKey); ok {
		meta.Customer = customer.(*appservice.WebsiteVisitorCustomer)
		meta.CustomerToken = strings.TrimSpace(c.GetHeader(websiteCustomerHeader))
	}
	return meta
}

// websiteVisitorLocale 按请求语言偏好返回访客使用的对客语言。
func websiteVisitorLocale(c *gin.Context) appservice.CustomerLocale {
	return appservice.CustomerLocale(i18n.PreferredCustomerLocale(c.GetHeader("Accept-Language")))
}

// invalidWebsiteVisitorTokenError 返回缺失或非法访客 Token 错误。
func invalidWebsiteVisitorTokenError(c *gin.Context) *appservice.Error {
	return appservice.WebsiteVisitorError(websiteVisitorLocale(c), appservice.ErrorKindInvalid, i18n.VisitorErrorRequestInvalid, map[string]i18n.Key{"visitorToken": i18n.VisitorErrorRequestInvalid})
}

// writeWebsiteVisitorResult 写入公开 Messenger 成功响应和本地化语言。
func writeWebsiteVisitorResult(c *gin.Context, status int, value any) {
	c.Header("Content-Language", string(websiteVisitorLocale(c)))
	c.Header("Vary", "Accept-Language")
	c.JSON(status, value)
}

// websiteVisitorMethodNotAllowed 返回公开路由的显式方法错误。
func websiteVisitorMethodNotAllowed(allowed string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Allow", allowed)
		writeApplicationError(c, appservice.WebsiteVisitorError(websiteVisitorLocale(c), appservice.ErrorKindFailed, i18n.VisitorErrorRequestInvalid, nil).WithStatus(http.StatusMethodNotAllowed))
	}
}
