//go:build server

package api

import (
	"net/http"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/httpcodec"
)

// registerAdminAuthRoutes 注册需要写入或清除平台后台 Cookie 的认证路由。
func (s *Service) registerAdminAuthRoutes() {
	s.handle("POST /install", s.installPlatform)
	s.handle("POST /admin/auth/login", s.adminLogin)
	s.handle("POST /admin/auth/logout", s.adminLogout)
}

func (s *Service) installPlatform(writer http.ResponseWriter, request *http.Request) {
	var input appservice.InstallPlatformInput
	if !httpcodec.BindJSON(writer, request, &input) {
		return
	}
	output, err := s.backend.InstallPlatform(request.Context(), httpcodec.RequestMeta(request), input)
	if httpcodec.WriteApplicationError(writer, request, err) {
		return
	}
	appservice.SetAdminSessionCookie(writer, output.Token, output.ExpiresAt, s.secureCookie())
	httpcodec.WriteResult(writer, request, http.StatusCreated, appservice.AdminAuth{
		Account: output.Account, ExpiresAt: output.ExpiresAt,
	}, nil)
}

func (s *Service) adminLogin(writer http.ResponseWriter, request *http.Request) {
	var input appservice.LoginInput
	if !httpcodec.BindJSON(writer, request, &input) {
		return
	}
	output, err := s.backend.AdminLogin(request.Context(), httpcodec.RequestMeta(request), input)
	if httpcodec.WriteApplicationError(writer, request, err) {
		return
	}
	appservice.SetAdminSessionCookie(writer, output.Token, output.ExpiresAt, s.secureCookie())
	httpcodec.WriteResult(writer, request, http.StatusOK, appservice.AdminAuth{
		Account: output.Account, ExpiresAt: output.ExpiresAt,
	}, nil)
}

func (s *Service) adminLogout(writer http.ResponseWriter, request *http.Request) {
	err := s.backend.AdminLogout(request.Context(), httpcodec.RequestMeta(request))
	appservice.ClearAdminSessionCookie(writer, s.secureCookie())
	httpcodec.WriteEmpty(writer, request, err)
}

func (s *Service) secureCookie() bool {
	if s.secureVisitorCookie != nil {
		return s.secureVisitorCookie()
	}
	return false
}
