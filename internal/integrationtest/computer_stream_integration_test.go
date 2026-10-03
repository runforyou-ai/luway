//go:build server

package integrationtest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"uuid"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// requestComputerStream 携带电脑凭据请求执行器事件流并返回响应。
func (h *realtimeGatewayHarness) requestComputerStream(t *testing.T, credential string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(h.url, gateway.Path)+gateway.ComputerPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// TestRealtimeComputerStream 验证执行器事件流凭电脑凭据建立并记录在线，只下发本电脑的待执行操作通知，与成员登录会话无关，电脑撤销后结束。
func TestRealtimeComputerStream(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	organizationID := f.owner.Organization.ID
	h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
	token := loginToken(t, f.db, organizationID, f.memberEmail)
	registered, err := computeraction.NewRegisterComputerAction(f.db).Execute(ctx, f.member, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员电脑", Platform: domain.ComputerPlatformMacOS})
	if err != nil {
		t.Fatal(err)
	}

	// 登录令牌与无效凭据都不能建立执行器事件流。
	for _, credential := range []string{token, "unknown"} {
		if response := h.requestComputerStream(t, credential); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("credential %q stream status = %d", credential, response.StatusCode)
		}
	}
	response := h.requestComputerStream(t, registered.Credential)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("computer stream status = %d", response.StatusCode)
	}
	computerClient := h.readFrames(t, response)
	if _, ok := computerClient.next().(protocol.ServerHello); !ok {
		t.Fatal("computer stream did not start with hello")
	}
	var computer servermodels.Computer
	if err := f.db.NewSelect().Model(&computer).Where("cmp.id = ?", registered.Record.ID).Scan(ctx); err != nil || !computer.Online(time.Now()) {
		t.Fatalf("computer after connect = %+v, err = %v", computer, err)
	}
	memberClient, _ := h.connect(t, token)

	// 本电脑的待执行操作通知只送达执行器事件流，其他电脑的通知不送达。
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.ComputerWork(organizationID, uuid.NewV7().String()))
		realtime.Notify(ctx, realtime.ComputerWork(organizationID, registered.Record.ID))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	computerClient.expect(protocol.ComputerWork{})
	computerClient.expectQuiet()
	memberClient.expectQuiet()

	// 成员登出不影响执行器事件流，撤销电脑后结束。
	if err := authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, token)); err != nil {
		t.Fatal(err)
	}
	computerClient.expectQuiet()
	if err := computeraction.NewRevokeComputerAction(f.db, newTestTasks(f.db)).Execute(ctx, f.member, registered.Record.ID); err != nil {
		t.Fatal(err)
	}
	computerClient.expectEnded()
}
