//go:build server

package integrationtest

import (
	"context"
	"errors"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/jetcast"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
)

// failingKickAdmin 记录真实 NATS 关闭目标并注入前六次管理请求失败。
type failingKickAdmin struct {
	jetcast.ConnectionAdmin
	mu      sync.Mutex
	targets []string
	cids    []uint64
}

// Kick 在同步关闭和前五次补偿时模拟系统账户故障，后续调用真实管理接口。
func (a *failingKickAdmin) Kick(ctx context.Context, serverID string, cid uint64) error {
	a.mu.Lock()
	a.targets = append(a.targets, serverID)
	a.cids = append(a.cids, cid)
	count := len(a.targets)
	a.mu.Unlock()
	if count <= 6 {
		return errors.New("injected SYS failure")
	}
	return a.ConnectionAdmin.Kick(ctx, serverID, cid)
}

// TestJetcastRevocationTaskRetry 验证持久任务恢复并关闭原连接，合法重连保留其他工作区授权。
func TestJetcastRevocationTaskRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newNavigationFixture(t)
	var admin *failingKickAdmin
	h := startRealtimeGateway(t, f, func(connection *broker.Connection) {
		admin = &failingKickAdmin{ConnectionAdmin: connection.Admin}
		connection.Admin = admin
		runtime, err := servertask.New(ctx, connection.JS, servertask.Options{Namespace: "revoke", Replicas: 1}, f.db, f.db, uuid.NewV7().String())
		require.NoError(t, err)
		require.NoError(t, connection.RegisterRevocations(runtime, f.db))
	})
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	second := servertest.AddAccountWorkspace(t, f.db, token, "保留的授权").Workspace
	closed := make(chan struct{})
	nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name("revocationRetrySocket1"), nats.Token(token), nats.NoReconnect(), nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	serverID := nc.ConnectedServerId()
	cid, err := nc.GetClientID()
	require.NoError(t, err)
	sub, err := nc.SubscribeSync("app_realtime.ev.prv." + realtime.MemberChannels(h.workspaceID, f.member.User.ID).InboxChannel)
	require.NoError(t, err)
	require.NoError(t, nc.Flush())
	require.NoError(t, h.members.Publish(ctx, realtime.ServiceInboxChannelChanged(h.workspaceID, uuid.NewV7().String())))
	_, err = sub.NextMsg(5 * time.Second)
	require.NoError(t, err, "真实直订阅已收到共享频道通知")
	// 不合作客户端只做 NATS 直订阅，业务停用后首次强制关闭确实失败。
	_, err = testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	require.False(t, nc.IsClosed())
	admin.mu.Lock()
	initialTargets := append([]string(nil), admin.targets...)
	admin.mu.Unlock()
	require.Len(t, initialTargets, 1)
	config, err := h.backend.GetRealtimeConnection(ctx, appservice.RequestMeta{Token: token})
	require.NoError(t, err)
	require.Len(t, config.Members, 1)
	require.Equal(t, second.ID, config.Members[0].WorkspaceID)
	remaining, valid := h.connectMemberChannels(t, token, false)
	// 重新组装任务运行时读取已有 JetStream 任务，原投递器未启动过消费者。
	runtime, err := servertask.New(ctx, h.broker.JS, servertask.Options{Namespace: "revoke", Replicas: 1}, f.db, f.db, uuid.NewV7().String())
	require.NoError(t, err)
	workerBroker := &broker.Connection{Admin: admin}
	require.NoError(t, workerBroker.RegisterRevocations(runtime, f.db))
	require.NoError(t, runtime.Start(ctx))
	t.Cleanup(runtime.Stop)
	select {
	case <-closed:
	case <-time.After(45 * time.Second):
		t.Fatal("持久补偿未关闭已失权的原连接")
	}
	require.NoError(t, h.members.Publish(ctx, realtime.ServiceInboxChannelChanged(h.workspaceID, uuid.NewV7().String())))
	_, err = sub.NextMsg(100 * time.Millisecond)
	require.Error(t, err, "旧连接不得收到强制关闭后的敏感通知")
	notice := realtime.UserNotificationFor(second.ID, config.Members[0].UserID, uuid.NewV7().String(), f.groupID, domain.NotificationViewGroup, "仍然可见", "保留的工作区")
	require.NoError(t, h.members.Publish(ctx, notice))
	remaining.expect(members.NotificationFrame(notice))
	require.Equal(t, "connected", string(valid.Status()))
	admin.mu.Lock()
	defer admin.mu.Unlock()
	require.Len(t, admin.targets, 7, "同步失败后，补偿超过默认五次尝试仍成功关闭")
	for i, target := range admin.targets {
		require.Equal(t, serverID, target)
		require.Equal(t, cid, admin.cids[i])
	}
}
