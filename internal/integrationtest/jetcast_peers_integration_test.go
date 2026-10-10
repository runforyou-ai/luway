//go:build server

package integrationtest

import (
	"context"
	"github.com/runforyou-ai/jetcast/client"
	"github.com/runforyou-ai/luway/internal/actions/realtimeauth"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"strings"
	"testing"
	"time"
)

// connectPeer 使用正式 SDK 订阅访客或电脑的全部精确频道。
func connectPeer(t *testing.T, db *bun.DB, address string, config appservice.RealtimePeerConnection) *realtimeTestClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	echo, err := client.Connect(ctx, client.Options{Servers: []string{"ws" + strings.TrimPrefix(address, "http") + config.Path}, Prefix: config.Prefix,
		GetToken: func(ctx context.Context) (string, error) {
			if _, _, err := realtimeauth.Authenticate(ctx, db, config.Token); err != nil {
				return "", client.ErrUnauthorized
			}
			return config.Token, nil
		}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = echo.Close() })
	c := &realtimeTestClient{t: t, echo: echo, frames: make(chan protocol.Frame, 256)}
	require.NotContains(t, string(echo.Info()), config.Token)
	for _, name := range []string{config.Channel, config.TypingChannel, config.ReceptionChannel} {
		if name == "" {
			continue
		}
		sub := echo.Private(name)
		sub.ListenAll(func(event client.Event) {
			frame, err := protocol.Decode(event.Data)
			if assert.NoError(t, err) {
				select {
				case c.frames <- frame:
				case <-t.Context().Done():
				}
			}
		})
		require.NoError(t, sub.Ready(ctx))
	}
	return c
}
