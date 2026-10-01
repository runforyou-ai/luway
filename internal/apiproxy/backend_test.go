//go:build !server

package apiproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/clientsession"
)

type memoryStore struct {
	serverURL     string
	credential    clientsession.Credential
	credentialSet bool
}

// GetServerURL 返回内存中保存的企业服务器地址。
func (s *memoryStore) GetServerURL(context.Context) (string, error) {
	return s.serverURL, nil
}

// SetServerURL 在内存中保存企业服务器地址。
func (s *memoryStore) SetServerURL(_ context.Context, serverURL string) error {
	s.serverURL = serverURL
	return nil
}

// LoadClientSession 返回内存中保存的原生端登录凭据。
func (s *memoryStore) LoadClientSession(context.Context) (clientsession.Credential, bool, error) {
	return s.credential, s.credentialSet, nil
}

// SaveClientSession 在内存中保存原生端登录凭据。
func (s *memoryStore) SaveClientSession(_ context.Context, credential clientsession.Credential) error {
	s.credential = credential
	s.credentialSet = true
	return nil
}

// DeleteClientSession 删除内存中的原生端登录凭据。
func (s *memoryStore) DeleteClientSession(context.Context) error {
	s.credential = clientsession.Credential{}
	s.credentialSet = false
	return nil
}

// newTestBackend 创建使用内存连接和会话存储的 API Proxy。
func newTestBackend(store *memoryStore) (*Backend, error) {
	sessions, err := clientsession.NewManager(context.Background(), store)
	if err != nil {
		return nil, err
	}
	return NewBackend(store, "", sessions, func(string, string, any) {}, nil)
}

