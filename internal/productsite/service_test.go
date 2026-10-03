//go:build server

package productsite

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runforyou-ai/luway/internal/clientrelease"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
)

// serve 以指定方法、路径和语言偏好请求不提供客户端安装包的产品站服务。
func serve(method, target, acceptLanguage string) *httptest.ResponseRecorder {
	clients, _ := clientrelease.Load("", "test")
	return serveWith(NewService("/docs/_assets/site.css?v=test", clients), method, target, acceptLanguage)
}

// serveWith 以指定方法、路径和语言偏好请求产品站服务。
func serveWith(service *Service, method, target, acceptLanguage string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Accept-Language", acceptLanguage)
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request)
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
		{http.MethodGet, "/zh-cn/download/", "", http.StatusOK, ""},
		{http.MethodGet, "/en/download", "", http.StatusMovedPermanently, "/en/download/"},
		{http.MethodGet, "/en/download/missing/", "en-US", http.StatusNotFound, ""},
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

// abcFile 返回内容为 abc 的客户端文件。
func abcFile(name string) clientrelease.File {
	return clientrelease.File{Name: name, Size: 3, SHA512: "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"}
}

// TestDownloadPage 校验下载页按平台列出服务器提供的安装包，移动端显示即将推出。
func TestDownloadPage(t *testing.T) {
	directory := t.TempDir()
	index := clientrelease.Index{Version: "1.2.3", Installers: []clientrelease.Installer{
		{OS: clientrelease.OSWindows, Arch: "amd64", Format: clientrelease.FormatEXE, File: abcFile("app_1.2.3_windows_amd64-installer.exe")},
		{OS: clientrelease.OSDarwin, Arch: clientrelease.ArchUniversal, Format: clientrelease.FormatDMG, File: abcFile("app_1.2.3_darwin_universal.dmg")},
		{OS: clientrelease.OSLinux, Arch: "amd64", Format: clientrelease.FormatDeb, File: abcFile("app_1.2.3_linux_amd64.deb")},
	}}
	for _, file := range index.Installers {
		if err := os.WriteFile(filepath.Join(directory, file.Name), []byte("abc"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(directory, clientrelease.IndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	clients, err := clientrelease.Load(directory, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	body := serveWith(NewService("/site.css", clients), http.MethodGet, "/zh-cn/download/", "").Body.String()
	for _, want := range []string{
		`href="/clients/app_1.2.3_windows_amd64-installer.exe?v=ddaf35a193617aba"`, `href="/clients/app_1.2.3_darwin_universal.dmg?v=ddaf35a193617aba"`,
		"版本 1.2.3", "Apple 芯片与 Intel", "Debian / Ubuntu · x64", `data-platform="android" aria-disabled="true"`,
		`data-platform="ios" aria-disabled="true"`, "即将推出",
		`<option value="/en/download/"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("下载页缺少 %q", want)
		}
	}
	if strings.Contains(body, "<no value>") || strings.Contains(body, "{{") {
		t.Error("下载页存在未渲染的文案")
	}
}

// TestDownloadPageUnavailable 校验服务器未提供安装包时桌面平台显示未提供且没有下载链接。
func TestDownloadPageUnavailable(t *testing.T) {
	body := serve(http.MethodGet, "/zh-cn/download/", "").Body.String()
	if !strings.Contains(body, `data-platform="windows" aria-disabled="true"`) || !strings.Contains(body, "当前服务器未提供") || strings.Contains(body, "/clients/") {
		t.Error("未提供安装包时下载页应显示未提供且不含下载链接")
	}
}
