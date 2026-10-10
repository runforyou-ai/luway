// Package pglisten 以独占连接监听 PostgreSQL 通知频道，连接失效时自动重连并重新订阅。
package pglisten

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
)

const (
	// idleTimeout 是连续未收到通知后探测连接的间隔。
	idleTimeout = 5 * time.Second
	// probeTimeout 是一次连接探测的时限。
	probeTimeout = 5 * time.Second
	// reconnectTimeout 是一次重连建立连接并订阅全部频道的时限。
	reconnectTimeout = 10 * time.Second
	// closeTimeout 是关闭失效连接时等待终止消息发送的时限。
	closeTimeout = time.Second
	// minReconnectDelay 与 maxReconnectDelay 是重连失败后的等待区间，每次失败等待时间翻倍。
	minReconnectDelay = 500 * time.Millisecond
	maxReconnectDelay = 30 * time.Second
)

// Notification 是收到的一条通知。
type Notification struct {
	Channel string
	Payload string
}

// Listener 在后台接收通知并按到达顺序交给处理函数；重连期间发出的通知不会送达。
type Listener struct {
	config   *pgx.ConnConfig
	channels []string
	handle   func(Notification)
	cancel   context.CancelFunc
	done     chan struct{}
}

// Listen 建立监听连接并订阅频道，成功后在后台把通知交给 handle，直到 Close；handle 阻塞时后续通知在数据库中排队。
func Listen(ctx context.Context, config *pgx.ConnConfig, channels []string, handle func(Notification)) (*Listener, error) {
	listener := &Listener{config: config.Copy(), channels: channels, handle: handle, done: make(chan struct{})}
	// 等待通知以上下文截止时间轮询连接，截止时只中断本地读取，不向数据库发送取消请求。
	listener.config.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.DeadlineContextWatcherHandler{Conn: conn.Conn()}
	}
	conn, err := listener.connect(ctx)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	listener.cancel = cancel
	go listener.run(runCtx, conn)
	return listener, nil
}

// Close 停止接收并关闭监听连接，返回时处理函数已不再被调用。
func (l *Listener) Close() {
	l.cancel()
	<-l.done
}

// connect 建立新连接并订阅全部频道。
func (l *Listener) connect(ctx context.Context) (*pgx.Conn, error) {
	conn, err := pgx.ConnectConfig(ctx, l.config)
	if err != nil {
		return nil, fmt.Errorf("connect listener: %w", err)
	}
	for _, channel := range l.channels {
		if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
			l.closeConn(conn)
			return nil, fmt.Errorf("listen %s: %w", channel, err)
		}
	}
	return conn, nil
}

// run 在连接上接收通知，连接失效后按退避间隔重连，直到停止。
func (l *Listener) run(ctx context.Context, conn *pgx.Conn) {
	defer close(l.done)
	for {
		err := l.receive(ctx, conn)
		l.closeConn(conn)
		if ctx.Err() != nil {
			return
		}
		slog.WarnContext(ctx, "PostgreSQL 监听连接失效，开始重连", "error", err)
		// 重连失败时等待时间翻倍，直到连接成功或停止。
		for delay := minReconnectDelay; ; delay = min(2*delay, maxReconnectDelay) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			connectCtx, cancel := context.WithTimeout(ctx, reconnectTimeout)
			conn, err = l.connect(connectCtx)
			cancel()
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return
			}
			slog.WarnContext(ctx, "PostgreSQL 监听连接重连失败", "error", err)
		}
	}
}

// receive 等待通知并交给处理函数，空闲超时后探测连接，连接失效或停止时返回。
func (l *Listener) receive(ctx context.Context, conn *pgx.Conn) error {
	for {
		waitCtx, cancel := context.WithTimeout(ctx, idleTimeout)
		notification, err := conn.WaitForNotification(waitCtx)
		cancel()
		if err == nil {
			l.handle(Notification{Channel: notification.Channel, Payload: notification.Payload})
			continue
		}
		if ctx.Err() != nil || !pgconn.Timeout(err) {
			return err
		}
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		err = conn.Ping(probeCtx)
		cancel()
		if err != nil {
			return err
		}
	}
}

// closeConn 限时关闭连接。
func (l *Listener) closeConn(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	_ = conn.Close(ctx)
}
