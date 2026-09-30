//go:build !server && !ios && !android

// Package devicehost 把桌面端本机设备注册到当前登录账号所在的各个工作区，并执行派发给本机的 Agent 运行。
package devicehost

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/clientsession"
	"github.com/runforyou-ai/cervi/internal/domain"
)

const (
	// idleInterval 是无事可做时重新检查登录状态的间隔。
	idleInterval = 5 * time.Minute
	// initialRetryInterval 是注册失败后首次重试的间隔，连续失败按倍数退避到 idleInterval。
	initialRetryInterval = 5 * time.Second
	// registerTimeout 是单次注册请求的时限。
	registerTimeout = 30 * time.Second
	// maxNameRunes 是上报设备名称的最大字符数，与服务端校验上限一致；超长主机名截断后上报。
	maxNameRunes = 100
)

// Store 持久化本机安装标识与各服务器上每个账号在各工作区的设备注册结果。
type Store interface {
	// DeviceInstallID 读取本机安装标识，尚未生成时创建并保存。
	DeviceInstallID(ctx context.Context) (string, error)
	// LoadDeviceRegistrations 读取本机在指定服务器上为指定账号在各工作区注册的设备编号，按工作区编号索引。
	LoadDeviceRegistrations(ctx context.Context, serverURL, accountID string) (map[string]string, error)
	// SaveDeviceRegistration 保存本机在指定服务器上为指定账号在指定工作区注册的设备编号。
	SaveDeviceRegistration(ctx context.Context, serverURL, accountID, organizationID, deviceID string) error
	// DeleteDeviceRegistration 删除本机在指定服务器上为指定账号在指定工作区的注册结果。
	DeleteDeviceRegistration(ctx context.Context, serverURL, accountID, organizationID string) error
}

// Client 是设备注册使用的服务端调用。
type Client interface {
	// ServerURL 返回当前配置的服务器地址。
	ServerURL(context.Context, appservice.RequestMeta) (string, error)
	// ListWorkspaces 读取当前账号作为有效成员可进入的工作区。
	ListWorkspaces(context.Context, appservice.RequestMeta) (appservice.WorkspaceList, error)
	// RegisterDevice 在请求目标工作区中注册当前成员的本机设备。
	RegisterDevice(context.Context, appservice.RequestMeta, appservice.DeviceRegistrationInput) (appservice.Device, error)
}

// Registrar 在登录会话建立后把本机设备注册到账号所在的每个工作区；每个登录会话在每个工作区注册一次，
// 账号不是其有效成员的工作区删除本机注册结果。注册与读取工作区列表都绑定发起时的登录会话，会话已更换时请求不发出，结果只归属该会话的账号。
type Registrar struct {
	store    Store
	client   Client
	sessions *clientsession.Manager
	name     string
	platform domain.DevicePlatform

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	// registerMu 串行化注册循环与按需注册，同一工作区不会并发注册。
	registerMu sync.Mutex

	mu sync.Mutex
	// registered 按工作区记录已完成的注册，登录会话变化后重新注册。
	registered map[string]registration
	// sequence 是已完成注册的递增序号，同步据此识别读取工作区列表之后才完成的注册。
	sequence uint64

	observerMu sync.Mutex
	observers  []func()
}

