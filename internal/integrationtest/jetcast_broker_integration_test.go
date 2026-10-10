//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
	"github.com/runforyou-ai/jetcast"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/broker"
	"github.com/runforyou-ai/luway/internal/realtime/members"
	"github.com/stretchr/testify/require"
)

// TestJetcastExternalBroker 验证外部账户配置、系统账户强制撤销与 Go WebSocket 代理的长连接和退出行为。
func TestJetcastExternalBroker(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	key, err := nkeys.CreateAccount()
	require.NoError(t, err)
	defer key.Wipe()
	seed, err := key.Seed()
	require.NoError(t, err)
	public, err := key.PublicKey()
	require.NoError(t, err)
	directory := t.TempDir()
	path := filepath.Join(directory, "nats.conf")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`
listen: "127.0.0.1:-1"
jetstream { store_dir: %q }
websocket { listen: "127.0.0.1:-1", no_tls: true }
accounts {
 APP {
  jetstream: enabled, users: [{user: app, password: application}]
  exports: [{stream: "app_realtime.ev.>", accounts: [CLIENT]}, {stream: "app_realtime.c.>", accounts: [CLIENT]}]
  imports: [{stream: {account: CLIENT, subject: "app_realtime.rq.>"}}]
 }
 CLIENT {
  limits: {max_subscriptions: 1000, max_payload: 65536}
  exports: [{stream: "app_realtime.rq.>", accounts: [APP]}]
  imports: [{stream: {account: APP, subject: "app_realtime.ev.>"}}, {stream: {account: APP, subject: "app_realtime.c.>"}}]
 }
 SYS { users: [
  {user: sys, password: system},
  {user: observer, password: readonly, permissions: {publish: ["$SYS.REQ.USER.INFO"], subscribe: ["_INBOX.>"]}}
 ] }
}
system_account: SYS
authorization { timeout: 5s, auth_callout { issuer: %q, account: APP, auth_users: [app, sys, observer] } }
`, directory, public)), 0o600))
	opts, err := server.ProcessConfigFile(path)
	require.NoError(t, err)
	opts.NoSigs, opts.NoLog = true, true
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	ns.Start()
	t.Cleanup(func() { ns.Shutdown(); ns.WaitForShutdown() })
	require.True(t, ns.ReadyForConnections(5*time.Second))
	config := serverconfig.Config{NATS: serverconfig.NATSConfig{
		URL:         strings.Replace(ns.ClientURL(), "://", "://app:application@", 1),
		SystemURL:   strings.Replace(ns.ClientURL(), "://", "://sys:system@", 1),
		CalloutSeed: string(seed), WebSocketURL: "http" + strings.TrimPrefix(ns.WebsocketURL(), "ws"), Namespace: "app", Replicas: 1,
	}}
	connection, err := broker.Open(config, "external-test")
	require.NoError(t, err)
	t.Cleanup(connection.Close)
	backend := newAccountTestBackend(t, f.db)
	service, err := members.New(connection, backend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	// 存储初始化被取消时返回启动错误，随后可用同一账户正常启动。
	failed, err := members.New(connection, backend, f.db, "app_realtime", 1)
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, failed.Start(canceled))
	require.NoError(t, failed.Stop())
	require.NoError(t, service.Start(t.Context()))
	t.Cleanup(func() { _ = service.Stop() })
	proxy, err := connection.Proxy()
	require.NoError(t, err)
	httpServer := httptest.NewUnstartedServer(proxy)
	httpServer.Config.ReadTimeout, httpServer.Config.WriteTimeout = 50*time.Millisecond, 50*time.Millisecond
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	token := loginToken(t, f.db, f.owner.Workspace.ID, f.memberEmail)
	nc, err := nats.Connect("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/nats", nats.Token(token), nats.Name("ABCDEFGHIJKLMNOPQRSTUV"), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	sub, err := nc.SubscribeSync("app_realtime.ev.prv." + realtime.MemberChannels(f.owner.Workspace.ID, f.member.User.ID).Channel)
	require.NoError(t, err)
	require.NoError(t, nc.Flush())
	// HTTP 时限过去后，已升级的 WebSocket 仍可接收消息。
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, service.Publish(t.Context(), realtime.UserIdentityProfileChanged(f.owner.Workspace.ID, f.member.User.ID, 2)))
	_, err = sub.NextMsg(time.Second)
	require.NoError(t, err)
	serverID := nc.ConnectedServerId()
	cid, err := nc.GetClientID()
	require.NoError(t, err)
	result, err := service.Server.Disconnect(t.Context(), jetcast.ByUser("a_"+f.member.Account.ID))
	require.NoError(t, err)
	require.True(t, result.Enforced)
	require.Positive(t, result.Kicked)
	require.Eventually(t, nc.IsClosed, time.Second, 10*time.Millisecond)
	require.NoError(t, connection.Admin.Kick(t.Context(), serverID, cid), "外部 SYS 重试已关闭连接保持幂等")
	// 外部 NATS 的未知实例继续通过 SYS 确认关闭结果。
	unknownCtx, unknownCancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer unknownCancel()
	require.Error(t, connection.Admin.Kick(unknownCtx, "unknown-server", cid))
	redacted := config.Redacted()
	require.NotContains(t, redacted.NATS.URL, "application")
	require.NotContains(t, redacted.NATS.SystemURL, "system@")
	require.Equal(t, "******", redacted.NATS.CalloutSeed)
	bad := config
	bad.NATS.CalloutSeed = "invalid"
	_, err = broker.Open(bad, "invalid-config")
	require.Error(t, err)
	bad = config
	bad.NATS.SystemURL = bad.NATS.URL
	_, err = broker.Open(bad, "wrong-system-account")
	require.Error(t, err)
	bad = config
	bad.NATS.SystemURL = strings.Replace(ns.ClientURL(), "://", "://observer:readonly@", 1)
	_, err = broker.Open(bad, "missing-kick-permission")
	require.ErrorContains(t, err, "forced disconnection")
	for _, address := range []string{"ws://nats:8222", "http://secret@nats:8222", "http://nats:8222?token=secret"} {
		bad = config
		bad.NATS.WebSocketURL = address
		require.Error(t, bad.NATS.ValidateRealtime())
	}
	// 任意网站来源可完成升级，实际业务身份仍由 NATS 认证。
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpServer.URL+"/nats", nil)
	require.NoError(t, err)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Origin", "https://unrelated.example")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	require.NoError(t, response.Body.Close())
	// 新账号的连接由代理关闭流程结束，外部 NATS 持续运行。
	otherToken := loginToken(t, f.db, f.owner.Workspace.ID, f.ownerEmail)
	other, err := nats.Connect("ws"+strings.TrimPrefix(httpServer.URL, "http")+"/nats", nats.Token(otherToken), nats.Name("ZYXWVUTSRQPONMLKJIHGFE"), nats.NoReconnect())
	require.NoError(t, err)
	t.Cleanup(other.Close)
	require.NoError(t, service.Stop())
	connection.Close()
	require.Eventually(t, other.IsClosed, time.Second, 10*time.Millisecond)
	require.True(t, ns.Running())
}

