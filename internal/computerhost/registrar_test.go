//go:build !server && !ios && !android

package computerhost

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
	desktopmodels "github.com/runforyou-ai/luway/internal/storage/desktop/models"
)

// stubStore 在内存中保存本机安装标识与电脑注册结果。
type stubStore struct {
	installID string
	// registrations 按「服务器|账号|工作区」保存电脑编号。
	registrations map[string]string
}

// ComputerInstallID 返回固定的本机安装标识。
func (s *stubStore) ComputerInstallID(context.Context) (string, error) { return s.installID, nil }

// LoadComputerRegistrations 读取内存中指定服务器与账号在各工作区的电脑注册结果。
func (s *stubStore) LoadComputerRegistrations(_ context.Context, serverURL, accountID string) (map[string]desktopmodels.ComputerRegistration, error) {
	computers := map[string]desktopmodels.ComputerRegistration{}
	for key, computerID := range s.registrations {
		if organizationID, ok := strings.CutPrefix(key, serverURL+"|"+accountID+"|"); ok {
			computers[organizationID] = desktopmodels.ComputerRegistration{
				ServerURL: serverURL, AccountID: accountID, OrganizationID: organizationID, ComputerID: computerID, Credential: "credential-" + computerID,
			}
		}
	}
	return computers, nil
}

// ListComputerRegistrations 读取内存中全部电脑注册结果。
func (s *stubStore) ListComputerRegistrations(context.Context) ([]desktopmodels.ComputerRegistration, error) {
	registrations := make([]desktopmodels.ComputerRegistration, 0, len(s.registrations))
	for key, computerID := range s.registrations {
		parts := strings.SplitN(key, "|", 3)
		registrations = append(registrations, desktopmodels.ComputerRegistration{ServerURL: parts[0], AccountID: parts[1], OrganizationID: parts[2], ComputerID: computerID})
	}
	return registrations, nil
}

// SaveComputerRegistration 保存内存中的电脑编号。
func (s *stubStore) SaveComputerRegistration(_ context.Context, registration desktopmodels.ComputerRegistration) error {
	s.registrations[registration.ServerURL+"|"+registration.AccountID+"|"+registration.OrganizationID] = registration.ComputerID
	return nil
}

// DeleteComputerRegistration 删除内存中的电脑编号，电脑编号非空时只删除该电脑的注册。
func (s *stubStore) DeleteComputerRegistration(_ context.Context, serverURL, accountID, organizationID, computerID string) error {
	key := serverURL + "|" + accountID + "|" + organizationID
	if computerID == "" || s.registrations[key] == computerID {
		delete(s.registrations, key)
	}
	return nil
}

// stubClient 返回预设的工作区，记录各工作区的注册调用并返回「computer-<工作区>」形式的电脑编号。
type stubClient struct {
	mu         sync.Mutex
	serverURL  string
	workspaces []string
	// failures 按工作区编号给出注册失败的原因。
	failures map[string]error
	calls    []string
	// tokens 按调用顺序记录读取工作区列表与注册请求携带的令牌。
	tokens []string
	// onList 非空时在返回工作区列表前调用，模拟读取期间发生的其他操作。
	onList func()
}

// ServerURL 返回预设的服务器地址。
func (c *stubClient) ServerURL(context.Context, appservice.RequestMeta) (string, error) {
	return c.serverURL, nil
}

// ListWorkspaces 返回预设的工作区。
func (c *stubClient) ListWorkspaces(_ context.Context, meta appservice.RequestMeta) (appservice.WorkspaceList, error) {
	c.mu.Lock()
	c.tokens = append(c.tokens, meta.Token)
	onList := c.onList
	c.onList = nil
	c.mu.Unlock()
	if onList != nil {
		onList()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	list := appservice.WorkspaceList{Items: []appservice.Workspace{}}
	for _, id := range c.workspaces {
		list.Items = append(list.Items, appservice.Workspace{ID: id, Name: id, Slug: id})
	}
	return list, nil
}

// RegisterComputer 记录一次注册调用及其目标工作区。
func (c *stubClient) RegisterComputer(_ context.Context, meta appservice.RequestMeta, input appservice.ComputerRegistrationInput) (appservice.ComputerRegistration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, meta.WorkspaceID)
	c.tokens = append(c.tokens, meta.Token)
	if input.InstallID != "install-1" {
		return appservice.ComputerRegistration{}, errors.New("unexpected install ID " + input.InstallID)
	}
	if failure := c.failures[meta.WorkspaceID]; failure != nil {
		return appservice.ComputerRegistration{}, failure
	}
	return appservice.ComputerRegistration{Computer: appservice.Computer{ID: "computer-" + meta.WorkspaceID, Name: input.Name}, Credential: "credential"}, nil
}

// registeredCalls 返回注册调用的目标工作区并清空记录。
func (c *stubClient) registeredCalls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls := c.calls
	c.calls = nil
	slices.Sort(calls)
	return calls
}

// stubSessionStore 提供一份可替换的原生端登录凭据。
type stubSessionStore struct {
	credential clientsession.Credential
	found      bool
}

