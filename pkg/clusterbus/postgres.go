package clusterbus

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/runforyou-ai/luway/pkg/pglisten"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/random"
)

const (
	// postgresNotifyBytes 是单条 NOTIFY 载荷的长度上限。
	postgresNotifyBytes = 7900
	// postgresBinaryPrefix 标记以 base64 编码二进制分片的 NOTIFY 载荷，JSON 消息原样发送。
	postgresBinaryPrefix = "~"
	// postgresSyncPrefix 标记确认监听生效的探测载荷，后接探测编号。
	postgresSyncPrefix = "!"
	// postgresSyncRetryInterval 是未收到确认探测时重发的间隔。
	postgresSyncRetryInterval = 100 * time.Millisecond
	// postgresSyncTimeout 是调用方未设置截止时间时等待监听确认的时限。
	postgresSyncTimeout = 5 * time.Second
	// postgresSendQueueSize 是等待发送的消息上限，队列已满时发送返回错误。
	postgresSendQueueSize = 4096
	// postgresBatchSize 是一次提交合并发送的消息上限。
	postgresBatchSize = 128
	// postgresBatchFrames 是一次提交合并发送的分片数达到该值后不再合并后续消息。
	postgresBatchFrames = 1024
	// postgresStatementNotifies 是一条语句中的 NOTIFY 数上限，低于 PostgreSQL 查询目标列表的列数上限。
	postgresStatementNotifies = 1024
	// postgresSendTimeout 是一次合并发送的时限。
	postgresSendTimeout = 5 * time.Second
	// postgresProbeInterval 是向本实例频道发送空探测消息的周期。
	postgresProbeInterval = 5 * time.Second
	// postgresOnlineWindow 是最近一次收到消息后仍判定连接可用的时长。
	postgresOnlineWindow = 3 * postgresProbeInterval
)

// errSendQueueFull 表示等待发送的消息已达上限。
var errSendQueueFull = errors.New("clusterbus: send queue full")

// PostgresOptions 是 PostgreSQL 传输层的参数。
type PostgresOptions struct {
	// Channel 是广播使用的 NOTIFY 频道，实例定向消息使用 Channel 加下划线与实例编号的频道。
	Channel string
}

// NewPostgres 创建经 PostgreSQL LISTEN/NOTIFY 传输消息的总线：db 发送通知，listenConfig 建立独占的监听连接。
func NewPostgres(instanceID string, db *sql.DB, listenConfig *pgx.ConnConfig, options PostgresOptions) *Bus {
	return newBus(instanceID, DriverPostgres, &postgresTransport{db: db, listenConfig: listenConfig, options: options, instanceID: instanceID})
}

// postgresTransport 以广播频道与实例频道传送消息：发送协程把排队消息合并在一次异步提交中 NOTIFY，同一消息的分片在同一事务中按序发送，监听连接按提交顺序接收。同一事务中频道与载荷都相同的通知只送达一次。
type postgresTransport struct {
	db           *sql.DB
	listenConfig *pgx.ConnConfig
	options      PostgresOptions
	instanceID   string
	receive      func([]byte)
	failed       func(int)

	listener *pglisten.Listener
	// lastReceived 是最近一次收到消息的本机时刻（Unix 纳秒），探测消息同样计入。
	lastReceived atomic.Int64
	queue        chan postgresOutgoing
	cancel       context.CancelFunc
	stopped      sync.WaitGroup

	// syncs 是等待送回本实例的确认探测，按探测编号登记。
	syncsMu sync.Mutex
	syncs   map[string]chan struct{}
}

// postgresOutgoing 是一条等待发送的消息；分片列表为空时是探测消息，以 probe 为载荷。
type postgresOutgoing struct {
	channel string
	frames  [][]byte
	probe   string
}

// directChannel 返回实例定向消息的频道。
func (t *postgresTransport) directChannel(instanceID string) string {
	return t.options.Channel + "_" + instanceID
}

