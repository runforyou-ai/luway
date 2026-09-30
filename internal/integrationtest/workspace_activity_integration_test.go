//go:build server

package integrationtest

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"uuid"

	authaction "github.com/runforyou-ai/cervi/internal/actions/auth"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	"github.com/runforyou-ai/cervi/internal/realtime/gateway"
	"github.com/runforyou-ai/cervi/internal/realtime/protocol"
	"github.com/uptrace/bun"
)

// openWorkspacesStream 只凭账号登录令牌建立工作区动态事件流并读取首个事件，要求其为服务端 Hello。
func (h *realtimeGatewayHarness) openWorkspacesStream(t *testing.T, token string) *realtimeTestClient {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(h.url, gateway.Path)+gateway.WorkspacesPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("workspaces stream status = %d", response.StatusCode)
	}
	client := h.readFrames(t, response)
	if _, ok := client.next().(protocol.ServerHello); !ok {
		t.Fatal("首个事件不是 server_hello")
	}
	return client
}

// TestRealtimeWorkspacesStream 验证工作区动态事件流下发账号在各工作区的变化并标明工作区，只转发影响提醒的通知，登出后结束。
func TestRealtimeWorkspacesStream(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
	token := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	second, err := h.backend.CreateWorkspace(ctx, appservice.RequestMeta{Token: token}, appservice.WorkspaceInput{Name: "第二工作区", Slug: "activity-" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[20:]})
	if err != nil {
		t.Fatal(err)
	}
	client := h.openWorkspacesStream(t, token)

	// 当前工作区的群消息标明所在工作区。
	f.send(t, f.owner, "工作区动态", false)
	client.expect(protocol.WorkspaceActivity{WorkspaceID: f.owner.Organization.ID, Kind: protocol.TypeConversationChanged, ConversationID: f.groupID, Changes: domain.ConversationChangeTimeline})

	// 另一工作区的客户会话变化经该工作区的客服共享受众送达；输入状态与提醒无关，不转发。
	conversationID := uuid.NewV7().String()
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.Notification{OrganizationID: second.ID, AudienceKind: realtime.AudienceCustomerInbox, AudienceID: second.ID, Kind: realtime.KindConversationTyping, ConversationID: conversationID, Active: true})
		realtime.Notify(ctx, realtime.ServiceInboxConversationChanged(second.ID, conversationID, domain.ConversationTypeChannel, 2, domain.ConversationChangeService))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client.expect(protocol.WorkspaceActivity{WorkspaceID: second.ID, Kind: protocol.TypeConversationChanged, ConversationID: conversationID, Changes: domain.ConversationChangeService})

	// 登出结束工作区动态事件流。
	if err := authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, token)); err != nil {
		t.Fatal(err)
	}
	client.expectEnded()
}

// TestListWorkspaceAttention 验证各工作区的提醒数量与该工作区收件箱的提醒数量一致，停用的成员身份不再返回。
func TestListWorkspaceAttention(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	backend := newAccountTestBackend(f.db)
	token := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	second, err := backend.CreateWorkspace(ctx, appservice.RequestMeta{Token: token}, appservice.WorkspaceInput{Name: "第二工作区", Slug: "attention-" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[20:]})
	if err != nil {
		t.Fatal(err)
	}
	f.send(t, f.owner, "未读一条", false)

	list, err := backend.ListWorkspaceAttention(ctx, appservice.RequestMeta{Token: token})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("attention = %#v, err = %v", list, err)
	}
	inbox, err := backend.LoadInbox(ctx, appservice.RequestMeta{Token: token, WorkspaceID: f.owner.Organization.ID}, appservice.LoadInboxInput{Scope: appservice.InboxScopeChat, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list.Items {
		switch item.WorkspaceID {
		case f.owner.Organization.ID:
			if item.AttentionUnreadCount != inbox.AttentionUnreadCount || item.AttentionUnreadCount == 0 ||
				item.PendingCount != inbox.PendingCount || item.PendingUnreadCount != inbox.PendingUnreadCount {
				t.Fatalf("first workspace attention = %#v, inbox = %#v", item, inbox)
			}
		case second.ID:
			if item != (appservice.WorkspaceAttention{WorkspaceID: second.ID}) {
				t.Fatalf("second workspace attention = %#v", item)
			}
		default:
			t.Fatalf("unexpected workspace %q", item.WorkspaceID)
		}
	}

	// 停用的成员身份不再计入。
	if _, err := testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive); err != nil {
		t.Fatal(err)
	}
	list, err = backend.ListWorkspaceAttention(ctx, appservice.RequestMeta{Token: token})
	if err != nil || len(list.Items) != 1 || list.Items[0].WorkspaceID != second.ID {
		t.Fatalf("attention after deactivation = %#v, err = %v", list, err)
	}
}

// deactivateAfterMembers 在首次读取成员身份后停用指定成员。
type deactivateAfterMembers struct {
	gateway.MemberBackend
	deactivate func()
	once       *sync.Once
}

// AuthenticateAccountMembers 返回成员身份，首次调用后停用指定成员。
func (b deactivateAfterMembers) AuthenticateAccountMembers(ctx context.Context, meta appservice.RequestMeta) (direct.AccountMembersSession, error) {
	session, err := b.MemberBackend.AuthenticateAccountMembers(ctx, meta)
	b.once.Do(b.deactivate)
	return session, err
}

// TestRealtimeWorkspacesStreamRejectsChangedMemberships 验证建立期间成员身份发生变化时拒绝本次连接，客户端重连后按新的成员身份订阅。
func TestRealtimeWorkspacesStreamRejectsChangedMemberships(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	token := loginToken(t, f.db, f.owner.Organization.ID, f.memberEmail)
	if _, err := newAccountTestBackend(f.db).CreateWorkspace(ctx, appservice.RequestMeta{Token: token}, appservice.WorkspaceInput{Name: "第二工作区", Slug: "changed-" + strings.ReplaceAll(uuid.NewV7().String(), "-", "")[20:]}); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	h := startRealtimeGateway(t, f, testGatewayOptions(), func(backend gateway.MemberBackend) gateway.MemberBackend {
		// 直接改库而不发出停用通知，模拟停用通知在订阅生效前已送达、本连接收不到的情形。
		return deactivateAfterMembers{MemberBackend: backend, once: &once, deactivate: func() {
			if _, err := f.db.NewUpdate().Table("users").Set("status = ?", domain.IdentityStatusInactive).Where("id = ?", f.member.User.ID).Exec(ctx); err != nil {
				t.Error(err)
			}
		}}
	})
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(h.url, gateway.Path)+gateway.WorkspacesPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	// 重连后只订阅仍有效的成员身份，停用工作区的变化不再下发。
	client := h.openWorkspacesStream(t, token)
	f.send(t, f.owner, "停用之后", false)
	client.expectQuiet()
}
