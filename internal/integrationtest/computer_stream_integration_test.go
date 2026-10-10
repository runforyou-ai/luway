//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"uuid"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// requestComputerStream 携带电脑凭据与执行器版本换取实时凭据。
func (h *realtimeGatewayHarness) requestComputerStream(t *testing.T, credential, version string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.url+"/api/computer/realtime-connection", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Accept-Language", "zh-CN")
	request.Header.Set(appservice.ExecutorVersionHeader, version)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

// TestRealtimeComputerStream 验证电脑凭据、版本、精确频道、独立 HTTP 心跳和电脑撤销。
func TestRealtimeComputerStream(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	workspaceID := f.owner.Workspace.ID
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, workspaceID, f.memberEmail)
	registered, err := computeraction.NewRegisterComputerAction(f.db).Execute(ctx, f.member, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员电脑"})
	require.NoError(t, err)

	// 登录令牌与无效凭据都不能建立执行器事件流。
	for _, credential := range []string{token, "unknown"} {
		require.Equal(t, http.StatusUnauthorized, h.requestComputerStream(t, credential, domain.ExecutorVersion).StatusCode, "credential %q", credential)
	}
	for _, version := range []string{"", "1"} {
		require.Equal(t, http.StatusPreconditionFailed, h.requestComputerStream(t, registered.Credential, version).StatusCode, "version %q", version)
	}
	response := h.requestComputerStream(t, registered.Credential, domain.ExecutorVersion)
	require.Equal(t, http.StatusOK, response.StatusCode, "computer stream status")
	var config appservice.RealtimePeerConnection
	require.NoError(t, json.NewDecoder(response.Body).Decode(&config))
	computerClient := connectPeer(t, h.db, h.url, config)
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.Error(t, computerClient.echo.Private(realtime.MemberChannels(workspaceID, f.member.User.ID).InboxChannel).Ready(ready))
	other, err := computeraction.NewCreateWorkspaceComputerAction(f.db).Execute(ctx, f.owner, "其他电脑")
	require.NoError(t, err)
	require.Error(t, computerClient.echo.Private(realtime.ComputerChannel(workspaceID, other.Record.ID)).Ready(ready))
	for _, path := range []string{"/api/realtime", "/api/realtime/computer", "/api/realtime/runs/" + uuid.NewV7().String()} {
		response, err := http.Get(h.url + path)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusNotFound, response.StatusCode)
	}
	require.NoError(t, h.backend.HeartbeatComputer(ctx, appservice.RequestMeta{Token: registered.Credential, ExecutorVersion: domain.ExecutorVersion}))
	var online bool
	require.NoError(t, f.db.NewSelect().Model((*servermodels.Computer)(nil)).ColumnExpr(servermodels.ComputerOnlineExpr("cmp")).Where("cmp.id = ?", registered.Record.ID).Scan(ctx, &online))
	require.True(t, online, "computer after connect")
	memberClient, _ := h.connect(t, token)

	// 本电脑的待执行操作通知只送达执行器事件流，其他电脑的通知不送达。
	require.NoError(t, realtime.RunInTx(ctx, f.db, func(ctx context.Context, _ bun.Tx) error {
		realtime.Notify(ctx, realtime.ComputerWork(workspaceID, uuid.NewV7().String()))
		realtime.Notify(ctx, realtime.ComputerWork(workspaceID, registered.Record.ID))
		return nil
	}))
	computerClient.expect(protocol.ComputerWork{})
	computerClient.expectQuiet()
	memberClient.expectQuiet()

	// 成员登出不影响执行器事件流，撤销电脑后结束。
	require.NoError(t, authaction.NewLogoutAction(f.db).Execute(ctx, testAccountSession(t, f.db, token)))
	computerClient.expectQuiet()
	require.NoError(t, computeraction.NewRevokeComputerAction(f.db, newTestToolDecisions(f.db, testEnqueuer)).Execute(ctx, f.member, registered.Record.ID))
	computerClient.expectEnded()
}
