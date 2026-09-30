//go:build server

package gateway

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/realtime"
)

// fakeVisitorBackend 按预设结果解析访客受众与签名身份。
type fakeVisitorBackend struct {
	verifyErr error
	verified  []string
}

// AuthenticateVisitor 返回固定的访客受众。
func (b *fakeVisitorBackend) AuthenticateVisitor(context.Context, appservice.WebsiteVisitorMeta, string, string) (direct.WebsiteVisitorAudience, error) {
	return direct.WebsiteVisitorAudience{OrganizationID: "org", ChannelID: "channel", ChannelIdentityID: "identity"}, nil
}

// VerifyCustomer 记录验签的签名身份并返回预设错误。
func (b *fakeVisitorBackend) VerifyCustomer(_ context.Context, _ appservice.WebsiteVisitorMeta, _ string, token string) (appservice.WebsiteVisitorCustomer, error) {
	b.verified = append(b.verified, token)
	return appservice.WebsiteVisitorCustomer{}, b.verifyErr
}

// TestVisitorRouteCustomer 验证登录用户事件流订阅客户身份撤销受众、按签名到期结束，并在订阅生效后按当前密钥重新验签。
func TestVisitorRouteCustomer(t *testing.T) {
	backend := &fakeVisitorBackend{}
	g := New(nil, backend, "test", DefaultOptions())
	expiresAt := time.Now().Add(time.Hour)
	meta := appservice.WebsiteVisitorMeta{CustomerToken: "signed", Customer: &appservice.WebsiteVisitorCustomer{UserID: "u-1", ExpiresAt: expiresAt}}
	route, err := g.visitorRoute(context.Background(), meta, "channel", "web-user:u-1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(route.subjects, realtime.Subject("test", "org", realtime.AudienceCustomerIdentity, "org")) || !route.expiresAt.Equal(expiresAt) {
		t.Fatalf("route = %+v", route)
	}
	if _, err := route.greet(context.Background(), "connection"); err != nil || !slices.Equal(backend.verified, []string{"signed"}) {
		t.Fatalf("greet err = %v, verified = %v", err, backend.verified)
	}

	// 订阅生效前密钥已重新生成时，复核验签失败并拒绝事件流。
	backend.verifyErr = errors.New("customer identity invalid")
	if _, err := route.greet(context.Background(), "connection"); err == nil {
		t.Fatal("expected greet to reject revoked customer identity")
	}

	// 匿名访客不订阅客户身份撤销受众，也不验签。
	backend.verified = nil
	route, err = g.visitorRoute(context.Background(), appservice.WebsiteVisitorMeta{}, "channel", "web-session:0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if len(route.subjects) != 3 || slices.Contains(route.subjects, realtime.Subject("test", "org", realtime.AudienceCustomerIdentity, "org")) ||
		!slices.Contains(route.subjects, realtime.Subject("test", "org", realtime.AudienceWebsiteVisitors, "org")) || !route.expiresAt.IsZero() {
		t.Fatalf("anonymous route = %+v", route)
	}
	if _, err := route.greet(context.Background(), "connection"); err != nil || len(backend.verified) != 0 {
		t.Fatalf("anonymous greet err = %v, verified = %v", err, backend.verified)
	}
}
