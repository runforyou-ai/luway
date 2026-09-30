package appservice

import (
	"context"
	"errors"
	"testing"
)

type stubBackend struct {
	Backend
}

type stubNativeLocaleUpdater struct {
	locales []Locale
}

// SetLocale 记录一次原生界面语言同步。
func (u *stubNativeLocaleUpdater) SetLocale(locale Locale) {
	u.locales = append(u.locales, locale)
}

type localeBackend struct {
	Backend
	locale Locale
}

// Login 返回带指定语言的登录身份。
func (b *localeBackend) Login(context.Context, RequestMeta, LoginInput) (Auth, error) {
	return Auth{Account: Account{Locale: b.locale}}, nil
}

// Logout 接受退出登录。
func (*localeBackend) Logout(context.Context, RequestMeta) error {
	return nil
}

// UpdateUserPreferences 返回保存后的用户语言。
func (b *localeBackend) UpdateUserPreferences(context.Context, RequestMeta, UserPreferencesInput) (CurrentUser, error) {
	return CurrentUser{Locale: b.locale}, nil
}

// TestPlatformMethodsRequireCapability 验证未实现平台能力的 Backend 返回方法不允许。
func TestPlatformMethodsRequireCapability(t *testing.T) {
	service := New(&stubBackend{})
	meta := RequestMeta{Locale: LocaleChineseSimplified}

	_, err := service.InstallWorkspace(context.Background(), meta, InstallWorkspaceInput{})
	assertMethodNotAllowed(t, err)

	_, err = service.ServerURL(context.Background(), meta)
	assertMethodNotAllowed(t, err)

	_, err = service.ProbeServer(context.Background(), meta, "https://app.example.com")
	assertMethodNotAllowed(t, err)

	err = service.ConnectServer(context.Background(), meta, "https://app.example.com")
	assertMethodNotAllowed(t, err)

	_, err = service.SelectImage(context.Background(), meta)
	assertMethodNotAllowed(t, err)
}

// TestNativeLocaleFollowsAuthenticationAndPreferences 验证原生界面跟随登录和偏好语言。
func TestNativeLocaleFollowsAuthenticationAndPreferences(t *testing.T) {
	backend := &localeBackend{locale: LocaleChineseSimplified}
	updater := &stubNativeLocaleUpdater{}
	service := New(backend, WithNativeLocaleUpdater(updater))

	if _, err := service.Login(context.Background(), RequestMeta{}, LoginInput{}); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	backend.locale = LocaleEnglishUnitedStates
	if _, err := service.UpdateUserPreferences(context.Background(), RequestMeta{}, UserPreferencesInput{}); err != nil {
		t.Fatalf("UpdateUserPreferences() error = %v", err)
	}
	if err := service.Logout(context.Background(), RequestMeta{}); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}

	want := []Locale{LocaleChineseSimplified, LocaleEnglishUnitedStates}
	if len(updater.locales) != len(want) || updater.locales[0] != want[0] || updater.locales[1] != want[1] {
		t.Fatalf("locales = %v, want %v", updater.locales, want)
	}
}

type startupBackend struct {
	Backend
	installed        bool
	registrationOpen bool
	deploymentMode   DeploymentMode
	statusErr        error
	identityCalls    int
}

// InstallationStatus 返回测试指定的安装状态。
func (b *startupBackend) InstallationStatus(context.Context, RequestMeta) (InstallationStatus, error) {
	if b.statusErr != nil {
		return InstallationStatus{}, b.statusErr
	}
	return InstallationStatus{Installed: b.installed, RegistrationOpen: b.registrationOpen, DeploymentMode: b.deploymentMode}, nil
}

// LoadIdentity 返回空身份并累计调用次数。
func (b *startupBackend) LoadIdentity(context.Context, RequestMeta) (Identity, error) {
	b.identityCalls++
	return Identity{}, nil
}

type nativeStartupBackend struct {
	*startupBackend
	serverURL string
}

// ServerURL 返回原生端已保存的服务器地址。
func (b *nativeStartupBackend) ServerURL(context.Context, RequestMeta) (string, error) {
	return b.serverURL, nil
}