// New 创建桌面端设备注册器；当前平台不支持本机设备时返回 nil。
func New(store Store, client Client, sessions *clientsession.Manager) *Registrar {
	platform, ok := currentPlatform()
	if !ok {
		slog.Info("当前平台不注册本机设备", "os", runtime.GOOS)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Registrar{
		store:      store,
		client:     client,
		sessions:   sessions,
		name:       deviceName(),
		platform:   platform,
		ctx:        ctx,
		cancel:     cancel,
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		registered: map[string]registration{},
	}
}

// Start 订阅登录凭据变化并开始注册循环。
func (r *Registrar) Start() {
	if r == nil {
		return
	}
	r.sessions.Subscribe(r.Wake)
	go r.run()
}

// Stop 取消进行中的注册，结束注册循环并等待其退出。
func (r *Registrar) Stop() {
	if r == nil {
		return
	}
	r.cancel()
	<-r.done
}

// Wake 请求立即同步一次各工作区的注册，循环正在运行时保留一次待处理信号。
func (r *Registrar) Wake() {
	if r == nil {
		return
	}
	signal(r.wake)
}

// Subscribe 登记本机注册结果变化的观察者；观察者必须尽快返回。
func (r *Registrar) Subscribe(observer func()) {
	r.observerMu.Lock()
	defer r.observerMu.Unlock()
	r.observers = append(r.observers, observer)
}

// notify 通知全部观察者本机注册结果已变化。
func (r *Registrar) notify() {
	r.observerMu.Lock()
	observers := slices.Clone(r.observers)
	r.observerMu.Unlock()
	for _, observer := range observers {
		observer()
	}
}

// CurrentDevice 返回本机在请求目标工作区中为当前账号注册的设备；尚未注册时立即注册一次，失败时返回空设备编号并交给注册循环重试。
func (r *Registrar) CurrentDevice(ctx context.Context, meta appservice.RequestMeta) (appservice.LocalDevice, error) {
	if r == nil || meta.WorkspaceID == "" {
		return appservice.LocalDevice{}, nil
	}
	serverURL, credential, ok := r.currentSession(ctx, meta)
	if !ok {
		return appservice.LocalDevice{}, nil
	}
	session, found, err := r.deviceSession(ctx, serverURL, credential, meta.WorkspaceID)
	if err != nil || found {
		return appservice.LocalDevice{DeviceID: session.deviceID}, err
	}
	// 新加入或新创建的工作区不等注册循环的下一轮。
	if registered, err := r.ensure(ctx, serverURL, credential, meta.WorkspaceID); err != nil {
		slog.Warn("注册本机设备失败", "server_url", serverURL, "organization_id", meta.WorkspaceID, "error", err)
		r.Wake()
		return appservice.LocalDevice{}, nil
	} else if registered {
		r.notify()
	}
	session, _, err = r.deviceSession(ctx, serverURL, credential, meta.WorkspaceID)
	return appservice.LocalDevice{DeviceID: session.deviceID}, err
}

// registration 是一次已完成的工作区注册。
type registration struct {
	session  string
	sequence uint64
}

// deviceSession 是当前登录会话及本机在其中一个工作区注册的设备。
type deviceSession struct {
	serverURL   string
	credential  clientsession.Credential
	workspaceID string
	deviceID    string
}

// key 标识一个工作区中的设备登录会话，换服、换账号、重新登录或重新注册后取值变化。
func (s deviceSession) key() string {
	return sessionKey(s.serverURL, s.credential) + "\n" + s.workspaceID + "\n" + s.deviceID
}

// meta 返回以本机设备身份访问该工作区的请求信息。
func (s deviceSession) meta() appservice.RequestMeta {
	return appservice.RequestMeta{DeviceID: s.deviceID, WorkspaceID: s.workspaceID}
}

// deviceSessions 返回当前登录会话在各工作区已注册的本机设备，按工作区编号排序；尚未登录时返回空列表。
func (r *Registrar) deviceSessions(ctx context.Context) ([]deviceSession, error) {
	serverURL, credential, ok := r.currentSession(ctx, appservice.RequestMeta{})
	if !ok {
		return nil, nil
	}
	devices, err := r.store.LoadDeviceRegistrations(ctx, serverURL, credential.AccountID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("load device registrations: %w", err)
	}
	sessions := make([]deviceSession, 0, len(devices))
	for _, workspaceID := range slices.Sorted(maps.Keys(devices)) {
		sessions = append(sessions, deviceSession{serverURL: serverURL, credential: credential, workspaceID: workspaceID, deviceID: devices[workspaceID]})
	}
	return sessions, nil
}

// deviceSession 返回当前登录会话在指定工作区已注册的本机设备。
func (r *Registrar) deviceSession(ctx context.Context, serverURL string, credential clientsession.Credential, workspaceID string) (deviceSession, bool, error) {
	devices, err := r.store.LoadDeviceRegistrations(ctx, serverURL, credential.AccountID)
	if err != nil {
		if ctx.Err() != nil {
			return deviceSession{}, false, ctx.Err()
		}
		return deviceSession{}, false, fmt.Errorf("load device registrations: %w", err)
	}
	deviceID, found := devices[workspaceID]
	if !found {
		return deviceSession{}, false, nil
	}
	return deviceSession{serverURL: serverURL, credential: credential, workspaceID: workspaceID, deviceID: deviceID}, true, nil
}

// run 在唤醒信号和重试间隔上同步注册，直到注册器停止；同步失败按退避缩短下次尝试的等待。
func (r *Registrar) run() {
	defer close(r.done)
	backoff := initialRetryInterval
	for {
		wait := idleInterval
		if r.register() {
			backoff = initialRetryInterval
		} else {
			wait = backoff
			backoff = min(backoff*2, idleInterval)
		}
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		case <-time.After(wait):
		}
	}
}