// LoadClientSession 返回当前登录凭据。
func (s *stubSessionStore) LoadClientSession(context.Context) (clientsession.Credential, bool, error) {
	return s.credential, s.found, nil
}

// SaveClientSession 保存当前登录凭据。
func (s *stubSessionStore) SaveClientSession(_ context.Context, credential clientsession.Credential) error {
	s.credential, s.found = credential, true
	return nil
}

// DeleteClientSession 删除当前登录凭据。
func (s *stubSessionStore) DeleteClientSession(context.Context) error {
	s.credential, s.found = clientsession.Credential{}, false
	return nil
}

// newTestRegistrar 创建使用内存依赖的注册器。
func newTestRegistrar(t *testing.T, store *stubStore, client *stubClient) (*Registrar, *clientsession.Manager) {
	t.Helper()
	sessions, err := clientsession.NewManager(context.Background(), &stubSessionStore{})
	if err != nil {
		t.Fatal(err)
	}
	registrar := New(store, client, sessions)
	if registrar == nil {
		t.Skip("当前平台不注册为电脑")
	}
	return registrar, sessions
}

// credentialFor 构造指定账号和令牌的登录凭据。
func credentialFor(serverURL, accountID, token string) clientsession.Credential {
	return clientsession.Credential{ServerURL: serverURL, AccountID: accountID, Token: token, ExpiresAt: time.Now().Add(time.Hour)}
}

const testServerURL = "https://app.example.com"

// TestRegisterSkipsWithoutSession 验证尚未登录时不注册电脑。
func TestRegisterSkipsWithoutSession(t *testing.T) {
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1"}}
	registrar, _ := newTestRegistrar(t, &stubStore{installID: "install-1", registrations: map[string]string{}}, client)

	registrar.register()

	if calls := client.registeredCalls(); len(calls) != 0 {
		t.Fatalf("未登录时的注册调用 = %v", calls)
	}
}

// TestRegisterEachWorkspaceOnce 验证每个工作区只注册一次，重新登录后沿用已保存的注册结果。
func TestRegisterEachWorkspaceOnce(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1", "org-2"}}
	registrar, sessions := newTestRegistrar(t, store, client)
	notified := 0
	registrar.Subscribe(func() { notified++ })

	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	registrar.register()
	registrar.register()
	if calls := client.registeredCalls(); !slices.Equal(calls, []string{"org-1", "org-2"}) {
		t.Fatalf("同一登录会话的注册调用 = %v", calls)
	}
	if notified != 1 {
		t.Fatalf("注册结果变化通知次数 = %d", notified)
	}
	want := map[string]string{
		testServerURL + "|account-1|org-1": "computer-org-1",
		testServerURL + "|account-1|org-2": "computer-org-2",
	}
	if !maps.Equal(store.registrations, want) {
		t.Fatalf("保存的注册结果 = %v", store.registrations)
	}

	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-2")); err != nil {
		t.Fatal(err)
	}
	registrar.register()
	if calls := client.registeredCalls(); len(calls) != 0 {
		t.Fatalf("重新登录后的注册调用 = %v", calls)
	}
}

// TestForgetRegistersAgainInNextSession 验证凭据失效删除注册结果后，同一登录会话不再注册该工作区，下一个登录会话重新注册。
func TestForgetRegistersAgainInNextSession(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1"}}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	registrar.register()
	client.registeredCalls()

	registrar.Forget(ctx, desktopmodels.ComputerRegistration{ServerURL: testServerURL, AccountID: "account-1", OrganizationID: "org-1", ComputerID: "computer-org-1"})
	if len(store.registrations) != 0 {
		t.Fatalf("凭据失效后的注册结果 = %v", store.registrations)
	}
	registrar.register()
	if calls := client.registeredCalls(); len(calls) != 0 {
		t.Fatalf("同一登录会话的重新注册调用 = %v", calls)
	}
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-2")); err != nil {
		t.Fatal(err)
	}
	registrar.register()
	if calls := client.registeredCalls(); !slices.Equal(calls, []string{"org-1"}) {
		t.Fatalf("下一个登录会话的注册调用 = %v", calls)
	}
}

// TestRegisterRetriesFailedWorkspace 验证某个工作区注册失败时其他工作区照常注册，下次只重试失败的工作区。
func TestRegisterRetriesFailedWorkspace(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1", "org-2"}, failures: map[string]error{"org-2": errors.New("服务器不可达")}}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}

	if registrar.register() {
		t.Fatal("部分工作区注册失败时应尽快重试")
	}
	client.registeredCalls()
	client.failures = nil
	if !registrar.register() {
		t.Fatal("全部注册成功后不需要尽快重试")
	}
	if calls := client.registeredCalls(); !slices.Equal(calls, []string{"org-2"}) {
		t.Fatalf("重试的注册调用 = %v", calls)
	}
	computer, err := registrar.CurrentComputer(ctx, appservice.RequestMeta{WorkspaceID: "org-2"})
	if err != nil || computer.ComputerID != "computer-org-2" {
		t.Fatalf("org-2 的本机电脑 = %#v, err = %v", computer, err)
	}
}

