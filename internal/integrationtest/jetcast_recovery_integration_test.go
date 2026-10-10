//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/jetcast/client"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/stretchr/testify/require"
)

// pausedMemberAuthentication 在真实数据库认证完成后暂停一次授权响应。
type pausedMemberAuthentication struct {
	members.Backend
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

// AuthenticateAccountMembers 将数据库快照停留在撤销操作之前。
func (b *pausedMemberAuthentication) AuthenticateAccountMembers(ctx context.Context, meta appservice.RequestMeta) (direct.AccountMembersSession, error) {
	session, err := b.Backend.AuthenticateAccountMembers(ctx, meta)
	if err == nil {
		b.once.Do(func() {
			close(b.entered)
			select {
			case <-b.release:
			case <-ctx.Done():
			}
		})
	}
	return session, err
}

// TestJetcastAuthenticationRevocationRace 验证登出提交时仍在认证中的连接不能持旧快照获得授权。
func TestJetcastAuthenticationRevocationRace(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	require.NoError(t, h.members.Stop())
	paused := &pausedMemberAuthentication{Backend: h.backend, entered: make(chan struct{}), release: make(chan struct{})}
	service, err := members.New(h.broker, paused, f.db, "app_realtime", 1)
	require.NoError(t, err)
	require.NoError(t, service.Start(t.Context()))
	t.Cleanup(func() { _ = service.Stop() })
	h.publisher.SetMembers(service)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(paused.release) }) })
	result := make(chan error, 1)
	go func() {
		nc, err := nats.Connect("ws"+strings.TrimPrefix(h.url, "http")+"/nats", nats.Name("ABCDEFGHIJKLMNOPQRSTUV"), nats.Token(token), nats.NoReconnect(), nats.Timeout(5*time.Second))
		if nc != nil {
			nc.Close()
		}
		result <- err
	}()
	select {
	case <-paused.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("认证未进入暂停点")
	}
	require.NoError(t, authaction.NewLogoutAction(f.db).Execute(t.Context(), testAccountSession(t, f.db, token)))
	release.Do(func() { close(paused.release) })
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("撤销后的认证未结束")
	}
	h.expectRejected(t, token)
}

// TestJetcastReconnectRecovery 验证断线补发与历史缺口提示使用成员正式传输和真实数据库身份。
func TestJetcastReconnectRecovery(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	h := startRealtimeGateway(t, f)
	token := loginToken(t, f.db, h.workspaceID, f.memberEmail)
	var gate atomic.Pointer[chan struct{}]
	echo, err := client.Connect(t.Context(), client.Options{
		Servers: []string{"ws" + strings.TrimPrefix(h.url, "http") + "/nats"}, Prefix: "app_realtime",
		GetToken: func(ctx context.Context) (string, error) {
			if paused := gate.Load(); paused != nil {
				select {
				case <-*paused:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return token, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() })
	sub := echo.Private(realtime.MemberChannels(h.workspaceID, f.member.User.ID).Channel)
	states := make(chan client.State, 32)
	events := make(chan client.Event, 32)
	sub.OnState(func(state client.State) { states <- state })
	sub.ListenAll(func(event client.Event) { events <- event })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	require.NoError(t, sub.Ready(ctx))
	registry, err := h.broker.JS.KeyValue(ctx, "app_realtime_events_CONN")
	require.NoError(t, err)
	for _, purge := range []bool{false, true} {
		// 等待初始或上次恢复的状态事件消费完毕，再切断当前底层连接。
		for len(states) > 0 {
			<-states
		}
		pause := make(chan struct{})
		gate.Store(&pause)
		record, err := registry.Get(ctx, "s."+echo.SocketID())
		require.NoError(t, err)
		var connection struct {
			Server string `json:"server"`
			CID    uint64 `json:"cid"`
		}
		require.NoError(t, json.Unmarshal(record.Value(), &connection))
		require.NoError(t, h.broker.Admin.Kick(ctx, connection.Server, connection.CID))
		require.Eventually(t, func() bool { return echo.Status() == client.StatusReconnecting }, 3*time.Second, 10*time.Millisecond)
		for version := int64(2); version <= 3; version++ {
			require.NoError(t, h.members.Publish(ctx, realtime.UserIdentityProfileChanged(h.workspaceID, f.member.User.ID, version)))
		}
		if purge {
			stream, err := h.broker.JS.Stream(ctx, "app_realtime_events")
			require.NoError(t, err)
			require.NoError(t, stream.Purge(ctx))
		}
		close(pause)
		gate.Store(nil)
		var recovered client.State
		for recovered.State != client.StateSubscribed {
			select {
			case recovered = <-states:
			case <-ctx.Done():
				t.Fatal("订阅未恢复")
			}
		}
		require.Equal(t, !purge, recovered.Recovered, recovered.Reason)
		if !purge {
			for range 2 {
				select {
				case <-events:
				case <-ctx.Done():
					t.Fatal("断线期间事件未补发")
				}
			}
		}
	}
}
