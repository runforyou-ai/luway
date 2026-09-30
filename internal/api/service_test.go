//go:build server

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runforyou-ai/cervi/internal/appservice"
)

type testBackend struct {
	appservice.Backend
	lastMeta appservice.RequestMeta
}

func (b *testBackend) InstallationStatus(context.Context, appservice.RequestMeta) (appservice.InstallationStatus, error) {
	return appservice.InstallationStatus{Installed: true, RegistrationOpen: true, DeploymentMode: appservice.DeploymentModeSelfHosted}, nil
}

func (b *testBackend) Login(_ context.Context, meta appservice.RequestMeta, input appservice.LoginInput) (appservice.Auth, error) {
	b.lastMeta = meta
	if input.Email != "admin@example.com" || input.Password != "password123" {
		return appservice.Auth{}, &appservice.Error{Kind: appservice.ErrorKindInvalid, Message: "账号或密码错误。"}
	}
	return appservice.Auth{Account: appservice.Account{ID: "account-1", Email: input.Email}, Token: "test-token", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (b *testBackend) LoadIdentity(_ context.Context, meta appservice.RequestMeta) (appservice.Identity, error) {
	b.lastMeta = meta
	if meta.Token != "test-token" {
		return appservice.Identity{}, &appservice.Error{State: appservice.SessionStateLogin, Message: "请先登录。"}
	}
	return testIdentity(), nil
}

// TestAuthenticationUsesBearerToken 验证登录返回令牌，后续请求读取 Bearer Token 和目标工作区请求头。
func TestAuthenticationUsesBearerToken(t *testing.T) {
	backend := &testBackend{}
	server := httptest.NewServer(NewService(appservice.New(backend)))
	defer server.Close()

	loginResponse := doJSON(t, http.MethodPost, server.URL+"/auth/login", map[string]string{
		"email": "admin@example.com", "password": "password123",
	}, "")
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginResponse.StatusCode, http.StatusOK)
	}
	var auth appservice.Auth
	if err := json.NewDecoder(loginResponse.Body).Decode(&auth); err != nil {
		t.Fatal(err)
	}
	if auth.Token != "test-token" {
		t.Fatalf("token = %q, want test-token", auth.Token)
	}

	unauthorized := doJSON(t, http.MethodGet, server.URL+"/auth/identity", nil, "")
	assertError(t, unauthorized, http.StatusUnauthorized, "", appservice.SessionStateLogin)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/auth/identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+auth.Token)
	request.Header.Set(appservice.WorkspaceHeader, " organization-1 ")
	authorized, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer authorized.Body.Close()
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("identity status = %d, want %d", authorized.StatusCode, http.StatusOK)
	}
	if backend.lastMeta.Token != auth.Token || backend.lastMeta.WorkspaceID != "organization-1" {
		t.Fatalf("backend meta = %#v, want token %q and workspace organization-1", backend.lastMeta, auth.Token)
	}
}

// TestInstallationStatusIsPublic 验证未登录可读取安装状态和注册开关。
func TestInstallationStatusIsPublic(t *testing.T) {
	server := httptest.NewServer(NewService(appservice.New(&testBackend{})))
	defer server.Close()

	response := doJSON(t, http.MethodGet, server.URL+"/installation/status", nil, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var status appservice.InstallationStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.RegistrationOpen || status.DeploymentMode != appservice.DeploymentModeSelfHosted {
		t.Fatalf("status = %#v", status)
	}
}

// TestInvalidJSONUsesRequestedLanguage 验证 HTTP 适配层的输入错误使用请求语言。
func TestInvalidJSONUsesRequestedLanguage(t *testing.T) {
	server := httptest.NewServer(NewService(appservice.New(&testBackend{})))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/auth/login", bytes.NewBufferString("{"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertError(t, response, http.StatusBadRequest, appservice.ErrorKindInvalid, "")
	if response.Header.Get("Content-Language") != "zh-CN" {
		t.Fatalf("language = %q, want zh-CN", response.Header.Get("Content-Language"))
	}
}

func testIdentity() appservice.Identity {
	return appservice.Identity{
		Organization: appservice.Organization{ID: "organization-1", Name: "演示公司", Slug: "app"},
		User:         appservice.CurrentUser{ID: "user-1", OrganizationID: "organization-1", Email: "admin@example.com", DisplayName: "管理员", RoleID: "role-1", Status: "active", Locale: "zh-CN", TimeZone: "Asia/Shanghai", MessageNotificationsEnabled: true, WorkStatus: appservice.WorkStatusWorking},
	}
}

func doJSON(t *testing.T, method, endpoint string, body any, token string) *http.Response {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, endpoint, &payload)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertError(t *testing.T, response *http.Response, status int, kind appservice.ErrorKind, state appservice.SessionState) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d", response.StatusCode, status)
	}
	var payload struct {
		Error appservice.Error `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Kind != kind || payload.Error.State != state {
		t.Fatalf("error = %+v, want kind %q state %q", payload.Error, kind, state)
	}
}
