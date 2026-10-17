//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/messaging"
	"github.com/runforyou-ai/luway/internal/realtime/gateway"
	"github.com/runforyou-ai/luway/internal/realtime/wire"
	"github.com/stretchr/testify/require"
)

// transportGateway 启动独立的真实消息服务和应用网关以验证传输边界。
func transportGateway(t *testing.T) (*messaging.Connection, *gateway.Server, string) {
	t.Helper()
	nc, err := messaging.Open(t.Context(), serverconfig.Config{Data: serverconfig.DataConfig{Directory: t.TempDir()}}, "gateway-test")
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	g := gateway.New(nc)
	g.Authenticate = func(context.Context, string) (gateway.User, error) {
		return gateway.User{ID: "member", Channels: []string{"test.channel"}}, nil
	}
	require.NoError(t, g.Start(t.Context()))
	t.Cleanup(func() { _ = g.Close() })
	http := httptest.NewServer(g)
	t.Cleanup(http.Close)
	return nc, g, "ws" + strings.TrimPrefix(http.URL, "http")
}

// TestRealtimeAuthenticationRevocationRace 验证撤销期间的在途认证不能登记旧连接。
func TestRealtimeAuthenticationRevocationRace(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		target  gateway.Target
		revoked bool
	}{
		{"同一主体", gateway.Target{User: "member"}, true},
		{"同一会话", gateway.Target{User: "member", Session: "session"}, true},
		{"其他主体", gateway.Target{User: "other"}, false},
		{"其他会话", gateway.Target{User: "member", Session: "other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nc, g, address := transportGateway(t)
			entered, release := make(chan struct{}), make(chan struct{})
			g.Authenticate = func(ctx context.Context, _ string) (gateway.User, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return gateway.User{}, ctx.Err()
				}
				return gateway.User{ID: "member", Session: "session"}, nil
			}
			conn, _, err := websocket.DefaultDialer.Dial(address, nil)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.WriteJSON(wire.Frame{Op: "auth", Ticket: "test"}))
			<-entered
			require.NoError(t, g.Disconnect(t.Context(), tc.target))
			processed := make(chan struct{})
			require.NoError(t, nc.Conn.Barrier(func() { close(processed) }))
			<-processed
			close(release)
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
			var frame wire.Frame
			if tc.revoked {
				require.Error(t, conn.ReadJSON(&frame))
				require.NotEqual(t, "ready", frame.Op)
			} else {
				require.NoError(t, conn.ReadJSON(&frame))
				require.Equal(t, "ready", frame.Op)
			}
		})
	}
}

// TestRealtimeSlowConnectionBounded 验证不读取消息的客户端在发送预算耗尽后被关闭。
func TestRealtimeSlowConnectionBounded(t *testing.T) {
	t.Parallel()
	_, g, address := transportGateway(t)
	conn, _, err := websocket.DefaultDialer.Dial(address, nil)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.WriteJSON(wire.Frame{Op: "auth", Ticket: "test"}))
	var frame wire.Frame
	require.NoError(t, conn.ReadJSON(&frame))
	require.Equal(t, "ready", frame.Op)
	require.NoError(t, conn.WriteJSON(wire.Frame{Op: "sub", Channel: "test.channel"}))
	require.NoError(t, conn.ReadJSON(&frame))
	require.Equal(t, "subscribed", frame.Op)
	payload, _ := json.Marshal(strings.Repeat("x", 128<<10))
	for i := 0; i < 256; i++ {
		require.NoError(t, g.Broadcast(t.Context(), gateway.Event{Channels: []string{"test.channel"}, Data: payload}))
	}
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	received := 0
	for {
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		received++
	}
	require.Less(t, received, 256, "slow reader must be disconnected before draining the full backlog")
}

// TestRealtimePeriodicCheckpoint 验证定期历史追平只推进游标，不重复触发订阅就绪。
func TestRealtimePeriodicCheckpoint(t *testing.T) {
	t.Parallel()
	_, _, address := transportGateway(t)
	conn, _, err := websocket.DefaultDialer.Dial(address, nil)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.WriteJSON(wire.Frame{Op: "auth", Ticket: "test"}))
	var frame wire.Frame
	require.NoError(t, conn.ReadJSON(&frame))
	require.Equal(t, "ready", frame.Op)
	require.NoError(t, conn.WriteJSON(wire.Frame{Op: "sub", Channel: "test.channel"}))
	require.NoError(t, conn.ReadJSON(&frame))
	require.Equal(t, "subscribed", frame.Op)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(12*time.Second)))
	require.NoError(t, conn.ReadJSON(&frame))
	require.Equal(t, "checkpoint", frame.Op)
}
