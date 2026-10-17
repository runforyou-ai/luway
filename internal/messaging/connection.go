//go:build server

// Package messaging 管理内部 NATS 连接、内嵌服务器和持久资源。
package messaging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
)

// Connection 持有内部消息连接与当前进程拥有的内嵌服务器。
type Connection struct {
	Conn   *nats.Conn
	JS     jetstream.JetStream
	server *server.Server
}

// Open 连接外部 NATS，或在数据目录启动无网络监听的内嵌服务器。
func Open(ctx context.Context, config serverconfig.Config, name string) (_ *Connection, err error) {
	if err = config.NATS.Validate(); err != nil {
		return nil, err
	}
	c := &Connection{}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	opts := []nats.Option{nats.Name(name), nats.MaxReconnects(-1), nats.ReconnectBufSize(-1), nats.Timeout(5 * time.Second)}
	if config.NATS.URL == "" {
		dir := filepath.Join(config.Data.Directory, "nats")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		c.server, err = server.NewServer(&server.Options{ServerName: name, DontListen: true, NoSigs: true, NoLog: true, JetStream: true, StoreDir: dir})
		if err != nil {
			return nil, err
		}
		c.server.Start()
		if !c.server.ReadyForConnections(10 * time.Second) {
			return nil, errors.New("embedded NATS did not become ready")
		}
		opts = append(opts, nats.InProcessServer(c.server))
	}
	c.Conn, err = nats.Connect(config.NATS.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect internal NATS: %w", err)
	}
	c.JS, err = jetstream.New(c.Conn)
	if err != nil {
		return nil, err
	}
	if config.NATS.URL == "" {
		err = Provision(ctx, c.JS, 1, jetstream.FileStorage)
	} else {
		err = Validate(ctx, c.JS)
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Close 释放连接并停止当前进程启动的内嵌服务器。
func (c *Connection) Close() {
	if c.Conn != nil {
		c.Conn.Close()
	}
	if c.server != nil {
		c.server.Shutdown()
		c.server.WaitForShutdown()
	}
}
