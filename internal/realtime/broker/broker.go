//go:build server

// Package broker 管理任务与成员实时推送共用的 NATS 连接、内嵌服务器和 WebSocket 入口。
package broker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
	"github.com/runforyou-ai/jetcast"
	"github.com/runforyou-ai/jetcast/embedded"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
)

// ClientAccount 是认证回调放置成员、访客与电脑连接的 NATS 账户。
const ClientAccount = "CLIENT"

// Connection 持有应用账户、系统管理能力与本实例 WebSocket 代理的资源。
type Connection struct {
	Conn   *nats.Conn
	JS     jetstream.JetStream
	Signer nkeys.KeyPair
	Admin  jetcast.ConnectionAdmin
	WSURL  string
	server *server.Server
	system *nats.Conn
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

// Open 连接外部 NATS，或启动只监听本机 WebSocket 的内嵌 NATS。
func Open(config serverconfig.Config, name string) (_ *Connection, err error) {
	if err := config.NATS.ValidateRealtime(); err != nil {
		return nil, err
	}
	b := &Connection{}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	defer func() {
		if err != nil {
			b.Close()
		}
	}()
	if config.NATS.URL == "" {
		if err = b.openEmbedded(config.Data.Directory, config.NATS.RealtimePrefix(), name); err != nil {
			return nil, err
		}
	} else {
		b.Signer, err = nkeys.FromSeed([]byte(config.NATS.CalloutSeed))
		if err != nil {
			return nil, fmt.Errorf("load NATS callout signer: %w", err)
		}
		b.Conn, err = nats.Connect(config.NATS.URL, nats.Name(name), nats.MaxReconnects(-1))
		if err != nil {
			return nil, fmt.Errorf("connect application NATS account: %w", err)
		}
		b.system, err = nats.Connect(config.NATS.SystemURL, nats.Name(name+"-system"), nats.MaxReconnects(-1))
		if err != nil {
			return nil, fmt.Errorf("connect system NATS account: %w", err)
		}
		b.Admin, b.WSURL = jetcast.SystemAdmin(b.system), config.NATS.WebSocketURL
		// 外部连接须分别属于应用账户和系统账户。
		for account, conn := range map[string]*nats.Conn{"APP": b.Conn, "SYS": b.system} {
			message, requestErr := conn.Request("$SYS.REQ.USER.INFO", nil, 5*time.Second)
			if requestErr != nil {
				return nil, fmt.Errorf("verify NATS %s account: %w", account, requestErr)
			}
			var response struct {
				Data server.UserInfo `json:"data"`
			}
			if err := json.Unmarshal(message.Data, &response); err != nil {
				return nil, err
			}
			if response.Data.Account != account {
				return nil, fmt.Errorf("NATS connection must belong to %s", account)
			}
		}
		if err := b.verifyAdmin(config.NATS.URL, name); err != nil {
			return nil, err
		}
	}
	b.JS, err = jetstream.New(b.Conn)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// verifyAdmin 用临时应用连接验证系统账户能在当前集群中强制关闭连接。
func (b *Connection) verifyAdmin(address, name string) error {
	closed := make(chan struct{})
	probe, err := nats.Connect(address, nats.Name(name+"-admin-check"), nats.NoReconnect(), nats.ClosedHandler(func(*nats.Conn) { close(closed) }))
	if err != nil {
		return fmt.Errorf("connect NATS administration probe: %w", err)
	}
	defer probe.Close()
	cid, err := probe.GetClientID()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()
	if err := b.Admin.Kick(ctx, probe.ConnectedServerId(), cid); err != nil {
		return fmt.Errorf("verify NATS forced disconnection: %w", err)
	}
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("verify NATS forced disconnection: %w", ctx.Err())
	}
}

// openEmbedded 在数据目录中保存签名密钥，以随机服务凭据启动 APP、CLIENT 与 SYS 账户，CLIENT 账户只与 APP 交换实时前缀下的主题。
func (b *Connection) openEmbedded(directory, prefix, name string) error {
	directory = filepath.Join(directory, "nats")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	// 默认账户的存储须在切换 APP 账户前完成迁移或归档。
	legacy := filepath.Join(directory, "jetstream", "$G")
	entries, err := os.ReadDir(legacy)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("embedded NATS default-account data exists at %s; migrate or archive it before starting the APP account", legacy)
	}
	signer, err := loadSigner(directory)
	if err != nil {
		return err
	}
	b.Signer = signer
	issuer, err := signer.PublicKey()
	if err != nil {
		return err
	}
	password := rand.Text()
	accounts, err := embedded.Accounts{Prefix: prefix, Clients: ClientAccount, AppPassword: password, SystemPassword: rand.Text(), Issuer: issuer}.Config()
	if err != nil {
		return err
	}
	configuration := fmt.Sprintf("jetstream { store_dir: %q }\nwebsocket { listen: \"127.0.0.1:-1\", no_tls: true }\n%s", directory, accounts)
	file, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(configuration)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	opts, err := server.ProcessConfigFile(file.Name())
	if err != nil {
		return err
	}
	opts.ServerName, opts.DontListen, opts.NoSigs, opts.NoLog = name, true, true, true
	b.server, err = server.NewServer(opts)
	if err != nil {
		return err
	}
	b.server.Start()
	if !b.server.ReadyForConnections(10 * time.Second) {
		return errors.New("embedded NATS did not become ready")
	}
	b.Conn, err = nats.Connect("", nats.InProcessServer(b.server), nats.UserInfo("app", password), nats.Name(name))
	if err != nil {
		return err
	}
	b.Admin = standaloneAdmin{ConnectionAdmin: embedded.Admin(b.server), serverID: b.server.ID()}
	b.WSURL = "http" + strings.TrimPrefix(b.server.WebsocketURL(), "ws")
	return nil
}

// loadSigner 原子创建或读取内嵌 NATS 的账户签名密钥。
func loadSigner(directory string) (nkeys.KeyPair, error) {
	path := filepath.Join(directory, "callout.seed")
	seed, err := os.ReadFile(path)
	if err == nil {
		return nkeys.FromSeed(seed)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := nkeys.CreateAccount()
	if err != nil {
		return nil, err
	}
	seed, err = key.Seed()
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, ".seed-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(seed)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return nil, err
	}
	if err := os.Link(file.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	seed, err = os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return nkeys.FromSeed(seed)
}

// Proxy 将部署的 /nats 入口转发到 NATS WebSocket，退出时关闭本实例代理的长连接。
func (b *Connection) Proxy() (http.Handler, error) {
	target, err := url.Parse(b.WSURL)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b.ctx.Err() != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(time.Time{}); err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := controller.SetWriteDeadline(time.Time{}); err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(b.ctx, cancel)
		defer stop()
		defer cancel()
		proxy.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

// Close 取消代理请求并关闭账户连接和本实例内嵌的服务器。
func (b *Connection) Close() {
	b.once.Do(func() {
		b.cancel()
		if b.Conn != nil {
			b.Conn.Close()
		}
		if b.system != nil {
			b.system.Close()
		}
		if b.server != nil {
			b.server.Shutdown()
			b.server.WaitForShutdown()
		}
		if b.Signer != nil {
			b.Signer.Wipe()
		}
	})
}
