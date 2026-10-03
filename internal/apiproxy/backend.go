//go:build !server

package apiproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
	"github.com/runforyou-ai/luway/internal/i18n"
)

var (
	_ appservice.Backend           = (*Backend)(nil)
	_ appservice.ServerConnector   = (*Backend)(nil)
	_ appservice.RealtimeConnector = (*Backend)(nil)
)

// ErrSessionChanged 表示请求给出的登录会话已不是当前服务器上的当前会话。
var ErrSessionChanged = errors.New("login session changed")

// Backend 将类型化应用服务调用转换为远程 HTTP 请求。
type Backend struct {
	connection *connection
	sessions   *clientsession.Manager
	sessionMu  sync.Mutex
	realtime   *realtimeClient
}

// NewBackend 创建原生端使用的远程应用后端，defaultServerURL 是本机尚未连接服务器时使用的内置部署地址，emit 把实时连接事件投递给指定窗口（窗口标识为空时投递给全部窗口），caller 从调用上下文解析发起请求的前端窗口标识。
func NewBackend(store Store, defaultServerURL string, sessions *clientsession.Manager, emit func(owner, name string, data any), caller func(context.Context) string) (*Backend, error) {
	remoteConnection, err := newConnection(store, defaultServerURL)
	if err != nil {
		return nil, err
	}
	return &Backend{connection: remoteConnection, sessions: sessions, realtime: &realtimeClient{emit: emit, caller: caller}}, nil
}

// InstallationStatus 通过公开接口读取远程安装状态。
func (b *Backend) InstallationStatus(ctx context.Context, meta appservice.RequestMeta) (appservice.InstallationStatus, error) {
	state := b.connection.currentState()
	if state == nil {
		return appservice.InstallationStatus{}, appservice.SessionError(meta, appservice.SessionStateConnect, i18n.ErrorServerConnectionRequired)
	}
	status, err := probeServer(ctx, state)
	if err == nil {
		return status, nil
	}
	if ctx.Err() != nil {
		return appservice.InstallationStatus{}, ctx.Err()
	}
	slog.Warn("检测已连接服务器失败", "server_url", state.baseURL.String(), "error", err)
	return appservice.InstallationStatus{}, appservice.UnavailableError(meta, i18n.ErrorServerConnectionFailed, nil)
}

// Login 校验账号密码并建立原生端登录会话。
func (b *Backend) Login(ctx context.Context, meta appservice.RequestMeta, input appservice.LoginInput) (appservice.Auth, error) {
	return b.establishSession(ctx, meta, "/auth/login", input)
}

// Register 注册本地账号并建立原生端登录会话。
func (b *Backend) Register(ctx context.Context, meta appservice.RequestMeta, input appservice.RegisterInput) (appservice.Auth, error) {
	return b.establishSession(ctx, meta, "/auth/register", input)
}

// establishSession 调用登录接口并保存原生端登录凭据，返回给前端的结果不含令牌。
func (b *Backend) establishSession(ctx context.Context, meta appservice.RequestMeta, path string, input any) (appservice.Auth, error) {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	var output appservice.Auth
	if err := b.do(ctx, meta, http.MethodPost, path, nil, input, &output); err != nil {
		return appservice.Auth{}, err
	}
	state := b.connection.currentState()
	if err := b.sessions.Establish(ctx, clientsession.Credential{
		ServerURL: state.baseURL.String(),
		AccountID: output.Account.ID,
		Token:     output.Token,
		ExpiresAt: output.ExpiresAt,
	}); err != nil {
		slog.Warn("保存原生端登录凭据失败", "server_url", state.baseURL.String(), "account_id", output.Account.ID, "error", err)
		return appservice.Auth{}, appservice.FailedError(meta, i18n.ErrorLoginFailed, err)
	}
	// 新登录会话不沿用上一会话的实时连接。
	b.realtime.disconnectAll()
	return appservice.Auth{Account: output.Account}, nil
}

// LoadIdentity 读取当前账号在请求目标工作区中的成员身份；原生端不记录当前工作区，各请求自行携带目标工作区。
func (b *Backend) LoadIdentity(ctx context.Context, meta appservice.RequestMeta) (appservice.Identity, error) {
	var output appservice.Identity
	if err := b.do(ctx, meta, http.MethodGet, "/auth/identity", nil, nil, &output); err != nil {
		return appservice.Identity{}, err
	}
	return output, nil
}

// Logout 退出远程会话并清除原生端登录凭据。
func (b *Backend) Logout(ctx context.Context, meta appservice.RequestMeta) error {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	remoteErr := b.do(ctx, meta, http.MethodPost, "/auth/logout", nil, nil, nil)
	b.realtime.disconnectAll()
	// 远程请求取消后仍清除本地凭据。
	if err := b.sessions.Clear(context.WithoutCancel(ctx)); err != nil {
		slog.Warn("清理原生端登录凭据失败", "error", err)
		return appservice.FailedError(meta, i18n.ErrorLogoutFailed, err)
	}
	return remoteErr
}

