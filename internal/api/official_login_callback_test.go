//go:build server

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOfficialLoginCallbackRedirectsToHashRoute 验证授权回调转到前端回调页并保留参数，其他请求照常处理。
func TestOfficialLoginCallbackRedirectsToHashRoute(t *testing.T) {
	handler := OfficialLoginCallbackMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/callback?code=abc&state=xyz", nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/#/auth/callback?code=abc&state=xyz" {
		t.Fatalf("回调重定向不正确: %d %q", recorder.Code, recorder.Header().Get("Location"))
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/callbacks", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("非回调路径被拦截: %d", recorder.Code)
	}
}
