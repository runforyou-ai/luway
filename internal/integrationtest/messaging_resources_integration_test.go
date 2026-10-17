//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMessagingEmbeddedPersistence 验证默认内嵌重启保留消息且无需网络入口。
func TestMessagingEmbeddedPersistence(t *testing.T) {
	t.Parallel()
	config := serverconfig.Config{Data: serverconfig.DataConfig{Directory: t.TempDir()}}
	first, err := messaging.Open(t.Context(), config, "embedded-first")
	require.NoError(t, err)
	_, err = first.JS.Publish(t.Context(), messaging.RealtimeInput+"test", []byte("durable"))
	require.NoError(t, err)
	first.Close()
	second, err := messaging.Open(t.Context(), config, "embedded-second")
	require.NoError(t, err)
	defer second.Close()
	stream, err := second.JS.Stream(t.Context(), messaging.RealtimeStream)
	require.NoError(t, err)
	msg, err := stream.GetLastMsgForSubject(t.Context(), messaging.RealtimeInput+"test")
	require.NoError(t, err)
	require.Equal(t, "durable", string(msg.Data))
	config.NATS.URL = "nats://127.0.0.1:1"
	failed, err := messaging.Open(t.Context(), config, "external-unavailable")
	require.Error(t, err)
	require.Nil(t, failed)
}

// TestMessagingExternalTopology 验证单节点和三副本集群共用 URL 配置并保留资源策略。
func TestMessagingExternalTopology(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("nodes-%d", count), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			var nodes []*server.Server
			for i := 0; i < count; i++ {
				opts := &server.Options{ServerName: fmt.Sprintf("resource-%d", i), Host: "127.0.0.1", Port: -1, NoSigs: true, NoLog: true, JetStream: true, StoreDir: t.TempDir()}
				if count > 1 {
					opts.Cluster = server.ClusterOpts{Name: "resources", Host: "127.0.0.1", Port: -1}
					if i == 0 {
						listener, err := net.Listen("tcp", "127.0.0.1:0")
						require.NoError(t, err)
						opts.Cluster.Port = listener.Addr().(*net.TCPAddr).Port
						require.NoError(t, listener.Close())
						route, err := url.Parse(fmt.Sprintf("nats://127.0.0.1:%d", opts.Cluster.Port))
						require.NoError(t, err)
						opts.Routes = []*url.URL{route}
					}
					if i > 0 {
						route, err := url.Parse("nats://" + nodes[0].ClusterAddr().String())
						require.NoError(t, err)
						opts.Routes = []*url.URL{route}
					}
				}
				node, err := server.NewServer(opts)
				require.NoError(t, err)
				node.Start()
				require.True(t, node.ReadyForConnections(5*time.Second))
				nodes = append(nodes, node)
				t.Cleanup(func() { node.Shutdown(); node.WaitForShutdown() })
			}
			nc, err := nats.Connect(nodes[0].ClientURL())
			require.NoError(t, err)
			defer nc.Close()
			js, err := jetstream.New(nc)
			require.NoError(t, err)
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				require.NoError(c, messaging.Provision(attempt, js, count, jetstream.FileStorage))
			}, 20*time.Second, 100*time.Millisecond)
			before, err := js.Stream(ctx, messaging.RealtimeStream)
			require.NoError(t, err)
			initial := before.CachedInfo().Config
			config := serverconfig.Config{NATS: serverconfig.NATSConfig{URL: nodes[0].ClientURL()}}
			app, err := messaging.Open(ctx, config, "application")
			require.NoError(t, err)
			defer app.Close()
			after, err := js.Stream(ctx, messaging.RealtimeStream)
			require.NoError(t, err)
			require.Equal(t, initial, after.CachedInfo().Config)
			require.Equal(t, count, after.CachedInfo().Config.Replicas)
			if count == 3 {
				require.Eventually(t, func() bool { return len(app.Conn.DiscoveredServers()) >= 2 }, 5*time.Second, 10*time.Millisecond)
				// 三副本全部同步后再注入节点故障。
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
					defer cancel()
					info, err := after.Info(attempt)
					if !assert.NoError(c, err) || !assert.NotNil(c, info.Cluster) {
						return
					}
					assert.Len(c, info.Cluster.Replicas, 2)
					for _, replica := range info.Cluster.Replicas {
						assert.True(c, replica.Current)
						assert.False(c, replica.Offline)
					}
				}, 20*time.Second, 100*time.Millisecond)
				nodes[0].Shutdown()
				nodes[0].WaitForShutdown()
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					attempt, cancel := context.WithTimeout(ctx, 2*time.Second)
					defer cancel()
					_, err := app.JS.Publish(attempt, messaging.RealtimeInput+"failover", []byte("survived"))
					require.NoError(c, err)
				}, 30*time.Second, 100*time.Millisecond)
			}
		})
	}
}