// TestJetcastEmbeddedPersistence 验证本机 WS 监听释放和签名种子跨启动保持一致。
func TestJetcastEmbeddedPersistence(t *testing.T) {
	t.Parallel()
	config := serverconfig.Config{Data: serverconfig.DataConfig{Directory: t.TempDir()}}
	first, err := broker.Open(config, "persist-first")
	require.NoError(t, err)
	public, err := first.Signer.PublicKey()
	require.NoError(t, err)
	address := strings.TrimPrefix(first.WSURL, "http://")
	require.True(t, strings.HasPrefix(address, "127.0.0.1:"))
	first.Close()
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	second, err := broker.Open(config, "persist-second")
	require.NoError(t, err)
	defer second.Close()
	secondPublic, err := second.Signer.PublicKey()
	require.NoError(t, err)
	require.Equal(t, public, secondPublic)
	_, err = second.JS.AccountInfo(context.Background())
	require.NoError(t, err)
}

// TestJetcastLegacyAccountGuard 验证默认账户存在任务数据时停止启动并保留旧队列。
func TestJetcastLegacyAccountGuard(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	opts := &server.Options{JetStream: true, StoreDir: filepath.Join(directory, "nats"), DontListen: true, NoLog: true, NoSigs: true}
	legacy, err := server.NewServer(opts)
	require.NoError(t, err)
	legacy.Start()
	t.Cleanup(func() { legacy.Shutdown(); legacy.WaitForShutdown() })
	require.True(t, legacy.ReadyForConnections(5*time.Second))
	nc, err := nats.Connect("", nats.InProcessServer(legacy))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	_, err = js.CreateStream(t.Context(), jetstream.StreamConfig{Name: "legacy_jobs", Subjects: []string{"legacy.jobs"}})
	require.NoError(t, err)
	_, err = js.Publish(t.Context(), "legacy.jobs", []byte("pending task"))
	require.NoError(t, err)
	nc.Close()
	legacy.Shutdown()
	legacy.WaitForShutdown()
	_, err = broker.Open(serverconfig.Config{Data: serverconfig.DataConfig{Directory: directory}}, "account-upgrade")
	require.ErrorContains(t, err, "default-account data exists")
	restored, err := server.NewServer(opts)
	require.NoError(t, err)
	restored.Start()
	t.Cleanup(func() { restored.Shutdown(); restored.WaitForShutdown() })
	require.True(t, restored.ReadyForConnections(5*time.Second))
	reader, err := nats.Connect("", nats.InProcessServer(restored))
	require.NoError(t, err)
	t.Cleanup(reader.Close)
	restoredJS, err := jetstream.New(reader)
	require.NoError(t, err)
	stream, err := restoredJS.Stream(t.Context(), "legacy_jobs")
	require.NoError(t, err)
	message, err := stream.GetLastMsgForSubject(t.Context(), "legacy.jobs")
	require.NoError(t, err)
	require.Equal(t, "pending task", string(message.Data))
}
