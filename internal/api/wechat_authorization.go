//go:build server

package api

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	wechataction "github.com/runforyou-ai/luway/internal/actions/wechat"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
)

//go:embed wechat_authorization.html
var wechatAuthorizationHTML string

// wechatAuthorizationPage 是授权发起页与授权结果页的模板。
var wechatAuthorizationPage = template.Must(template.New("wechat_authorization").Parse(wechatAuthorizationHTML))

// WechatAuthorizationPageResolver 解析授权标识对应的微信授权页地址。
type WechatAuthorizationPageResolver interface {
	Execute(ctx context.Context, state string) (string, error)
}

// WechatAuthorizationCompleter 用授权回跳的授权码完成公众号授权。
type WechatAuthorizationCompleter interface {
	Execute(ctx context.Context, state, authorizationCode string) (wechataction.AuthorizationResult, error)
}

// wechatAuthorizationView 定义授权页面的展示内容。
type wechatAuthorizationView struct {
	Lang        string
	Title       string
	Heading     string
	Body        string
	Failed      bool
	ActionURL   string
	ActionLabel string
}

// showWechatAuthorization 输出授权发起页，页面链接到微信授权页；授权标识失效时输出失败页。
func (s *Service) showWechatAuthorization(writer http.ResponseWriter, request *http.Request) {
	languages := request.Header.Get("Accept-Language")
	authorizationURL, err := s.wechatAuthorizationPage.Execute(request.Context(), request.PathValue("state"))
	if err != nil {
		s.writeWechatAuthorizationFailure(writer, request, languages, err)
		return
	}
	title, lang := i18n.Localize(languages, i18n.WechatAuthorizationTitle)
	body, _ := i18n.Localize(languages, i18n.WechatAuthorizationStartBody)
	action, _ := i18n.Localize(languages, i18n.WechatAuthorizationStartAction)
	writeWechatAuthorizationPage(writer, request, http.StatusOK, wechatAuthorizationView{
		Lang: lang, Title: title, Heading: title, Body: body, ActionURL: authorizationURL, ActionLabel: action,
	})
}

// completeWechatAuthorization 处理微信授权回跳，输出授权结果页。
func (s *Service) completeWechatAuthorization(writer http.ResponseWriter, request *http.Request) {
	languages := request.Header.Get("Accept-Language")
	// 管理员在微信页面未完成授权时回跳不带授权码。
	if request.URL.Query().Get("auth_code") == "" {
		title, lang := i18n.Localize(languages, i18n.WechatAuthorizationTitle)
		heading, _ := i18n.Localize(languages, i18n.WechatAuthorizationFailedHeading)
		body, _ := i18n.Localize(languages, i18n.WechatAuthorizationIncomplete)
		writeWechatAuthorizationPage(writer, request, http.StatusBadRequest, wechatAuthorizationView{Lang: lang, Title: title, Heading: heading, Body: body, Failed: true})
		return
	}
	result, err := s.wechatAuthorizationComplete.Execute(request.Context(), request.PathValue("state"), request.URL.Query().Get("auth_code"))
	if err != nil {
		s.writeWechatAuthorizationFailure(writer, request, languages, err)
		return
	}
	title, lang := i18n.Localize(languages, i18n.WechatAuthorizationTitle)
	heading, _ := i18n.Localize(languages, i18n.WechatAuthorizationSuccessHeading)
	writeWechatAuthorizationPage(writer, request, http.StatusOK, wechatAuthorizationView{
		Lang: lang, Title: title, Heading: heading,
		Body: i18n.LocalizeTemplate(languages, i18n.WechatAuthorizationSuccessBody, map[string]any{"Name": result.NickName}),
	})
}

// writeWechatAuthorizationFailure 按授权失败原因输出失败页，未预期的错误记录日志。
func (s *Service) writeWechatAuthorizationFailure(writer http.ResponseWriter, request *http.Request, languages string, err error) {
	status, message := http.StatusConflict, i18n.ErrorInternal
	switch {
	case errors.Is(err, wechataction.ErrAuthorizationIntentNotFound):
		status, message = http.StatusNotFound, i18n.WechatAuthorizationExpired
	case errors.Is(err, wechataction.ErrAuthorizationRevoked):
		message = i18n.WechatAuthorizationRevoked
	case errors.Is(err, wechataction.ErrNotVerifiedServiceAccount):
		message = i18n.ErrorWechatNotVerifiedServiceAccount
	case errors.Is(err, wechataction.ErrAppIDTaken):
		message = i18n.ErrorWechatAppIDTaken
	case errors.Is(err, channelaction.ErrWechatAccountEnabledElsewhere):
		message = i18n.ErrorWechatAccountEnabledElsewhere
	case errors.Is(err, wechataction.ErrAuthorizedAccountMismatch):
		message = i18n.ErrorWechatAuthorizedAccountMismatch
	case errors.Is(err, wechataction.ErrPlatformNotConfigured):
		message = i18n.ErrorWechatPlatformNotConfigured
	case errors.Is(err, wechataction.ErrPlatformUnavailable):
		status, message = http.StatusServiceUnavailable, i18n.ErrorWechatPlatformUnavailable
	case errors.Is(err, wechat.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, i18n.ErrorWechatUnavailable
	default:
		status = http.StatusInternalServerError
		if request.Context().Err() == nil {
			slog.ErrorContext(request.Context(), "公众号授权页面处理失败", "error", err)
		}
	}
	title, lang := i18n.Localize(languages, i18n.WechatAuthorizationTitle)
	heading, _ := i18n.Localize(languages, i18n.WechatAuthorizationFailedHeading)
	body, _ := i18n.Localize(languages, message)
	writeWechatAuthorizationPage(writer, request, status, wechatAuthorizationView{Lang: lang, Title: title, Heading: heading, Body: body, Failed: true})
}

// writeWechatAuthorizationPage 渲染授权页面。
func writeWechatAuthorizationPage(writer http.ResponseWriter, request *http.Request, status int, view wechatAuthorizationView) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if err := wechatAuthorizationPage.Execute(writer, view); err != nil {
		slog.WarnContext(request.Context(), "渲染公众号授权页面失败", "error", err)
	}
}