// ServerURL 返回当前配置的服务器地址。
func (b *Backend) ServerURL(_ context.Context, _ appservice.RequestMeta) (string, error) {
	state := b.connection.currentState()
	if state == nil {
		return "", nil
	}
	return state.baseURL.String(), nil
}

// ProbeServer 检测服务器并返回安装状态。
func (b *Backend) ProbeServer(ctx context.Context, meta appservice.RequestMeta, serverURL string) (appservice.InstallationStatus, error) {
	state, status, err := b.inspectServer(ctx, meta, serverURL)
	if err != nil {
		return appservice.InstallationStatus{}, err
	}
	slog.Info("已检测到服务器", "server_url", state.baseURL.String())
	return status, nil
}

// ConnectServer 验证并保存服务器地址；服务器要求升级客户端时保存地址后返回需要升级的会话错误。
func (b *Backend) ConnectServer(ctx context.Context, meta appservice.RequestMeta, serverURL string) error {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	state, status, err := b.inspectServer(ctx, meta, serverURL)
	if err != nil {
		return err
	}
	current := b.connection.currentState()
	changed := current == nil || current.baseURL.String() != state.baseURL.String()
	if changed {
		if err := b.sessions.Clear(ctx); err != nil {
			slog.Warn("切换服务器前清理登录凭据失败", "server_url", state.baseURL.String(), "error", err)
			return appservice.FailedError(meta, i18n.ErrorServerConnectionSaveFailed, err)
		}
		b.realtime.disconnectAll()
	}
	if err := b.connection.store.SetServerURL(ctx, state.baseURL.String()); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("保存服务器配置失败", "server_url", state.baseURL.String(), "error", err)
		return appservice.FailedError(meta, i18n.ErrorServerConnectionSaveFailed, err)
	}
	b.connection.mu.Lock()
	b.connection.state = state
	b.connection.mu.Unlock()
	slog.Info("服务器连接成功", "server_url", state.baseURL.String(), "changed", changed)
	if status.ClientOutdated() {
		slog.Info("服务器要求升级客户端", "server_url", state.baseURL.String(), "min_client_api_version", status.MinClientAPIVersion)
		return appservice.ClientUpgradeError(meta)
	}
	return nil
}

// inspectServer 校验地址、读取远程安装状态并确认服务器接口版本不低于本端要求。
func (b *Backend) inspectServer(ctx context.Context, meta appservice.RequestMeta, serverURL string) (*remoteState, appservice.InstallationStatus, error) {
	parsed, err := parseServerURL(serverURL)
	if err != nil {
		var validationError *serverURLValidationError
		if !errors.As(err, &validationError) {
			return nil, appservice.InstallationStatus{}, fmt.Errorf("parse enterprise server URL: %w", err)
		}
		return nil, appservice.InstallationStatus{}, appservice.InvalidError(meta, i18n.ErrorServerURLInvalid, map[string]i18n.Key{"serverUrl": validationError.messageKey})
	}
	state := newRemoteState(parsed)
	status, err := probeServer(ctx, state)
	if err != nil {
		if ctx.Err() != nil {
			return nil, appservice.InstallationStatus{}, ctx.Err()
		}
		slog.Warn("验证服务器失败", "server_url", parsed.String(), "error", err)
		return nil, appservice.InstallationStatus{}, appservice.UnavailableError(meta, i18n.ErrorServerUnavailable, map[string]i18n.Key{"serverUrl": i18n.FieldServerURLUnrecognized})
	}
	if !status.Installed {
		slog.Info("服务器尚未完成首次安装", "server_url", parsed.String())
		return nil, appservice.InstallationStatus{}, appservice.InvalidError(meta, i18n.ErrorServerInitializationRequired, nil)
	}
	if status.ServerOutdated() {
		slog.Info("服务器接口版本过旧", "server_url", parsed.String(), "server_api_version", status.APIVersion)
		return nil, appservice.InstallationStatus{}, appservice.InvalidError(meta, i18n.ErrorServerOutdated, nil)
	}
	return state, status, nil
}

// do 向已连接的服务器发送 HTTP 请求，解码 JSON 响应并按当前连接地址补全本地存储文件地址。
func (b *Backend) do(ctx context.Context, meta appservice.RequestMeta, method, path string, query url.Values, input, output any) error {
	response, err := b.send(ctx, meta, method, path, query, input)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if output == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(output); err != nil {
		slog.Warn("解析服务器响应失败", "method", method, "path", path, "status", response.StatusCode, "error", err)
		return appservice.UnavailableError(meta, i18n.ErrorServerConnectionFailed, nil)
	}
	if state := b.connection.currentState(); state != nil {
		resolveFileURLs(output, state.baseURL)
	}
	return nil
}