// register 在已登录时读取账号的工作区，为尚未注册的工作区上报本机设备，并删除账号不是其有效成员的工作区的注册结果；
// 返回本次是否无需尽快重试。
func (r *Registrar) register() bool {
	ctx, cancel := context.WithTimeout(r.ctx, registerTimeout)
	defer cancel()
	serverURL, credential, ok := r.currentSession(ctx, appservice.RequestMeta{})
	if !ok {
		return true
	}
	// 列表只反映读取时的成员关系，之后才完成的注册（如刚打开的新工作区）不按这份列表删除。
	snapshot := r.currentSequence()
	workspaces, err := r.client.ListWorkspaces(ctx, appservice.RequestMeta{Token: credential.Token})
	if err != nil {
		slog.Warn("读取账号的工作区失败，暂不同步本机设备注册", "server_url", serverURL, "account_id", credential.AccountID, "error", err)
		return false
	}
	changed, complete := false, true
	current := map[string]bool{}
	for _, workspace := range workspaces.Items {
		current[workspace.ID] = true
		registered, err := r.ensure(ctx, serverURL, credential, workspace.ID)
		if err != nil {
			slog.Warn("注册本机设备失败", "server_url", serverURL, "organization_id", workspace.ID, "error", err)
			complete = false
			continue
		}
		changed = changed || registered
	}
	removed, err := r.forgetOthers(ctx, serverURL, credential, current, snapshot)
	if err != nil {
		slog.Warn("清理本机设备注册结果失败", "server_url", serverURL, "account_id", credential.AccountID, "error", err)
		complete = false
	}
	if changed || removed {
		r.notify()
	}
	return complete
}

// ensure 在当前登录会话尚未在指定工作区注册时上报本机设备并保存结果，返回本次是否新注册。
func (r *Registrar) ensure(ctx context.Context, serverURL string, credential clientsession.Credential, workspaceID string) (bool, error) {
	r.registerMu.Lock()
	defer r.registerMu.Unlock()
	session := sessionKey(serverURL, credential)
	if r.registeredFor(workspaceID, session) {
		return false, nil
	}
	installID, err := r.store.DeviceInstallID(ctx)
	if err != nil {
		return false, fmt.Errorf("load install ID: %w", err)
	}
	device, err := r.client.RegisterDevice(ctx, appservice.RequestMeta{Token: credential.Token, WorkspaceID: workspaceID}, appservice.DeviceRegistrationInput{
		InstallID: installID, Name: r.name, Platform: appservice.DevicePlatform(r.platform),
	})
	if err != nil {
		return false, err
	}
	if err := r.store.SaveDeviceRegistration(ctx, serverURL, credential.AccountID, workspaceID, device.ID); err != nil {
		return false, fmt.Errorf("save device registration: %w", err)
	}
	r.markRegistered(workspaceID, session)
	slog.Info("本机设备已注册", "server_url", serverURL, "account_id", credential.AccountID, "organization_id", workspaceID, "device_id", device.ID, "name", device.Name)
	return true, nil
}