// ProbeServer 返回空的服务器检测结果。
func (b *nativeStartupBackend) ProbeServer(context.Context, RequestMeta, string) (InstallationStatus, error) {
	return InstallationStatus{}, nil
}

// ConnectServer 接受服务器连接。
func (b *nativeStartupBackend) ConnectServer(context.Context, RequestMeta, string) error {
	return nil
}

// TestLoadStartupResolvesWebEntry 验证 Web 端只按初始化状态选择启动入口。
func TestLoadStartupResolvesWebEntry(t *testing.T) {
	backend := &startupBackend{installed: false}
	service := New(backend)
	startup, err := service.LoadStartup(context.Background(), RequestMeta{})
	if err != nil || startup.State != SessionStateSetup {
		t.Fatalf("uninstalled startup = %+v, err = %v", startup, err)
	}

	backend = &startupBackend{installed: true, registrationOpen: true, deploymentMode: DeploymentModeSelfHosted}
	service = New(backend)
	startup, err = service.LoadStartup(context.Background(), RequestMeta{Token: "ignored"})
	if err != nil || startup.State != SessionStateReady || startup.DeploymentMode != DeploymentModeSelfHosted {
		t.Fatalf("ready startup = %+v, err = %v", startup, err)
	}
	if calls := backend.identityCalls; calls != 0 {
		t.Fatalf("web startup identity calls = %d, want 0", calls)
	}
}

// TestLoadStartupResolvesManagedWebEntry 验证托管部署没有初始化入口，尚无账号时也直接就绪。
func TestLoadStartupResolvesManagedWebEntry(t *testing.T) {
	backend := &startupBackend{deploymentMode: DeploymentModeManaged}
	startup, err := New(backend).LoadStartup(context.Background(), RequestMeta{})
	if err != nil || startup.State != SessionStateReady || startup.DeploymentMode != DeploymentModeManaged {
		t.Fatalf("managed startup = %+v, err = %v", startup, err)
	}
}

// TestLoadStartupResolvesNativeEntry 验证原生端只检测服务器地址和初始化状态。
func TestLoadStartupResolvesNativeEntry(t *testing.T) {
	backend := &nativeStartupBackend{
		startupBackend: &startupBackend{installed: true},
		serverURL:      "https://app.example.com",
	}
	startup, err := New(backend).LoadStartup(context.Background(), RequestMeta{})
	if err != nil || startup.State != SessionStateReady {
		t.Fatalf("ready native startup = %+v, err = %v", startup, err)
	}

	backend.serverURL = ""
	startup, err = New(backend).LoadStartup(context.Background(), RequestMeta{})
	if err != nil || startup.State != SessionStateConnect {
		t.Fatalf("unconnected native startup = %+v, err = %v", startup, err)
	}

	backend.serverURL = "https://app.example.com"
	backend.installed = false
	startup, err = New(backend).LoadStartup(context.Background(), RequestMeta{})
	if err != nil || startup.State != SessionStateConnect {
		t.Fatalf("uninitialized native startup = %+v, err = %v", startup, err)
	}
	if backend.identityCalls != 0 {
		t.Fatalf("native startup identity calls = %d, want 0", backend.identityCalls)
	}
}

// TestLoadStartupRoutesUnavailableNativeServerToConnect 验证原生端服务器不可用时进入连接页。
func TestLoadStartupRoutesUnavailableNativeServerToConnect(t *testing.T) {
	backend := &nativeStartupBackend{
		startupBackend: &startupBackend{
			statusErr: &Error{Kind: ErrorKindUnavailable, Message: "暂时无法连接服务器。"},
		},
		serverURL: "https://app.example.com",
	}

	startup, err := New(backend).LoadStartup(context.Background(), RequestMeta{})
	if err != nil || startup.State != SessionStateConnect {
		t.Fatalf("unavailable native startup = %+v, err = %v", startup, err)
	}
	if backend.identityCalls != 0 {
		t.Fatalf("unavailable native startup identity calls = %d, want 0", backend.identityCalls)
	}
}

func assertMethodNotAllowed(t *testing.T, err error) {
	t.Helper()
	var apiError *Error
	if !errors.As(err, &apiError) || apiError.Kind != ErrorKindFailed {
		t.Fatalf("error = %#v, want METHOD_NOT_ALLOWED", err)
	}
}