// start 监听广播频道与本实例频道，启动发送与探测协程，并等待确认监听生效；确认超时只记录日志，不阻塞启动。
func (t *postgresTransport) start(receive func([]byte), failed func(int)) error {
	t.receive, t.failed = receive, failed
	t.syncs = map[string]chan struct{}{}
	listenCtx, listenCancel := context.WithTimeout(context.Background(), postgresSendTimeout)
	defer listenCancel()
	listener, err := pglisten.Listen(listenCtx, t.listenConfig, []string{t.options.Channel, t.directChannel(t.instanceID)}, t.handleNotification)
	if err != nil {
		return fmt.Errorf("listen bus channels: %w", err)
	}
	t.listener = listener
	t.lastReceived.Store(time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.queue = make(chan postgresOutgoing, postgresSendQueueSize)
	t.stopped.Add(2)
	go t.runSend(ctx)
	go t.runProbe(ctx)
	syncCtx, syncCancel := context.WithTimeout(context.Background(), postgresSyncTimeout)
	defer syncCancel()
	if err := t.sync(syncCtx); err != nil {
		slog.WarnContext(ctx, "总线监听确认失败", "error", err)
	}
	return nil
}

// frameBytes 返回分片按 base64 编码后不超过 NOTIFY 载荷上限的长度。
func (t *postgresTransport) frameBytes() int {
	return (postgresNotifyBytes - len(postgresBinaryPrefix)) / 4 * 3
}

// broadcast 把消息放入发送队列，发往广播频道。
func (t *postgresTransport) broadcast(_ string, frames [][]byte) error {
	return t.enqueue(t.options.Channel, frames, "")
}

// direct 把消息放入发送队列，发往目标实例频道。
func (t *postgresTransport) direct(instanceID string, frames [][]byte) error {
	return t.enqueue(t.directChannel(instanceID), frames, "")
}

// enqueue 把消息放入发送队列，frames 为空时以 probe 为载荷发送探测；传输层未启动或队列已满时返回错误。
func (t *postgresTransport) enqueue(channel string, frames [][]byte, probe string) error {
	if t.queue == nil {
		return ErrClosed
	}
	select {
	case t.queue <- postgresOutgoing{channel: channel, frames: frames, probe: probe}:
		return nil
	default:
		return errSendQueueFull
	}
}

// runSend 取出排队消息，按消息数与分片数上限合并在一次提交中发送，直到传输层关闭。
func (t *postgresTransport) runSend(ctx context.Context) {
	defer t.stopped.Done()
	for {
		var batch []postgresOutgoing
		select {
		case <-ctx.Done():
			return
		case first := <-t.queue:
			batch = append(batch, first)
		}
		frames := len(batch[0].frames)
	drain:
		for len(batch) < postgresBatchSize && frames < postgresBatchFrames {
			select {
			case next := <-t.queue:
				batch = append(batch, next)
				frames += len(next.frames)
			default:
				break drain
			}
		}
		sendCtx, cancel := context.WithTimeout(ctx, postgresSendTimeout)
		err := t.send(sendCtx, batch)
		cancel()
		if err != nil && ctx.Err() == nil {
			// 探测消息不计入失败数。
			t.failed(arr.Count(batch, func(outgoing postgresOutgoing) bool { return len(outgoing.frames) > 0 }))
			slog.WarnContext(ctx, "总线消息发送失败", "count", len(batch), "error", err)
		}
	}
}

// send 在一次关闭同步提交的事务中按序 NOTIFY 全部消息的分片，每条语句不超过 postgresStatementNotifies 个，提交时一并送达监听方；JSON 分片原样发送，其余分片以 base64 编码。
func (t *postgresTransport) send(ctx context.Context, batch []postgresOutgoing) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
		return err
	}
	var arguments []any
	for _, outgoing := range batch {
		payloads := make([]string, len(outgoing.frames))
		for index, frame := range outgoing.frames {
			if len(frame) > 0 && frame[0] == '{' {
				payloads[index] = string(frame)
			} else {
				payloads[index] = postgresBinaryPrefix + base64.StdEncoding.EncodeToString(frame)
			}
		}
		if len(payloads) == 0 {
			payloads = []string{outgoing.probe}
		}
		for _, payload := range payloads {
			arguments = append(arguments, outgoing.channel, payload)
		}
	}
	for start := 0; start < len(arguments); start += 2 * postgresStatementNotifies {
		statement := arguments[start:min(len(arguments), start+2*postgresStatementNotifies)]
		// 每个 pg_notify 依次使用两个占位参数。
		calls := make([]string, len(statement)/2)
		for index := range calls {
			calls[index] = fmt.Sprintf("pg_notify($%d, $%d)", 2*index+1, 2*index+2)
		}
		if _, err := tx.ExecContext(ctx, "SELECT "+strings.Join(calls, ", "), statement...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// handleNotification 按到达顺序把收到的分片交给总线；监听连接失效时由 pglisten 重连。
func (t *postgresTransport) handleNotification(notification pglisten.Notification) {
	t.lastReceived.Store(time.Now().UnixNano())
	if notification.Payload == "" {
		return
	}
	if id, probe := strings.CutPrefix(notification.Payload, postgresSyncPrefix); probe {
		t.syncsMu.Lock()
		if done := t.syncs[id]; done != nil {
			delete(t.syncs, id)
			close(done)
		}
		t.syncsMu.Unlock()
		return
	}
	frame := []byte(notification.Payload)
	if encoded, binary := strings.CutPrefix(notification.Payload, postgresBinaryPrefix); binary {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			slog.WarnContext(context.Background(), "总线分片无法解码", "error", err)
			return
		}
		frame = decoded
	}
	t.receive(frame)
}

// runProbe 定期向本实例频道发送空探测消息。
func (t *postgresTransport) runProbe(ctx context.Context) {
	defer t.stopped.Done()
	ticker := time.NewTicker(postgresProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.enqueue(t.directChannel(t.instanceID), nil, ""); err != nil {
				slog.WarnContext(ctx, "总线探测消息发送失败", "error", err)
			}
		}
	}
}