// TestBackendRequiresEnterpriseServer 验证未配置企业服务器时拒绝远程调用。
func TestBackendRequiresEnterpriseServer(t *testing.T) {
	backend, err := newTestBackend(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.LoadInbox(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, appservice.LoadInboxInput{})
	var apiError *appservice.Error
	if !errors.As(err, &apiError) || apiError.State != appservice.SessionStateConnect {
		t.Fatalf("error = %#v, want connect session", err)
	}
}

// TestBackendUsesDefaultServerUntilSaved 验证本机未保存服务器地址时使用内置部署地址且不保存，内置地址无效时等待配置，已保存的地址优先于内置地址。
func TestBackendUsesDefaultServerUntilSaved(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{}
	sessions, err := clientsession.NewManager(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewBackend(store, "https://app.example.com/", sessions, func(string, string, any) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if serverURL, _ := backend.ServerURL(ctx, appservice.RequestMeta{}); serverURL != "https://app.example.com" || store.serverURL != "" {
		t.Fatalf("内置部署地址 = %q，已保存地址 = %q", serverURL, store.serverURL)
	}

	backend, err = NewBackend(store, "app.example.com", sessions, func(string, string, any) {}, nil)
	if err != nil {
		t.Fatalf("内置地址无效时应进入连接页: %v", err)
	}
	if serverURL, _ := backend.ServerURL(ctx, appservice.RequestMeta{}); serverURL != "" {
		t.Fatalf("无效内置地址不应使用: %q", serverURL)
	}

	store.serverURL = "https://saved.example.com"
	backend, err = NewBackend(store, "https://app.example.com", sessions, func(string, string, any) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if serverURL, _ := backend.ServerURL(ctx, appservice.RequestMeta{}); serverURL != "https://saved.example.com" {
		t.Fatalf("已保存地址未优先使用: %q", serverURL)
	}
}

// TestBackendPreservesCancellation 验证远程请求取消原因的透传。
func TestBackendPreservesCancellation(t *testing.T) {
	backend, err := newTestBackend(&memoryStore{serverURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = backend.LoadInbox(ctx, appservice.RequestMeta{}, appservice.LoadInboxInput{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

// TestBackendUnavailablePreservesConnection 验证企业服务器不可用时保留已有连接配置。
func TestBackendUnavailablePreservesConnection(t *testing.T) {
	backend, err := newTestBackend(&memoryStore{serverURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.LoadInbox(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, appservice.LoadInboxInput{})
	var apiError *appservice.Error
	if !errors.As(err, &apiError) || apiError.Kind != appservice.ErrorKindUnavailable || apiError.State != "" {
		t.Fatalf("error = %#v, want unavailable without session state", err)
	}
}

// TestBackendConnectsAndUsesBearerToken 验证类型化远程调用使用 Bearer Token。
func TestBackendConnectsAndUsesBearerToken(t *testing.T) {
	const contactAvatarURL = "/storage/organizations/organization-1/files/019d4e1c-40a5-77dd-82e6-6951f9957ba5.png"
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch strings.TrimPrefix(request.URL.Path, "/company") {
		case "/api/installation/status":
			if request.Header.Get("Authorization") != "" {
				http.Error(writer, "installation status must not use login state", http.StatusBadRequest)
				return
			}
			writeTestJSON(writer, http.StatusOK, map[string]any{"installed": true, "registrationOpen": true})
		case "/api/auth/identity":
			if request.Header.Get("Authorization") != "Bearer test-token" || request.Header.Get(appservice.WorkspaceHeader) != "organization-1" {
				http.Error(writer, "identity requires token and workspace", http.StatusBadRequest)
				return
			}
			writeTestJSON(writer, http.StatusOK, map[string]any{
				"organization": map[string]string{"id": "organization-1", "name": "演示公司", "slug": "app"},
				"user":         map[string]string{"id": "user-1", "organizationId": "organization-1", "email": "admin@example.com"},
			})
		case "/api/inbox":
			if request.Header.Get("Authorization") == "Bearer test-token" && request.Header.Get(appservice.WorkspaceHeader) == "organization-1" {
				writeTestJSON(writer, http.StatusOK, map[string]any{
					"conversations": []map[string]any{{
						"id": "conversation-1", "type": "channel", "direct": nil,
						"service": map[string]any{
							"title": "Telegram 会话", "source": "channel", "audience": "customer", "requesterName": "访客",
							"requesterAvatarUrl": contactAvatarURL,
							"channel":            map[string]any{"type": "telegram", "name": "Telegram"}, "preview": "你好",
							"lastMessageAt": time.Now(), "serviceSessionStatus": "open",
						},
					}},
				})
				return
			}
			writeTestJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{
				"state": "login", "message": "Authentication required.",
			}})
		case "/api/auth/login":
			writeTestJSON(writer, http.StatusOK, map[string]any{
				"account": map[string]string{"id": "account-1", "email": "admin@example.com"},
				"token":   "test-token", "expiresAt": time.Now().Add(time.Hour),
			})
		case "/api/auth/logout":
			if request.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(writer, "unexpected logout request", http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer remote.Close()
	serverURL := remote.URL + "/company"

	store := &memoryStore{}
	backend, err := newTestBackend(store)
	if err != nil {
		t.Fatal(err)
	}
	meta := appservice.RequestMeta{Locale: "zh-CN"}
	status, err := backend.ProbeServer(context.Background(), meta, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.RegistrationOpen {
		t.Fatalf("probe status = %#v", status)
	}
	if store.serverURL != "" {
		t.Fatalf("probe should not save server URL, got %q", store.serverURL)
	}
	if err := backend.ConnectServer(context.Background(), meta, serverURL); err != nil {
		t.Fatal(err)
	}
	if store.serverURL != serverURL {
		t.Fatalf("server URL = %q, want %q", store.serverURL, serverURL)
	}
	if err := backend.ConnectServer(context.Background(), meta, serverURL); err != nil {
		t.Fatal(err)
	}
	status, err = backend.InstallationStatus(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed {
		t.Fatalf("status = %#v", status)
	}
	configuredServerURL, err := backend.ServerURL(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if configuredServerURL != serverURL {
		t.Fatalf("configured server URL = %q, want %q", configuredServerURL, serverURL)
	}
	auth, err := backend.Login(context.Background(), meta, appservice.LoginInput{Email: "admin@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Token != "" || auth.Account.ID != "account-1" {
		t.Fatalf("native auth = %#v", auth)
	}
	status, err = backend.InstallationStatus(context.Background(), meta)
	if err != nil || !status.Installed {
		t.Fatalf("authenticated installation status = %#v, err = %v", status, err)
	}
	if !store.credentialSet || store.credential.Token != "test-token" || store.credential.AccountID != "account-1" {
		t.Fatalf("saved client credential = %#v, found = %v", store.credential, store.credentialSet)
	}
	// 读取工作区成员身份时携带目标工作区，登录凭据不随之变化。
	workspaceMeta := appservice.RequestMeta{Locale: "zh-CN", WorkspaceID: "organization-1"}
	identity, err := backend.LoadIdentity(context.Background(), workspaceMeta)
	if err != nil || identity.Organization.Slug != "app" {
		t.Fatalf("identity = %#v, err = %v", identity, err)
	}
	if store.credential.Token != "test-token" || store.credential.AccountID != "account-1" {
		t.Fatalf("credential after identity = %#v", store.credential)
	}
	if err := backend.ConnectServer(context.Background(), meta, serverURL); err != nil || !store.credentialSet {
		t.Fatalf("same server credential found = %v, error = %v", store.credentialSet, err)
	}
	backend, err = newTestBackend(store)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := backend.LoadInbox(context.Background(), workspaceMeta, appservice.LoadInboxInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Conversations) != 1 || inbox.Conversations[0].Service == nil || inbox.Conversations[0].Service.RequesterAvatarURL != serverURL+contactAvatarURL {
		t.Fatalf("normalized inbox = %#v", inbox)
	}
	if err := backend.Logout(context.Background(), meta); err != nil {
		t.Fatal(err)
	}
	if store.credentialSet {
		t.Fatalf("client credential was not deleted: %#v", store.credential)
	}
}

// TestBackendClearsRejectedCredential 验证服务端拒绝认证后删除原生端登录凭据。
func TestBackendClearsRejectedCredential(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/inbox" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer rejected-token" {
			http.Error(writer, "unexpected authorization", http.StatusBadRequest)
			return
		}
		writeTestJSON(writer, http.StatusUnauthorized, map[string]any{"error": map[string]string{
			"state": "login", "message": "Authentication required.",
		}})
	}))
	defer remote.Close()

	store := &memoryStore{
		serverURL: remote.URL,
		credential: clientsession.Credential{
			ServerURL: remote.URL,
			Token:     "rejected-token",
			ExpiresAt: time.Now().Add(time.Hour),
		},
		credentialSet: true,
	}
	backend, err := newTestBackend(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.LoadInbox(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, appservice.LoadInboxInput{})
	var apiError *appservice.Error
	if !errors.As(err, &apiError) || apiError.State != appservice.SessionStateLogin {
		t.Fatalf("error = %#v, want login session", err)
	}
	if store.credentialSet {
		t.Fatalf("rejected credential was not deleted: %#v", store.credential)
	}
}

// TestBackendClearsCredentialWhenChangingServer 验证切换企业服务器时删除旧登录凭据。
func TestBackendClearsCredentialWhenChangingServer(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/installation/status" {
			http.NotFound(writer, request)
			return
		}
		writeTestJSON(writer, http.StatusOK, map[string]any{"installed": true})
	}))
	defer remote.Close()

	store := &memoryStore{
		serverURL: "https://old.example.com",
		credential: clientsession.Credential{
			ServerURL: "https://old.example.com",
			Token:     "old-token",
			ExpiresAt: time.Now().Add(time.Hour),
		},
		credentialSet: true,
	}
	backend, err := newTestBackend(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.ConnectServer(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, remote.URL); err != nil {
		t.Fatal(err)
	}
	if store.credentialSet {
		t.Fatalf("old credential was not deleted: %#v", store.credential)
	}
}

// TestBackendRejectsUninitializedServer 验证原生端连接时的企业初始化校验。
func TestBackendRejectsUninitializedServer(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/installation/status" {
			http.NotFound(writer, request)
			return
		}
		writeTestJSON(writer, http.StatusOK, map[string]bool{"installed": false})
	}))
	defer remote.Close()

	store := &memoryStore{}
	backend, err := newTestBackend(store)
	if err != nil {
		t.Fatal(err)
	}
	err = backend.ConnectServer(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, remote.URL)
	var apiError *appservice.Error
	if !errors.As(err, &apiError) || apiError.Kind != appservice.ErrorKindInvalid {
		t.Fatalf("error = %#v, want invalid", err)
	}
	if store.serverURL != "" {
		t.Fatalf("server URL = %q, want empty", store.serverURL)
	}
}

// TestBackendRejectsUnrecognizedServer 验证原生端拒绝普通 HTTP 服务。
func TestBackendRejectsUnrecognizedServer(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeTestJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer remote.Close()

	backend, err := newTestBackend(&memoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	err = backend.ConnectServer(context.Background(), appservice.RequestMeta{Locale: "zh-CN"}, remote.URL)
	var apiError *appservice.Error
	if !errors.As(err, &apiError) || apiError.Kind != appservice.ErrorKindUnavailable || apiError.State != "" {
		t.Fatalf("error = %#v, want unavailable without session state", err)
	}
}

// TestParseServerURLAcceptsHTTPAndHTTPS 验证企业服务器可以使用 HTTP 或 HTTPS。
func TestParseServerURLAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, value := range []string{"http://app.example.com", "https://app.example.com"} {
		if _, err := parseServerURL(value); err != nil {
			t.Fatalf("server URL %q was rejected: %v", value, err)
		}
	}
	if _, err := parseServerURL("ftp://app.example.com"); err == nil {
		t.Fatal("expected unsupported server URL scheme to be rejected")
	}
}

func writeTestJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// TestConversationAvatarURLs 验证各会话响应按连接地址补全本地头像地址并保留对象存储地址。
func TestConversationAvatarURLs(t *testing.T) {
	const serverURL = "https://company.example.com/app"
	const avatarPath = "/storage/avatar.png"
	const objectURL = "https://objects.example.com/avatar.png"
	base, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, sourceURL := range []string{avatarPath, objectURL, ""} {
		t.Run(sourceURL, func(t *testing.T) {
			want := sourceURL
			if sourceURL == avatarPath {
				want = serverURL + avatarPath
			}
			conversation := appservice.InboxConversation{Direct: &appservice.DirectInboxConversation{PeerAvatarURL: sourceURL}}
			message := appservice.ConversationMessage{
				Sender:  &appservice.ConversationMessageSender{AvatarURL: sourceURL},
				ReplyTo: &appservice.ConversationMessageReference{Sender: &appservice.ConversationMessageSender{AvatarURL: sourceURL}},
			}
			batch := appservice.InboxConversationResults{Results: []appservice.InboxConversationResult{{Conversation: &conversation}, {Conversation: nil}}}
			inbox := appservice.Inbox{Conversations: []appservice.InboxConversation{conversation}}
			lookup := appservice.DirectConversationLookup{Conversation: &conversation}
			first := appservice.FirstDirectTextMessageResult{Conversation: conversation, Message: message}
			history := appservice.ConversationMessageList{Messages: []appservice.ConversationMessage{message}}
			for _, output := range []any{&batch, &conversation, &inbox, &lookup, &first, &history, &message} {
				// 每次恢复相对地址，验证各响应入口都完成转换。
				conversation.Direct.PeerAvatarURL = sourceURL
				message.Sender.AvatarURL = sourceURL
				message.ReplyTo.Sender.AvatarURL = sourceURL
				resolveFileURLs(output, base)
				switch output.(type) {
				case *appservice.InboxConversationResults, *appservice.InboxConversation, *appservice.Inbox, *appservice.DirectConversationLookup, *appservice.FirstDirectTextMessageResult:
					if conversation.Direct.PeerAvatarURL != want {
						t.Fatalf("%T peer avatar=%q, want=%q", output, conversation.Direct.PeerAvatarURL, want)
					}
				}
				switch output.(type) {
				case *appservice.FirstDirectTextMessageResult, *appservice.ConversationMessageList, *appservice.ConversationMessage:
					if message.Sender.AvatarURL != want || message.ReplyTo.Sender.AvatarURL != want {
						t.Fatalf("%T sender=%q reply=%q, want=%q", output, message.Sender.AvatarURL, message.ReplyTo.Sender.AvatarURL, want)
					}
				}
			}
		})
	}
}

// TestDirectoryAvatarURLs 验证成员、AI 员工、AI 员工服务记录、同事目录、团队成员、联系人、助理和客服负责人响应按连接地址补全本地头像地址并保留对象存储地址。
func TestDirectoryAvatarURLs(t *testing.T) {
	const serverURL = "https://company.example.com/app"
	const avatarPath = "/storage/avatar.png"
	const objectURL = "https://objects.example.com/avatar.png"
	base, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, sourceURL := range []string{avatarPath, objectURL, ""} {
		t.Run(sourceURL, func(t *testing.T) {
			want := sourceURL
			if sourceURL == avatarPath {
				want = serverURL + avatarPath
			}
			user := appservice.User{AvatarURL: sourceURL}
			users := appservice.UserList{Users: []appservice.User{{AvatarURL: sourceURL}}}
			agent := appservice.Agent{AvatarURL: sourceURL}
			agents := appservice.AgentList{Agents: []appservice.AgentListItem{{AvatarURL: sourceURL}}}
			records := appservice.AgentServiceSessionList{Sessions: []appservice.AgentServiceSession{{RequesterAvatarURL: sourceURL}}}
			colleagues := appservice.ColleagueList{Colleagues: []appservice.Colleague{{AvatarURL: sourceURL}}}
			members := appservice.TeamMemberList{Members: []appservice.TeamMember{{AvatarURL: sourceURL}}}
			contact := appservice.Contact{AvatarURL: sourceURL}
			contacts := appservice.ContactList{Contacts: []appservice.ContactSummary{{AvatarURL: sourceURL}}}
			assistants := appservice.AssistantList{Assistants: []appservice.Assistant{{AvatarURL: sourceURL}}}
			assignees := appservice.ServiceAssigneeList{Assignees: []appservice.InboxAssignee{{AvatarURL: sourceURL}}}
			for _, output := range []any{&user, &users, &agent, &agents, &records, &colleagues, &members, &contact, &contacts, &assistants, &assignees} {
				resolveFileURLs(output, base)
			}
			for name, got := range map[string]string{
				"user": user.AvatarURL, "users": users.Users[0].AvatarURL, "agent": agent.AvatarURL, "agents": agents.Agents[0].AvatarURL,
				"records":    records.Sessions[0].RequesterAvatarURL,
				"colleagues": colleagues.Colleagues[0].AvatarURL,
				"members":    members.Members[0].AvatarURL, "contact": contact.AvatarURL, "contacts": contacts.Contacts[0].AvatarURL,
				"assistants": assistants.Assistants[0].AvatarURL, "assignees": assignees.Assignees[0].AvatarURL,
			} {
				if got != want {
					t.Fatalf("%s avatar=%q, want=%q", name, got, want)
				}
			}
		})
	}
}

// TestFileRequestURLs 验证分片序号和下载文件名在补全连接地址后保持查询参数，非文件地址字段和正文保持原样。
func TestFileRequestURLs(t *testing.T) {
	base, err := url.Parse("https://company.example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/storage/file.bin?partNumber=2", "/storage/file.bin?download=%E6%96%87%E4%BB%B6.dat"} {
		request := appservice.FileUploadRequest{URL: path}
		resolveFileURLs(&request, base)
		if request.URL != "https://company.example.com/app"+path {
			t.Fatalf("URL=%q", request.URL)
		}
	}
	segment := appservice.InboxSearchSegment{Text: "/storage/file.bin"}
	resolveFileURLs(&segment, base)
	if segment.Text != "/storage/file.bin" {
		t.Fatalf("text=%q", segment.Text)
	}
}

// TestBackendInboxPagination 验证原生代理保留筛选、游标、页大小和权威总数。
func TestBackendInboxPagination(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path != "/api/inbox" || query.Get("scope") != "all" || query.Get("assigneeFilter") != "identity" || query.Get("assigneeIdentityId") != "peer" || query.Get("audience") != "customer" ||
			query.Get("channelId") != "channel" || query.Get("serviceStatus") != "closed" || !slices.Equal(query["kinds"], []string{"channel"}) ||
			(query.Get("cursor") != "boundary-value" || query.Get("beforeCursor") != "") && (query.Get("beforeCursor") != "boundary-value" || query.Get("cursor") != "") || query.Get("limit") != "7" || request.Header.Get("Authorization") != "Bearer page-token" {
			t.Errorf("request=%s authorization=%s", request.URL, request.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(writer).Encode(appservice.Inbox{Conversations: []appservice.InboxConversation{}, StartCursor: "first", EndCursor: "last", HasBefore: true, NextCursor: "next-boundary", HasMore: true, UnreadCount: 80, AttentionUnreadCount: 70})
	}))
	defer remote.Close()
	backend, err := newTestBackend(&memoryStore{serverURL: remote.URL, credentialSet: true, credential: clientsession.Credential{ServerURL: remote.URL, Token: "page-token", ExpiresAt: time.Now().Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, before := range []bool{false, true} {
		input := appservice.LoadInboxInput{
			Scope: appservice.InboxScopeAll, AssigneeFilter: appservice.InboxAssigneeFilterIdentity, AssigneeIdentityID: "peer", Audience: appservice.ServiceAudienceCustomer,
			ChannelID: "channel", ServiceStatus: appservice.ServiceSessionStatusClosed, Kinds: []appservice.ConversationType{appservice.ConversationTypeChannel},
			Cursor: "boundary-value", Limit: 7,
		}
		if before {
			input.Cursor, input.BeforeCursor = "", "boundary-value"
		}
		page, err := backend.LoadInbox(context.Background(), appservice.RequestMeta{}, input)
		if err != nil || page.NextCursor != "next-boundary" || !page.HasMore || !page.HasBefore || page.StartCursor != "first" || page.EndCursor != "last" || page.UnreadCount != 80 || page.AttentionUnreadCount != 70 {
			t.Fatalf("page=%+v err=%v", page, err)
		}
	}
}

// TestBackendBindsExplicitTokenToCurrentSession 验证请求显式给出令牌时只在它仍是当前服务器上的当前会话时发出；
// 换了账号或服务器后不发出请求，旧令牌不会发往其他服务器。
func TestBackendBindsExplicitTokenToCurrentSession(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		mu.Unlock()
		writeTestJSON(writer, http.StatusOK, map[string]any{"items": []any{}})
	}))
	defer remote.Close()
	store := &memoryStore{serverURL: remote.URL, credentialSet: true, credential: clientsession.Credential{
		ServerURL: remote.URL, AccountID: "account-current", Token: "current-token", ExpiresAt: time.Now().Add(time.Hour),
	}}
	backend, err := newTestBackend(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ListWorkspaces(context.Background(), appservice.RequestMeta{Token: "current-token"}); err != nil {
		t.Fatal(err)
	}
	// 发起后换了账号：旧令牌不再发出。
	if _, err := backend.ListWorkspaces(context.Background(), appservice.RequestMeta{Token: "previous-token"}); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("stale token error = %v", err)
	}
	// 发起后换了服务器：当前服务器上没有这份会话，令牌不发往新服务器。
	moved := &memoryStore{serverURL: remote.URL, credentialSet: true, credential: clientsession.Credential{
		ServerURL: "https://old.example.com", AccountID: "account-old", Token: "old-token", ExpiresAt: time.Now().Add(time.Hour),
	}}
	backend, err = newTestBackend(moved)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ListWorkspaces(context.Background(), appservice.RequestMeta{Token: "old-token"}); !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("other server token error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(authorizations, []string{"Bearer current-token"}) {
		t.Fatalf("authorizations = %v", authorizations)
	}
}
