//go:build server

package integrationtest

import (
	"context"
	"fmt"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"strings"
	"sync/atomic"
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

// deadlineKickAdmin 在故障期间等待请求时限耗尽，恢复后关闭真实 NATS 连接。
type deadlineKickAdmin struct {
	jetcast.ConnectionAdmin
	restored atomic.Bool
}

// Kick 模拟系统账户请求超时，并在恢复后委托真实管理接口。
func (a *deadlineKickAdmin) Kick(ctx context.Context, serverID string, cid uint64) error {
	if !a.restored.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	return a.ConnectionAdmin.Kick(ctx, serverID, cid)
}

// TestJetcastRevocationAfterDeadline 验证同步关闭耗尽时限后，全部旧连接仍持有独立的可靠补偿。
func TestJetcastRevocationAfterDeadline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newNavigationFixture(t)
	var admin *deadlineKickAdmin
	h := startRealtimeGateway(t, f, func(connection *broker.Connection) {
		admin = &deadlineKickAdmin{ConnectionAdmin: connection.Admin}
		runtime, err := servertask.New(ctx, connection.JS, servertask.Options{Namespace: "deadline_revoke", Replicas: 1}, f.db, f.db, uuid.NewV7().String())
		require.NoError(t, err)
		connection.Admin = admin
		require.NoError(t, connection.RegisterRevocations(runtime, f.db))
	})
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	second := servertest.AddAccountWorkspace(t, f.db, token, "超时后保留的工作区").Workspace
	var old []*nats.Conn
	var subscriptions []*nats.Subscription
	for index := range 2 {
		nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name(fmt.Sprintf("timeoutRevocation%05d", index)), nats.Token(token), nats.NoReconnect())
		require.NoError(t, err)
		t.Cleanup(nc.Close)
		sub, err := nc.SubscribeSync("app_realtime.ev.prv." + realtime.MemberChannels(h.workspaceID, f.member.User.ID).InboxChannel)
		require.NoError(t, err)
		require.NoError(t, nc.Flush())
		old = append(old, nc)
		subscriptions = append(subscriptions, sub)
	}
	require.NoError(t, h.members.Publish(ctx, realtime.ServiceInboxChannelChanged(h.workspaceID, uuid.NewV7().String())))
	for _, sub := range subscriptions {
		_, err := sub.NextMsg(5 * time.Second)
		require.NoError(t, err)
	}
	// 消费者尚未启动，首次管理请求占满整个同步撤销时限，两个直订阅客户端保持连接。
	_, err := testUserStatusAction(f.db).Execute(ctx, f.owner, f.member.User.ID, domain.IdentityStatusInactive)
	require.NoError(t, err)
	for _, nc := range old {
		require.False(t, nc.IsClosed())
	}
	config, err := h.backend.GetRealtimeConnection(ctx, appservice.RequestMeta{Token: token})
	require.NoError(t, err)
	require.Len(t, config.Members, 1)
	require.Equal(t, second.ID, config.Members[0].WorkspaceID)
	remaining, valid := h.connectMemberChannels(t, token, false)
	// 重新创建消费者，仅使用此前持久登记的原连接目标完成撤销。
	admin.restored.Store(true)
	runtime, err := servertask.New(ctx, h.broker.JS, servertask.Options{Namespace: "deadline_revoke", Replicas: 1}, f.db, f.db, uuid.NewV7().String())
	require.NoError(t, err)
	workerBroker := &broker.Connection{Admin: admin}
	require.NoError(t, workerBroker.RegisterRevocations(runtime, f.db))
	require.NoError(t, runtime.Start(ctx))
	t.Cleanup(runtime.Stop)
	require.Eventually(t, func() bool { return old[0].IsClosed() && old[1].IsClosed() }, 15*time.Second, 20*time.Millisecond, "同步时限耗尽后两条原连接均须得到补偿")
	require.NoError(t, h.members.Publish(ctx, realtime.ServiceInboxChannelChanged(h.workspaceID, uuid.NewV7().String())))
	for _, sub := range subscriptions {
		_, err := sub.NextMsg(100 * time.Millisecond)
		require.Error(t, err)
	}
	notice := realtime.UserNotificationFor(second.ID, config.Members[0].UserID, uuid.NewV7().String(), f.groupID, domain.NotificationViewGroup, "保留的通知", "合法新连接")
	require.NoError(t, h.members.Publish(ctx, notice))
	remaining.expect(members.NotificationFrame(notice))
	require.Equal(t, "connected", string(valid.Status()))
}