// TestRegisterForgetsLeftWorkspace 验证账号失去某个工作区的有效成员身份后删除该工作区的本机注册结果并通知观察者。
func TestRegisterForgetsLeftWorkspace(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1", "org-2"}}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	registrar.register()
	notified := 0
	registrar.Subscribe(func() { notified++ })

	client.workspaces = []string{"org-1"}
	registrar.register()

	if !maps.Equal(store.registrations, map[string]string{testServerURL + "|account-1|org-1": "computer-org-1"}) {
		t.Fatalf("退出工作区后的注册结果 = %v", store.registrations)
	}
	if notified != 1 {
		t.Fatalf("注册结果变化通知次数 = %d", notified)
	}
}

// TestCurrentComputerRegistersRequestedWorkspace 验证读取尚未注册的工作区的本机电脑时立即注册，未给出工作区时返回空电脑。
func TestCurrentComputerRegistersRequestedWorkspace(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}

	computer, err := registrar.CurrentComputer(ctx, appservice.RequestMeta{})
	if err != nil || computer.ComputerID != "" {
		t.Fatalf("未给出工作区时的本机电脑 = %#v, err = %v", computer, err)
	}
	computer, err = registrar.CurrentComputer(ctx, appservice.RequestMeta{WorkspaceID: "org-new"})
	if err != nil || computer.ComputerID != "computer-org-new" {
		t.Fatalf("新工作区的本机电脑 = %#v, err = %v", computer, err)
	}
	if _, err := registrar.CurrentComputer(ctx, appservice.RequestMeta{WorkspaceID: "org-new"}); err != nil {
		t.Fatal(err)
	}
	if calls := client.registeredCalls(); !slices.Equal(calls, []string{"org-new"}) {
		t.Fatalf("注册调用 = %v", calls)
	}
}

// TestCurrentComputerSeparatesAccounts 验证同一台机器切换账号后不返回上一账号的电脑编号。
func TestCurrentComputerSeparatesAccounts(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1"}}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	registrar.register()

	// 换成同一工作区的另一个账号，该账号的注册尚未成功。
	client.failures = map[string]error{"org-1": errors.New("服务器不可达")}
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-2", "token-2")); err != nil {
		t.Fatal(err)
	}
	registrar.register()

	computer, err := registrar.CurrentComputer(ctx, appservice.RequestMeta{WorkspaceID: "org-1"})
	if err != nil {
		t.Fatal(err)
	}
	if computer.ComputerID != "" {
		t.Fatalf("切换账号后的本机电脑编号 = %q", computer.ComputerID)
	}
}

// TestRegisterKeepsWorkspaceRegisteredDuringSync 验证读取工作区列表期间按需注册的新工作区，不会被这份较早的列表当作已退出删除。
func TestRegisterKeepsWorkspaceRegisteredDuringSync(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1"}}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	client.onList = func() {
		if computer, err := registrar.CurrentComputer(ctx, appservice.RequestMeta{WorkspaceID: "org-new"}); err != nil || computer.ComputerID != "computer-org-new" {
			t.Errorf("同步期间按需注册的本机电脑 = %#v, err = %v", computer, err)
		}
	}

	registrar.register()

	want := map[string]string{
		testServerURL + "|account-1|org-1":   "computer-org-1",
		testServerURL + "|account-1|org-new": "computer-org-new",
	}
	if !maps.Equal(store.registrations, want) {
		t.Fatalf("同步后的注册结果 = %v", store.registrations)
	}
	// 下一轮同步拿到的列表仍不含该工作区时照常删除。
	registrar.register()
	if _, found := store.registrations[testServerURL+"|account-1|org-new"]; found {
		t.Fatalf("账号不在其中的工作区仍保留注册结果: %v", store.registrations)
	}
}

// TestRegisterBindsRequestsToSyncSession 验证同步期间换成另一个账号时，本轮请求仍以发起时的令牌发出，注册结果只保存到发起时的账号名下。
func TestRegisterBindsRequestsToSyncSession(t *testing.T) {
	ctx := context.Background()
	store := &stubStore{installID: "install-1", registrations: map[string]string{}}
	client := &stubClient{serverURL: testServerURL, workspaces: []string{"org-1"}}
	registrar, sessions := newTestRegistrar(t, store, client)
	if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-1", "token-1")); err != nil {
		t.Fatal(err)
	}
	client.onList = func() {
		if err := sessions.Establish(ctx, credentialFor(testServerURL, "account-2", "token-2")); err != nil {
			t.Error(err)
		}
	}

	registrar.register()

	if !slices.Equal(client.tokens, []string{"token-1", "token-1"}) {
		t.Fatalf("本轮请求携带的令牌 = %v", client.tokens)
	}
	if !maps.Equal(store.registrations, map[string]string{testServerURL + "|account-1|org-1": "computer-org-1"}) {
		t.Fatalf("注册结果 = %v", store.registrations)
	}
}
