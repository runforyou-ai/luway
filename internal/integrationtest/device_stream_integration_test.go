//go:build server

package integrationtest

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"uuid"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/uptrace/bun"
)

// requestDeviceStream 携带令牌与设备编号请求设备事件流并返回响应。
func (h *realtimeGatewayHarness) requestDeviceStream(t *testing.T, token, deviceID string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(h.url, gateway.Path)+gateway.DevicePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(appservice.WorkspaceHeader, h.workspaceID)
	request.Header.Set(appservice.DeviceHeader, deviceID)
	request.Header.Set("Accept-Language", "zh-CN")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// TestRealtimeDeviceStream 验证设备事件流只下发不带同步探针的 Hello 与本设备的工作水位，成员事件流不下发工作水位，登出后设备事件流结束。
func TestRealtimeDeviceStream(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	organizationID := f.owner.Organization.ID
	h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
	token := loginToken(t, f.db, organizationID, f.memberEmail)
	device, err := deviceaction.NewRegisterDeviceAction(f.db).Execute(ctx, f.member, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员电脑", Platform: domain.DevicePlatformMacOS})
	if err != nil {
		t.Fatal(err)
	}

	// 未注册的设备编号不能建立设备事件流。
	if response := h.requestDeviceStream(t, token, uuid.NewV7().String()); response.StatusCode == http.StatusOK {
		t.Fatal("unknown device stream accepted")
	}
	response := h.requestDeviceStream(t, token, device.ID)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("device stream status = %d", response.StatusCode)
	}
	deviceClient := h.readFrames(t, response)
	hello, ok := deviceClient.next().(protocol.ServerHello)
	if !ok || hello.SyncHeads != (appservice.SyncHeads{}) {
		t.Fatalf("device hello = %#v", hello)
	}
	memberClient, _ := h.connect(t, token)

	// 客服共享通知不送达设备事件流；本设备的工作水位只送达设备事件流，其他设备的工作水位不送达。
	if err := realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.ServiceInboxConversationChanged(organizationID, uuid.NewV7().String(), domain.ConversationTypeChannel, 2, domain.ConversationChangeTimeline))
		realtime.Notify(ctx, realtime.UserDeviceWorkAdvanced(organizationID, f.member.User.ID, uuid.NewV7().String(), 1))
		realtime.Notify(ctx, realtime.UserDeviceWorkAdvanced(organizationID, f.member.User.ID, device.ID, 7))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deviceClient.expect(protocol.DeviceWorkAdvanced{DeviceID: device.ID, WorkSeq: 7})
	deviceClient.expectQuiet()
	if _, ok := memberClient.next().(protocol.ConversationChanged); !ok {
		t.Fatal("member stream did not receive customer inbox change")
	}
	memberClient.expectQuiet()

	// 登出结束设备事件流。
	if err := authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, token)); err != nil {
		t.Fatal(err)
	}
	deviceClient.expectEnded()
}
