//go:build server

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"uuid"
)

const (
	// ServerErrorRetention 是服务端错误记录的保留时长。
	ServerErrorRetention = 7 * 24 * time.Hour
	// PruneServerErrorsActionName 是删除超过保留时长的服务端错误记录的后台任务。
	PruneServerErrorsActionName = "platform.server_errors.prune"
	// PruneServerErrorsScheduleKey 是删除过期服务端错误记录的定时计划标识。
	PruneServerErrorsScheduleKey = "platform-server-errors-prune"
	// serverErrorBuffer 是等待写入的错误记录上限，超出时丢弃新记录。
	serverErrorBuffer = 1000
	// serverErrorBatch 是单次写入的最大记录数。
	serverErrorBatch = 100
	// serverErrorFlushInterval 是等待凑满一批的最长时间。
	serverErrorFlushInterval = time.Second
	// serverErrorWriteTimeout 是单次写入的时限。
	serverErrorWriteTimeout = 5 * time.Second
	// pruneServerErrorsBatch 是单条删除语句处理的最大记录数。
	pruneServerErrorsBatch = 5000
)

// serverErrorColumns 是写入独立字段的顶层日志属性，其余顶层属性与分组属性写入 attributes。
var serverErrorColumns = map[string]bool{"error": true, "operation": true, "action": true, "queue": true}

// ServerErrorLog 把本进程的 Error 级别日志异步批量写入 server_errors；数据库不可用或缓冲已满时丢弃记录，并在标准错误输出中说明。
type ServerErrorLog struct {
	db         *bun.DB
	instanceID string
	hostname   string
	version    string
	records    chan servermodels.ServerError
	dropped    atomic.Int64
	stop       chan struct{}
	done       chan struct{}
}

// NewServerErrorLog 创建服务端错误记录器，instanceID、hostname 与 version 标识写入日志的进程。
func NewServerErrorLog(db *bun.DB, instanceID, hostname, version string) *ServerErrorLog {
	return &ServerErrorLog{
		db: db, instanceID: instanceID, hostname: hostname, version: version,
		records: make(chan servermodels.ServerError, serverErrorBuffer),
		stop:    make(chan struct{}), done: make(chan struct{}),
	}
}

// Handler 返回 slog 处理器：日志交给 next 输出，Error 级别的日志同时排队写入数据库。
func (l *ServerErrorLog) Handler(next slog.Handler) slog.Handler {
	return &serverErrorHandler{next: next, log: l}
}

// Start 在后台按批写入排队的错误记录，直到 Stop。
func (l *ServerErrorLog) Start() {
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(serverErrorFlushInterval)
		defer ticker.Stop()
		batch := make([]servermodels.ServerError, 0, serverErrorBatch)
		for {
			select {
			case record := <-l.records:
				if batch = append(batch, record); len(batch) == serverErrorBatch {
					batch = l.write(batch)
				}
			case <-ticker.C:
				batch = l.write(batch)
			case <-l.stop:
				// 写入停止前已排队的记录。
				for {
					select {
					case record := <-l.records:
						if batch = append(batch, record); len(batch) == serverErrorBatch {
							batch = l.write(batch)
						}
					default:
						l.write(batch)
						return
					}
				}
			}
		}
	}()
}

// Stop 写入已排队的记录后停止。
func (l *ServerErrorLog) Stop() {
	close(l.stop)
	<-l.done
}

// write 写入一批记录并返回清空后的批次；写入失败与此前丢弃的记录数输出到标准错误，不经 slog 记录。
func (l *ServerErrorLog) write(batch []servermodels.ServerError) []servermodels.ServerError {
	if dropped := l.dropped.Swap(0); dropped > 0 {
		fmt.Fprintf(os.Stderr, "服务端错误记录缓冲已满，丢弃 %d 条\n", dropped)
	}
	if len(batch) == 0 {
		return batch
	}
	ctx, cancel := context.WithTimeout(context.Background(), serverErrorWriteTimeout)
	defer cancel()
	if _, err := l.db.NewInsert().Model(&batch).Exec(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %d 条服务端错误记录失败: %v\n", len(batch), err)
	}
	return batch[:0]
}

