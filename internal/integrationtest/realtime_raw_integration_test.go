//go:build server

package integrationtest

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/realtime/wire"
	"github.com/stretchr/testify/require"
)

// rawRealtime 是只维持 WebSocket 读取、不执行重新认证的测试客户端。
type rawRealtime struct {
	conn   *websocket.Conn
	closed chan struct{}
	ended  atomic.Bool
}

// dialRawRealtime 使用首帧票据建立原始实时连接。
func dialRawRealtime(address, ticket string) (*rawRealtime, error) {
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(address, "http")+"/realtime", nil)
	if err != nil {
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.WriteJSON(wire.Frame{Op: "auth", Ticket: ticket}); err != nil {
		conn.Close()
		return nil, err
	}
	var frame wire.Frame
	if err = conn.ReadJSON(&frame); err != nil {
		conn.Close()
		return nil, err
	}
	if frame.Op != "ready" {
		conn.Close()
		return nil, errors.New("authentication rejected")
	}
	_ = conn.SetReadDeadline(time.Time{})
	raw := &rawRealtime{conn: conn, closed: make(chan struct{})}
	go func() {
		defer close(raw.closed)
		defer raw.ended.Store(true)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return raw, nil
}

// Close 关闭底层 WebSocket。
func (r *rawRealtime) Close() { _ = r.conn.Close() }

// IsClosed 返回服务端是否已关闭连接。
func (r *rawRealtime) IsClosed() bool { return r.ended.Load() }

// IsConnected 返回客户端是否仍维持连接。
func (r *rawRealtime) IsConnected() bool { return !r.IsClosed() }

// rawPeer 使用访客或电脑短期票据建立不自动重连的连接。
func rawPeer(t *testing.T, address string, config appservice.RealtimePeerConnection) *rawRealtime {
	t.Helper()
	raw, err := dialRawRealtime(address, config.Token)
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	return raw
}