// send 向已连接的服务器发送 HTTP 请求，返回状态码为 2xx 的响应，调用方负责关闭响应体。
func (b *Backend) send(ctx context.Context, meta appservice.RequestMeta, method, path string, query url.Values, input any) (*http.Response, error) {
	return b.sendVia(ctx, meta, false, method, path, query, input)
}

// sendVia 向已连接的服务器发送 HTTP 请求；contextDeadline 为 true 时请求期限只由 ctx 控制，否则使用普通接口的请求时限。
func (b *Backend) sendVia(ctx context.Context, meta appservice.RequestMeta, contextDeadline bool, method, path string, query url.Values, input any) (*http.Response, error) {
	state := b.connection.currentState()
	if state == nil {
		return nil, appservice.SessionError(meta, appservice.SessionStateConnect, i18n.ErrorServerConnectionRequired)
	}
	credential, authenticated := b.sessions.Current(ctx, state.baseURL.String())
	// 界面请求不携带令牌，由原生端附加当前登录会话；本机后台任务给出发起时的令牌，
	// 该令牌已不是当前服务器上的当前会话（换了账号或服务器）时不发出请求，令牌不会发往其他服务器，结果也不会归到另一个会话。
	if meta.Token != "" && (!authenticated || credential.Token != meta.Token) {
		return nil, ErrSessionChanged
	}
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode remote request: %w", err)
		}
		body = bytes.NewReader(payload)
	}
	rawQuery := ""
	if query != nil {
		rawQuery = query.Encode()
	}
	endpoint := remoteEndpoint(state.baseURL, path, rawQuery)
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, appservice.FailedError(meta, i18n.ErrorRemoteRequestCreateFailed, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", string(meta.Locale))
	request.Header.Set(appservice.ClientAPIVersionHeader, strconv.Itoa(appservice.APIVersion))
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+credential.Token)
	}
	if meta.WorkspaceID != "" {
		request.Header.Set(appservice.WorkspaceHeader, meta.WorkspaceID)
	}
	client := state.client
	if contextDeadline {
		client = &http.Client{Transport: state.client.Transport}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("服务器请求失败", "server_url", state.baseURL.String(), "method", method, "path", path, "error", err)
		return nil, appservice.UnavailableError(meta, i18n.ErrorServerConnectionFailed, nil)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		var rejected *clientsession.Credential
		if authenticated {
			rejected = &credential
		}
		return nil, b.remoteError(ctx, state, rejected, response, method, path)
	}
	return response, nil
}

// remoteError 解析服务器错误响应；登录会话失效且请求携带了凭据时清除本地凭据。
func (b *Backend) remoteError(ctx context.Context, state *remoteState, credential *clientsession.Credential, response *http.Response, method, path string) error {
	var payload errorBody
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&payload); err != nil {
		slog.Warn("解析服务器错误响应失败", "server_url", state.baseURL.String(), "method", method, "path", path, "status", response.StatusCode, "error", err)
		return &appservice.Error{Kind: appservice.ErrorKindFailed, Message: http.StatusText(response.StatusCode)}
	}
	sessionState := payload.Error.State
	if sessionState == appservice.SessionStateSetup {
		slog.Info("远端要求初始化，改为连接服务器")
		sessionState = appservice.SessionStateConnect
	}
	if sessionState == appservice.SessionStateLogin && credential != nil {
		if err := b.sessions.ClearIfCurrent(ctx, *credential); err != nil {
			slog.Warn("登录凭据失效后清理本地会话失败", "server_url", state.baseURL.String(), "error", err)
		}
	}
	return &appservice.Error{Kind: payload.Error.Kind, State: sessionState, Message: payload.Error.Message, Fields: payload.Error.Fields, Reason: payload.Error.Reason}
}

// setQuery 在值非空时写入查询参数。
func setQuery(query url.Values, name, value string) {
	if value != "" {
		query.Set(name, value)
	}
}

// setTrueQuery 在布尔值为真时写入 true。
func setTrueQuery(query url.Values, name string, value bool) {
	if value {
		query.Set(name, "true")
	}
}

// setOptionalQuery 在指针非空时写入查询参数。
func setOptionalQuery[T ~string](query url.Values, name string, value *T) {
	if value != nil {
		setQuery(query, name, string(*value))
	}
}

// setListQuery 按顺序写入重复查询参数。
func setListQuery[T ~string](query url.Values, name string, values []T) {
	for _, value := range values {
		query.Add(name, string(value))
	}
}

// setPositiveQuery 在值为正数时写入查询参数。
func setPositiveQuery(query url.Values, name string, value int) {
	if value > 0 {
		query.Set(name, strconv.Itoa(value))
	}
}
