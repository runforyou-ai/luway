//go:build server

package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/jetcast"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
)

// TestJetcastRevocationAfterRestart 验证内嵌实例退出后的撤销任务完成，且旧实例的 CID 不会关闭新实例连接。
func TestJetcastRevocationAfterRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newNavigationFixture(t)
	backend := newAccountTestBackend(t, f.db)
	config := serverconfig.Config{Data: serverconfig.DataConfig{Directory: t.TempDir()}, NATS: serverconfig.NATSConfig{Namespace: "app"}}
	options := servertask.Options{Namespace: "restart_revoke", Replicas: 1}
	first, err := broker.Open(config, "revoke-first")
	require.NoError(t, err)
	t.Cleanup(first.Close)
	pending, err := servertask.New(ctx, first.JS, options, f.db, f.db, uuid.NewV7().String())
	require.NoError(t, err)
	require.NoError(t, first.RegisterRevocations(pending, f.db))
	service, err := members.New(first, backend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() { require.NoError(t, service.Stop()) })
	token := loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail)
	old, err := nats.Connect("ws"+strings.TrimPrefix(first.WSURL, "http"), nats.Token(token), nats.Name("restartRevocation00001"), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(old.Close)
	oldServerID := old.ConnectedServerId()
	result, err := service.Server.Disconnect(ctx, jetcast.ByUser("a_"+f.member.Account.ID))
	require.NoError(t, err)
	require.Positive(t, result.Kicked)
	require.Eventually(t, old.IsClosed, time.Second, 10*time.Millisecond)
	status, err := pending.TaskStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, status.Delayed)
	// 关闭内嵌服务器，尚未消费的补偿任务与连接登记保存在原目录。
	require.NoError(t, service.Stop())
	first.Close()
	second, err := broker.Open(config, "revoke-second")
	require.NoError(t, err)
	t.Cleanup(second.Close)
	runtime, err := servertask.New(ctx, second.JS, options, f.db, f.db, uuid.NewV7().String())
	require.NoError(t, err)
	require.NoError(t, second.RegisterRevocations(runtime, f.db))
	restarted, err := members.New(second, backend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, restarted.Start(ctx))
	t.Cleanup(func() { require.NoError(t, restarted.Stop()) })
	currentToken := loginToken(t, f.db, f.owner.Workspace.ID, f.ownerEmail)
	current, err := nats.Connect("ws"+strings.TrimPrefix(second.WSURL, "http"), nats.Token(currentToken), nats.Name("restartRevocation00002"), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(current.Close)
	cid, err := current.GetClientID()
	require.NoError(t, err)
	require.NotEqual(t, oldServerID, current.ConnectedServerId())
	sub, err := current.SubscribeSync("app_realtime.ev.prv." + realtime.MemberChannels(f.owner.Workspace.ID, f.owner.User.ID).Channel)
	require.NoError(t, err)
	require.NoError(t, current.Flush())
	// 同一个 CID 属于不同实例时，已退出实例的关闭请求按原始服务器编号完成。
	require.NoError(t, second.Admin.Kick(ctx, oldServerID, cid))
	require.NoError(t, runtime.Start(ctx))
	t.Cleanup(runtime.Stop)
	require.Eventually(t, func() bool {
		status, err := runtime.TaskStatus(ctx)
		if err != nil || status.Delayed != 0 {
			return false
		}
		for _, queue := range status.Queues {
			if queue.Waiting+queue.Running+queue.Failed != 0 {
				return false
			}
		}
		return true
	}, 15*time.Second, 100*time.Millisecond, "已退出内嵌实例的撤销任务须完成")
	require.False(t, current.IsClosed())
	require.NoError(t, restarted.Publish(ctx, realtime.UserIdentityProfileChanged(f.owner.Workspace.ID, f.owner.User.ID, 2)))
	_, err = sub.NextMsg(time.Second)
	require.NoError(t, err)
	// 当前实例的连接仍按真实管理接口强制关闭。
	require.NoError(t, second.Admin.Kick(ctx, current.ConnectedServerId(), cid))
	require.Eventually(t, current.IsClosed, time.Second, 10*time.Millisecond)
}
