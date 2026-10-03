//go:build server

package productsite

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
)

// serve 以指定方法、路径和语言偏好请求产品首页服务。
func serve(method, target, acceptLanguage string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Accept-Language", acceptLanguage)
	recorder := httptest.NewRecorder()
	NewService("/docs/_assets/site.css?v=test").ServeHTTP(recorder, request)
	return recorder
}

// TestServeRoutes 校验根路径按语言偏好跳转、语言目录补全斜杠、未知路径与不支持的方法。
func TestServeRoutes(t *testing.T) {
	tests := []struct {
		method, target, acceptLanguage string
		status                         int
		location                       string
	}{
		{http.MethodGet, "/", "zh-CN,zh;q=0.9", http.StatusFound, "/zh-cn/"},
		{http.MethodGet, "/", "en-US", http.StatusFound, "/en/"},
		{http.MethodGet, "/en", "", http.StatusMovedPermanently, "/en/"},
		{http.MethodGet, "/zh-cn/", "", http.StatusOK, ""},
		{http.MethodHead, "/en/", "", http.StatusOK, ""},
		{http.MethodGet, "/missing/", "en-US", http.StatusNotFound, ""},
		{http.MethodGet, "/en/missing/", "en-US", http.StatusNotFound, ""},
		{http.MethodPost, "/en/", "", http.StatusMethodNotAllowed, ""},
	}
	for _, test := range tests {
		recorder := serve(test.method, test.target, test.acceptLanguage)
		if recorder.Code != test.status {
			t.Errorf("%s %s 状态码 = %d，期望 %d", test.method, test.target, recorder.Code, test.status)
		}
		if location := recorder.Header().Get("Location"); location != test.location {
			t.Errorf("%s %s 跳转 = %q，期望 %q", test.method, test.target, location, test.location)
		}
	}
}

// TestHomePage 校验首页带当前品牌名称、应用与文档入口，且全部文案均已渲染。
func TestHomePage(t *testing.T) {
	for locale, tag := range map[string]string{"zh-cn": "zh-CN", "en": "en-US"} {
		body := serve(http.MethodGet, "/"+locale+"/", "").Body.String()
		for _, want := range []string{
			`lang="` + tag + `"`, brand.Current().Name(tag), `href="` + domain.WebAppPath + `"`,
			`href="/docs/` + locale + `/"`, `href="/docs/` + locale + `/deployment/"`, `/docs/_assets/site.css?v=test`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s 首页缺少 %q", locale, want)
			}
		}
		if strings.Contains(body, "<no value>") || strings.Contains(body, "{{") {
			t.Errorf("%s 首页存在未渲染的文案", locale)
		}
	}
}
