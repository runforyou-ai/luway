//go:build server

// Package serverlog 把服务端进程的全部日志异步批量写入数据库，并按 UTC 自然日维护日志分区与保留期。
package serverlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/integration/control"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

const (
	// ServerLogRetentionDays 是服务端日志的保留天数，按 UTC 自然日分区整体删除超过保留期的日志。
	ServerLogRetentionDays = 30
	// MaintainServerLogsActionName 是提前创建服务端日志分区并删除过期分区的后台任务。
	MaintainServerLogsActionName = "platform.server_logs.maintain"
	// MaintainServerLogsScheduleKey 是维护服务端日志分区的定时计划标识。
	MaintainServerLogsScheduleKey = "platform-server-logs-maintain"
	// serverLogPartitionsAhead 是今天之后提前创建的日分区数。
	serverLogPartitionsAhead = 2
	// serverLogBuffer 是等待写入的日志记录上限，超出时丢弃新记录。
	serverLogBuffer = 10000
	// serverLogBatch 是单次写入的最大记录数。
	serverLogBatch = 500
	// serverLogFlushInterval 是等待凑满一批的最长时间。
	serverLogFlushInterval = time.Second
	// serverLogWriteTimeout 是单次写入的时限。
	serverLogWriteTimeout = 10 * time.Second
)

// ServerLog 把本进程的全部日志异步批量写入 server_logs：处理器在进程启动时安装并排队，Start 后开始写入；数据库不可用或缓冲已满时丢弃记录，并在标准错误输出中说明。
type ServerLog struct {
	instanceID string
	hostname   string
	version    string
	records    chan servermodels.ServerLog
	dropped    atomic.Int64
	stop       chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	started    atomic.Bool
}

// NewServerLog 创建服务端日志记录器，instanceID、hostname 与 version 标识写入日志的进程。
func NewServerLog(instanceID, hostname, version string) *ServerLog {
	return &ServerLog{
		instanceID: instanceID, hostname: hostname, version: version,
		records: make(chan servermodels.ServerLog, serverLogBuffer),
		stop:    make(chan struct{}), done: make(chan struct{}),
	}
}

// Handler 返回 slog 处理器：全部级别的日志排队写入数据库，再按下一个处理器的级别交给它输出。
func (l *ServerLog) Handler(next slog.Handler) slog.Handler {
	return &serverLogHandler{next: next, log: l}
}

// Start 确保日志分区存在后在后台按批写入排队的日志，直到 Stop。
func (l *ServerLog) Start(ctx context.Context, db *bun.DB) error {
	if err := MaintainServerLogPartitions(ctx, db); err != nil {
		return err
	}
	l.started.Store(true)
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(serverLogFlushInterval)
		defer ticker.Stop()
		batch := make([]servermodels.ServerLog, 0, serverLogBatch)
		for {
			select {
			case record := <-l.records:
				if batch = append(batch, record); len(batch) == serverLogBatch {
					batch = l.write(db, batch)
				}
			case <-ticker.C:
				batch = l.write(db, batch)
			case <-l.stop:
				// 写入停止前已排队的记录。
				for {
					select {
					case record := <-l.records:
						if batch = append(batch, record); len(batch) == serverLogBatch {
							batch = l.write(db, batch)
						}
					default:
						l.write(db, batch)
						return
					}
				}
			}
		}
	}()
	return nil
}

// Stop 写入已排队的记录后停止，未开始写入时丢弃排队的记录；之后的日志不再排队。
func (l *ServerLog) Stop() {
	l.stopOnce.Do(func() {
		close(l.stop)
		if l.started.Load() {
			<-l.done
		}
	})
}

// write 写入一批记录并返回清空后的批次；写入失败与此前丢弃的记录数输出到标准错误，不经 slog 记录。
func (l *ServerLog) write(db *bun.DB, batch []servermodels.ServerLog) []servermodels.ServerLog {
	if dropped := l.dropped.Swap(0); dropped > 0 {
		fmt.Fprintf(os.Stderr, "服务端日志缓冲已满，丢弃 %d 条\n", dropped)
	}
	if len(batch) == 0 {
		return batch
	}
	ctx, cancel := context.WithTimeout(context.Background(), serverLogWriteTimeout)
	defer cancel()
	if _, err := db.NewInsert().Model(&batch).Exec(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %d 条服务端日志失败: %v\n", len(batch), err)
	}
	return batch[:0]
}

// enqueue 把一条日志排队；已停止或缓冲已满时丢弃，缓冲已满计入丢弃数。
func (l *ServerLog) enqueue(row servermodels.ServerLog) {
	select {
	case <-l.stop:
		return
	default:
	}
	select {
	case l.records <- row:
	default:
		l.dropped.Add(1)
	}
}

// serverLogHandler 把日志排队写入数据库，并按级别交给下一个处理器输出；prefix 是当前分组前缀。
type serverLogHandler struct {
	next   slog.Handler
	log    *ServerLog
	attrs  []slog.Attr
	prefix string
}