// enqueue 把一条 Error 级别日志转为错误记录排队，eventID 为上报的事件编号；缓冲已满时计入丢弃数。
func (l *ServerErrorLog) enqueue(record slog.Record, attrs map[string]string, columns map[string]string, eventID string) {
	row := servermodels.ServerError{
		ID: uuid.NewV7().String(), OccurredAt: record.Time, InstanceID: l.instanceID, Hostname: l.hostname, Version: l.version,
		Message: record.Message, Operation: optionalText(columns["operation"]), Action: optionalText(columns["action"]),
		Queue: optionalText(columns["queue"]), Error: optionalText(columns["error"]), EventID: optionalText(eventID), Attributes: attrs,
	}
	select {
	case l.records <- row:
	default:
		l.dropped.Add(1)
	}
}

// optionalText 把空文本转为空指针。
func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// serverErrorHandler 把日志交给下一个处理器输出，并把 Error 级别的日志排队写入数据库；prefix 是当前分组前缀。
type serverErrorHandler struct {
	next   slog.Handler
	log    *ServerErrorLog
	attrs  []slog.Attr
	prefix string
}

// Enabled 对 Error 级别始终启用，其余级别按下一个处理器判断。
func (h *serverErrorHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelError || h.next.Enabled(ctx, level)
}

// Handle 把 Error 级别的日志排队写入数据库，再交给下一个处理器输出。
func (h *serverErrorHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelError {
		attrs, columns := map[string]string{}, map[string]string{}
		// 顶层的 error、operation、action、queue 写入独立字段，上报的事件编号取自 context，上报处理器附加的 event_id 属性不重复写入，其余属性按分组路径展开为文本。
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
			if attr.Key == "" {
				return
			}
			if attr.Key == "event_id" && eventID != "" {
				return
			}
			if prefix == "" && serverErrorColumns[attr.Key] {
				columns[attr.Key] = value.String()
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
		h.log.enqueue(record, attrs, columns, eventID)
	}
	if !h.next.Enabled(ctx, record.Level) {
		return nil
	}
	return h.next.Handle(ctx, record)
}

// WithAttrs 返回附加属性后的处理器，属性记在当前分组下。
func (h *serverErrorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
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
func (h *serverErrorHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.next = h.next.WithGroup(name)
	clone.prefix = h.prefix + name + "."
	return &clone
}

// PruneServerErrors 分批删除超过保留时长的服务端错误记录。
func PruneServerErrors(ctx context.Context, db bun.IDB) error {
	for {
		result, err := db.NewRaw(`
			DELETE FROM server_errors
			WHERE id IN (SELECT id FROM server_errors WHERE occurred_at < now() - make_interval(secs => ?) LIMIT ?)
		`, ServerErrorRetention.Seconds(), pruneServerErrorsBatch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("prune server errors: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read pruned server error count: %w", err)
		}
		if count < pruneServerErrorsBatch {
			return nil
		}
	}
}

// ServerErrorListInput 定义服务端错误列表的分页。
type ServerErrorListInput struct {
	Page     int
	PageSize int
}

// ServerErrorListOutput 定义服务端错误分页结果。
type ServerErrorListOutput struct {
	Errors []servermodels.ServerError
	Page   common.PageInfo
}

// ServerErrorListQuery 读取保留期内的服务端错误记录。
type ServerErrorListQuery struct {
	db *bun.DB
}

// NewServerErrorListQuery 创建服务端错误列表查询。
func NewServerErrorListQuery(db *bun.DB) *ServerErrorListQuery {
	return &ServerErrorListQuery{db: db}
}

// Execute 按记录时间倒序返回一页保留期内的服务端错误。
func (q *ServerErrorListQuery) Execute(ctx context.Context, input ServerErrorListInput) (ServerErrorListOutput, error) {
	page, pageSize, valid := common.NormalizePagination(input.Page, input.PageSize)
	if !valid {
		return ServerErrorListOutput{}, &common.FieldError{Fields: map[string]common.FieldCode{"query": ValidationQueryInvalid}}
	}
	records := make([]servermodels.ServerError, 0)
	total, err := q.db.NewSelect().Model(&records).
		Where("se.occurred_at >= now() - make_interval(secs => ?)", ServerErrorRetention.Seconds()).
		OrderExpr("se.occurred_at DESC, se.id DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		ScanAndCount(ctx)
	if err != nil {
		return ServerErrorListOutput{}, fmt.Errorf("list server errors: %w", err)
	}
	return ServerErrorListOutput{Errors: records, Page: common.PageInfo{Number: page, Size: pageSize, Total: total}}, nil
}