// forgetOthers 删除账号不在其中的工作区的本机注册结果，跳过序号 snapshot 之后在本登录会话中完成的注册，返回是否有删除。
func (r *Registrar) forgetOthers(ctx context.Context, serverURL string, credential clientsession.Credential, current map[string]bool, snapshot uint64) (bool, error) {
	r.registerMu.Lock()
	defer r.registerMu.Unlock()
	devices, err := r.store.LoadDeviceRegistrations(ctx, serverURL, credential.AccountID)
	if err != nil {
		return false, err
	}
	session := sessionKey(serverURL, credential)
	removed := false
	for workspaceID := range devices {
		if current[workspaceID] || r.registeredAfter(workspaceID, session, snapshot) {
			continue
		}
		if err := r.store.DeleteDeviceRegistration(ctx, serverURL, credential.AccountID, workspaceID); err != nil {
			return removed, err
		}
		r.mu.Lock()
		delete(r.registered, workspaceID)
		r.mu.Unlock()
		removed = true
		slog.Info("账号不是该工作区的有效成员，删除本机设备注册结果", "server_url", serverURL, "account_id", credential.AccountID, "organization_id", workspaceID)
	}
	return removed, nil
}

// currentSession 返回当前服务器地址及有效的登录凭据，尚未登录时返回 false。
func (r *Registrar) currentSession(ctx context.Context, meta appservice.RequestMeta) (string, clientsession.Credential, bool) {
	serverURL, err := r.client.ServerURL(ctx, meta)
	if err != nil || serverURL == "" {
		return "", clientsession.Credential{}, false
	}
	credential, ok := r.sessions.Current(ctx, serverURL)
	if !ok {
		return "", clientsession.Credential{}, false
	}
	return serverURL, credential, true
}

// registeredFor 判断指定登录会话是否已在指定工作区完成注册。
func (r *Registrar) registeredFor(workspaceID, session string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registered[workspaceID].session == session
}

// registeredAfter 判断指定登录会话在指定工作区的注册是否在序号 snapshot 之后完成。
func (r *Registrar) registeredAfter(workspaceID, session string, snapshot uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	registered := r.registered[workspaceID]
	return registered.session == session && registered.sequence > snapshot
}

// currentSequence 返回最近一次完成注册的序号。
func (r *Registrar) currentSequence() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sequence
}

// markRegistered 记录指定登录会话已在指定工作区完成注册。
func (r *Registrar) markRegistered(workspaceID, session string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sequence++
	r.registered[workspaceID] = registration{session: session, sequence: r.sequence}
}

// sessionKey 标识一个账号登录会话，换服、换账号或重新登录后取值变化。
func sessionKey(serverURL string, credential clientsession.Credential) string {
	return serverURL + "\n" + credential.AccountID + "\n" + credential.Token
}

// currentPlatform 返回当前运行平台对应的设备平台。
func currentPlatform() (domain.DevicePlatform, bool) {
	switch runtime.GOOS {
	case "darwin":
		return domain.DevicePlatformMacOS, true
	case "windows":
		return domain.DevicePlatformWindows, true
	case "linux":
		return domain.DevicePlatformLinux, true
	}
	return "", false
}

// deviceName 返回本机名称，读取失败时使用平台名称，超出服务端上限时截断。
func deviceName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		slog.Warn("读取本机名称失败", "error", err)
		return runtime.GOOS
	}
	if runes := []rune(hostname); len(runes) > maxNameRunes {
		return string(runes[:maxNameRunes])
	}
	return hostname
}
