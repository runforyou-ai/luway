//go:build server

package integrationtest

import (
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/stretchr/testify/require"
)

// TestJetcastComputerReregistrationRevocation 验证同一安装重新注册关闭旧连接并保留另一电脑的授权。
func TestJetcastComputerReregistrationRevocation(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	ctx := t.Context()
	meta := appservice.RequestMeta{Token: f.memberToken, WorkspaceID: f.member.Workspace.ID}
	input := appservice.ComputerRegistrationInput{InstallID: uuid.NewV7().String(), Name: "重新注册电脑"}
	first, err := h.backend.RegisterComputer(ctx, meta, input)
	require.NoError(t, err)
	oldMeta := appservice.RequestMeta{Token: first.Credential, ExecutorVersion: domain.ExecutorVersion}
	oldConfig, err := h.backend.GetComputerRealtimeConnection(ctx, oldMeta)
	require.NoError(t, err)
	old := rawPeer(t, h.url, oldConfig)
	oldSDK := connectPeer(t, f.db, h.url, oldConfig)
	other, err := h.backend.RegisterComputer(ctx, meta, appservice.ComputerRegistrationInput{InstallID: uuid.NewV7().String(), Name: "保留的电脑"})
	require.NoError(t, err)
	otherMeta := appservice.RequestMeta{Token: other.Credential, ExecutorVersion: domain.ExecutorVersion}
	otherConfig, err := h.backend.GetComputerRealtimeConnection(ctx, otherMeta)
	require.NoError(t, err)
	valid := connectPeer(t, f.db, h.url, otherConfig)
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(meta.WorkspaceID, first.Computer.ID)))
	oldSDK.expect(protocol.ComputerWork{})
	require.True(t, old.IsConnected())

	// 相同安装只轮换现有电脑的凭据，旧直订阅由服务端强制关闭。
	registered, err := h.backend.RegisterComputer(ctx, meta, input)
	require.NoError(t, err)
	require.Equal(t, first.Computer.ID, registered.Computer.ID)
	require.NotEqual(t, first.Credential, registered.Credential)
	require.Eventually(t, old.IsClosed, 5*time.Second, 10*time.Millisecond, "重新注册必须强制关闭忽略 SDK 控制消息的旧连接")
	oldSDK.expectEnded()
	require.Equal(t, http.StatusUnauthorized, h.requestComputerStream(t, first.Credential, domain.ExecutorVersion).StatusCode)
	require.Error(t, h.backend.HeartbeatComputer(ctx, oldMeta))
	denied, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+oldConfig.Path,
		nats.Token(oldConfig.Token), nats.Name(strings.ReplaceAll(uuid.NewV7().String(), "-", "")[:22]), nats.NoReconnect())
	if denied != nil {
		denied.Close()
	}
	require.Error(t, err, "旧实时 token 必须拒绝重新连接")

	// 新凭据和另一电脑均可心跳并收取自己的工作通知。
	newMeta := appservice.RequestMeta{Token: registered.Credential, ExecutorVersion: domain.ExecutorVersion}
	newConfig, err := h.backend.GetComputerRealtimeConnection(ctx, newMeta)
	require.NoError(t, err)
	current := connectPeer(t, f.db, h.url, newConfig)
	require.NoError(t, h.backend.HeartbeatComputer(ctx, newMeta))
	require.NoError(t, h.backend.HeartbeatComputer(ctx, otherMeta))
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(meta.WorkspaceID, registered.Computer.ID)))
	current.expect(protocol.ComputerWork{})
	require.NoError(t, h.members.Publish(ctx, realtime.ComputerWork(meta.WorkspaceID, other.Computer.ID)))
	valid.expect(protocol.ComputerWork{})
}