// subscribe 不需要登记，广播频道收到的全部消息由总线按主题分发。
func (t *postgresTransport) subscribe(string) error { return nil }

// unsubscribe 不需要取消登记。
func (t *postgresTransport) unsubscribe(string) {}

// sync 向本实例频道发送带编号的确认探测并等待收到，收到时之前登记的监听都已生效；未收到时每隔 postgresSyncRetryInterval 重发，调用方未设置截止时间时最多等待 postgresSyncTimeout。
func (t *postgresTransport) sync(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, postgresSyncTimeout)
		defer cancel()
	}
	if t.queue == nil {
		return ErrClosed
	}
	key := random.Hex(8)
	done := make(chan struct{})
	t.syncsMu.Lock()
	t.syncs[key] = done
	t.syncsMu.Unlock()
	defer func() {
		t.syncsMu.Lock()
		delete(t.syncs, key)
		t.syncsMu.Unlock()
	}()
	// 监听生效前送达的探测会丢失，未收到时按间隔重发同一编号的探测。
	retry := time.NewTicker(postgresSyncRetryInterval)
	defer retry.Stop()
	for {
		if err := t.enqueue(t.directChannel(t.instanceID), nil, postgresSyncPrefix+key); err != nil {
			return err
		}
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-retry.C:
		}
	}
}

// connected 返回最近是否收到过消息：探测消息按周期经数据库送回本实例，收不到即判定发送或监听不可用。
func (t *postgresTransport) connected() bool {
	return time.Since(time.Unix(0, t.lastReceived.Load())) < postgresOnlineWindow
}

// close 停止发送、接收与探测协程并关闭监听连接，尚未发送的消息直接丢弃。
func (t *postgresTransport) close() error {
	if t.cancel == nil {
		return nil
	}
	t.cancel()
	t.lastReceived.Store(0)
	t.listener.Close()
	t.stopped.Wait()
	return nil
}