// Enabled 对全部级别启用。
func (h *serverLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

// Handle 把日志排队写入数据库，再按下一个处理器的级别交给它输出。
func (h *serverLogHandler) Handle(ctx context.Context, record slog.Record) error {
	scope := logscope.From(ctx)
	attrs, cause := map[string]string{}, ""
	// 顶层的 error 写入独立字段，上报的事件编号取自 context，上报处理器附加的 event_id 属性不重复写入，其余属性按分组路径展开为文本。
	eventID := control.EventID(ctx)
	var add func(prefix string, attr slog.Attr)
	add = func(prefix string, attr slog.Attr) {
		value := attr.Value.Resolve()
		if value.Kind() == slog.KindGroup {
			if attr.Key != "" {
				prefix += attr.Key + "."
			}
			for _, child := range value.Group() {
				add(prefix, child)
			}
			return
		}
		if attr.Key == "" || (attr.Key == "event_id" && eventID != "") {
			return
		}
		if prefix == "" && attr.Key == "error" {
			cause = value.String()
			return
		}
		attrs[prefix+attr.Key] = value.String()
	}
	for _, attr := range h.attrs {
		add("", attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		add(h.prefix, attr)
		return true
	})
	h.log.enqueue(servermodels.ServerLog{
		ID: uuid.NewV7().String(), OccurredAt: record.Time, Level: int(record.Level),
		InstanceID: h.log.instanceID, Hostname: h.log.hostname, Version: h.log.version, Message: record.Message,
		TraceID: support.NilIfZero(scope.TraceID), Operation: support.NilIfZero(scope.Operation),
		TaskRunID: support.NilIfZero(scope.TaskRunID), Action: support.NilIfZero(scope.Action), Queue: support.NilIfZero(scope.Queue),
		WorkspaceID: support.NilIfZero(scope.WorkspaceID), AccountID: support.NilIfZero(scope.AccountID),
		Error: support.NilIfZero(cause), EventID: support.NilIfZero(eventID), Attributes: attrs,
	})
	if !h.next.Enabled(ctx, record.Level) {
		return nil
	}
	return h.next.Handle(ctx, record)
}

// WithAttrs 返回附加属性后的处理器，属性记在当前分组下。
func (h *serverLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.next = h.next.WithAttrs(attrs)
	clone.attrs = append([]slog.Attr{}, h.attrs...)
	for _, attr := range attrs {
		if h.prefix != "" {
			attr = slog.Attr{Key: h.prefix[:len(h.prefix)-1], Value: slog.GroupValue(attr)}
		}
		clone.attrs = append(clone.attrs, attr)
	}
	return &clone
}

// WithGroup 返回进入分组后的处理器。
func (h *serverLogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.next = h.next.WithGroup(name)
	clone.prefix = h.prefix + name + "."
	return &clone
}

// MaintainServerLogPartitions 在事务内取咨询锁，按数据库时钟的 UTC 日期创建今天起若干天的日分区，并删除整天早于保留期的分区。
func MaintainServerLogPartitions(ctx context.Context, db *bun.DB) error {
	return serverstorage.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := serverstorage.XactLock(ctx, tx, serverstorage.LockServerLogPartitions); err != nil {
			return err
		}
		var today time.Time
		if err := tx.NewRaw("SELECT (now() AT TIME ZONE 'UTC')::date").Scan(ctx, &today); err != nil {
			return fmt.Errorf("read server log partition date: %w", err)
		}
		for offset := range serverLogPartitionsAhead + 1 {
			day := today.AddDate(0, 0, offset)
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s PARTITION OF server_logs FOR VALUES FROM ('%s') TO ('%s')",
				serverLogPartitionName(day), day.Format("2006-01-02")+" 00:00:00+00", day.AddDate(0, 0, 1).Format("2006-01-02")+" 00:00:00+00",
			)); err != nil {
				return fmt.Errorf("create server log partition %s: %w", day.Format(time.DateOnly), err)
			}
		}
		var partitions []string
		if err := tx.NewRaw(`
			SELECT child.relname FROM pg_inherits
			JOIN pg_class child ON child.oid = pg_inherits.inhrelid
			JOIN pg_class parent ON parent.oid = pg_inherits.inhparent
			JOIN pg_namespace ON pg_namespace.oid = parent.relnamespace
			WHERE parent.relname = 'server_logs' AND pg_namespace.nspname = current_schema()
		`).Scan(ctx, &partitions); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("list server log partitions: %w", err)
		}
		cutoff := serverLogPartitionName(today.AddDate(0, 0, -ServerLogRetentionDays))
		for _, partition := range partitions {
			// 分区名按日期排序，早于截止日的分区整体超出保留期。
			if strings.HasPrefix(partition, "server_logs_") && partition < cutoff {
				if _, err := tx.ExecContext(ctx, "DROP TABLE ?", bun.Ident(partition)); err != nil {
					return fmt.Errorf("drop server log partition %s: %w", partition, err)
				}
				slog.InfoContext(ctx, "已删除过期的服务端日志分区", "partition", partition)
			}
		}
		return nil
	})
}

// serverLogPartitionName 返回 UTC 日期对应的日分区表名。
func serverLogPartitionName(day time.Time) string {
	return "server_logs_" + day.Format("20060102")
}
