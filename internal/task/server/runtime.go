//go:build server

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"uuid"

	"github.com/runforyou-ai/luway/internal/messaging"
	"github.com/runforyou-ai/luway/internal/messaging/outbox"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/runforyou-ai/jetq"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

var (
	_ Enqueuer   = (*Runtime)(nil)
	_ TxEnqueuer = (*Runtime)(nil)
	_ Monitor    = (*Runtime)(nil)
)

// 任务消息头：所属工作区、路由与登记任务时的日志串联编号。
const (
	headerWorkspace = "App-Workspace"
	headerRoute     = "App-Route"
	headerTraceID   = "App-Trace-Id"
)

// Runtime 经 jetq 投递与执行服务端 Action：任务存放在 JetStream，定时计划由 NATS 服务端触发；所属工作区未启用或本实例未持有路由租约时任务推迟执行。
type Runtime struct {
	*Routes
	client    *jetq.Client
	db        *bun.DB
	registry  *Registry
	schedules []ScheduleDefinition
	outbox    *outbox.Dispatcher

	cancel context.CancelFunc
	wait   sync.WaitGroup
}

// New 连接已初始化的任务资源并创建任务运行时。
func New(ctx context.Context, js jetstream.JetStream, db, leaseDB *bun.DB, instanceID string) (*Runtime, error) {
	opts := append(messaging.QueueOptions(), jetq.WithLogger(slog.New(jobLogHandler{slog.Default().Handler()})))
	client, err := jetq.Attach(ctx, js, opts...)
	if err != nil {
		return nil, fmt.Errorf("attach task queue: %w", err)
	}
	r := &Runtime{Routes: NewRoutes(leaseDB, instanceID), client: client, db: db, registry: NewRegistry()}
	r.outbox = outbox.New(db, "task", r.publishOutbox)
	return r, nil
}

// queuedTask 保存任务的稳定标识、载荷及绝对执行时间。
type queuedTask struct {
	ID      string
	Action  string
	Payload json.RawMessage
	Options EnqueueOptions
	TraceID string
}

// publishOutbox 按稳定任务编号重新投递，发布结果不确定时保留待投递登记。
func (r *Runtime) publishOutbox(ctx context.Context, _ string, raw json.RawMessage) error {
	var item queuedTask
	if err := json.Unmarshal(raw, &item); err != nil {
		return err
	}
	job, opts, err := r.prepare(ctx, item.Action, item.Payload, item.Options, item.ID)
	if err != nil {
		return err
	}
	opts = append(opts, jetq.MsgID(item.ID))
	if item.TraceID != "" {
		opts = append(opts, jetq.WithHeader(headerTraceID, item.TraceID))
	}
	return r.publish(ctx, item.Action, job, opts)
}

// Registry 返回 Action 注册表。
func (r *Runtime) Registry() *Registry {
	return r.registry
}

// RegisterSchedule 注册一个由代码管理的定时 Action。
func (r *Runtime) RegisterSchedule(definition ScheduleDefinition) {
	r.schedules = append(r.schedules, definition)
}

// Enqueue 立即投递 Action 输入。
func (r *Runtime) Enqueue(ctx context.Context, actionName string, payload any, options EnqueueOptions) error {
	job, opts, err := r.prepare(ctx, actionName, payload, options, uuid.NewV7().String())
	if err != nil {
		return err
	}
	return r.publish(ctx, actionName, job, opts)
}

// EnqueueIn 在当前写事务内登记 Action 输入并返回任务编号，事务提交后投递，回滚时丢弃；调用方必须处于 serverstorage.RunInTx 内。
func (r *Runtime) EnqueueIn(ctx context.Context, actionName string, payload any, options EnqueueOptions) (string, error) {
	id := uuid.NewV7().String()
	job, _, err := r.prepare(ctx, actionName, payload, options, id)
	if err != nil {
		return "", err
	}
	item := queuedTask{ID: id, Action: actionName, Payload: job, Options: options, TraceID: logscope.From(ctx).TraceID}
	if options.Delay > 0 {
		now, err := serverstorage.ClockNow(ctx, serverstorage.Transaction(ctx))
		if err != nil {
			return "", err
		}
		item.Options.At = now.Add(options.Delay)
		item.Options.Delay = 0
	}
	_, err = serverstorage.PutOutbox(ctx, "task", item)
	return id, err
}

// EnqueueManyIn 在当前写事务内登记一批 Action 输入，事务提交后按输入顺序投递。
func (r *Runtime) EnqueueManyIn(ctx context.Context, requests []EnqueueRequest) error {
	for _, request := range requests {
		if _, err := r.EnqueueIn(ctx, request.ActionName, request.Payload, request.Options); err != nil {
			return err
		}
	}
	return nil
}

// prepare 校验并编码一次任务投递，登记时的日志串联编号随任务传递。
func (r *Runtime) prepare(ctx context.Context, actionName string, payload any, options EnqueueOptions, id string) (json.RawMessage, []jetq.EnqueueOption, error) {
	if !r.registry.registered(actionName) {
		return nil, nil, fmt.Errorf("task action %q is not registered", actionName)
	}
	queue := strings.TrimSpace(options.Queue)
	if queue == "" {
		queue = QueueDefault
	}
	if !knownQueue(queue) {
		return nil, nil, fmt.Errorf("unknown task queue %q", queue)
	}
	if options.MaxAttempts < 0 {
		return nil, nil, errors.New("task max attempts must not be negative")
	}
	encoded, err := encodePayload(payload)
	if err != nil {
		return nil, nil, err
	}
	opts := []jetq.EnqueueOption{jetq.JobID(id), jetq.OnQueue(queue)}
	if options.MaxAttempts > 0 {
		opts = append(opts, jetq.MaxAttempts(options.MaxAttempts))
	}
	if key := strings.TrimSpace(options.IdempotencyKey); key != "" {
		opts = append(opts, jetq.UniqueUntilDone(actionName+":"+key))
	}
	if options.Delay > 0 && !options.At.IsZero() {
		return nil, nil, errors.New("task delay and at are mutually exclusive")
	}
	if options.Delay > 0 {
		opts = append(opts, jetq.Delay(options.Delay))
	}
	if !options.At.IsZero() {
		opts = append(opts, jetq.At(options.At))
	}
	if options.WorkspaceID != "" {
		opts = append(opts, jetq.WithHeader(headerWorkspace, options.WorkspaceID))
	}
	if route := strings.TrimSpace(options.Route); route != "" {
		opts = append(opts, jetq.WithHeader(headerRoute, route))
	}
	if traceID := logscope.From(ctx).TraceID; traceID != "" {
		opts = append(opts, jetq.WithHeader(headerTraceID, traceID))
	}
	return encoded, opts, nil
}

// publish 把任务写入 JetStream：同键任务尚未结束时视为已投递；投递结果不确定时返回错误并保留持久登记。
func (r *Runtime) publish(ctx context.Context, actionName string, job json.RawMessage, opts []jetq.EnqueueOption) error {
	_, err := r.client.EnqueueNamed(ctx, actionName, job, opts...)
	switch {
	case err == nil, errors.Is(err, jetq.ErrDuplicate):
		return nil

	}
	return fmt.Errorf("enqueue task %s: %w", actionName, err)
}

// encodePayload 将 Action 输入序列化为 JSON。
func encodePayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode task payload: %w", err)
	}
	return encoded, nil
}

// Start 同步定时计划、投递启动时立即执行的计划，并开始执行任务与清理过期路由租约。
func (r *Runtime) Start(parent context.Context) error {
	if err := r.syncSchedules(parent); err != nil {
		return err
	}
	worker := r.client.NewWorker(workerQueues()...)
	worker.SetShutdownTimeout(shutdownGracePeriod)
	worker.Use(r.executionScope, r.deferUnavailable)
	// jetq 记录重试、最终失败与结算异常的日志时沿用任务的日志作用域。
	worker.SetLogContext(func(ctx context.Context, info jetq.Info) context.Context { return r.scope(ctx, info, true) })
	for _, register := range r.registry.actions {
		register(worker)
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	r.outbox.Start(ctx)
	r.cancel = cancel
	r.wait.Add(2)
	go func() {
		defer r.wait.Done()
		if err := worker.Run(ctx); err != nil {
			slog.ErrorContext(ctx, "服务端任务执行退出", "error", err)
		}
	}()
	go func() {
		defer r.wait.Done()
		r.pruneExpired(ctx)
	}()
	slog.InfoContext(ctx, "服务端任务运行时已启动", "workers", WorkerCount(), "schedules", len(r.schedules))
	return nil
}

// Stop 停止领取新任务，等待在途任务结束后返回；超过停止宽限期的任务被取消并交给其他实例重新执行。
func (r *Runtime) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.outbox.Stop()
	r.wait.Wait()
	r.cancel = nil
	slog.InfoContext(context.Background(), "服务端任务运行时已停止")
}

// syncSchedules 把启用的定时计划同步到 NATS 服务端，并投递需要在启动时立即执行一次的计划。
func (r *Runtime) syncSchedules(ctx context.Context) error {
	schedules := make([]jetq.Schedule, 0, len(r.schedules))
	for _, definition := range r.schedules {
		if !definition.Enabled {
			continue
		}
		if !r.registry.registered(definition.ActionName) {
			return fmt.Errorf("schedule %q uses unregistered action %q", definition.Key, definition.ActionName)
		}
		if !knownQueue(definition.Queue) {
			return fmt.Errorf("schedule %q uses unknown queue %q", definition.Key, definition.Queue)
		}
		encoded, err := encodePayload(definition.Payload)
		if err != nil {
			return err
		}
		// 计划标识中的点号按横线写入主题片段。
		timezone := definition.Timezone
		if timezone == "UTC" {
			timezone = ""
		}
		schedules = append(schedules, jetq.Schedule{
			Key: strings.ReplaceAll(definition.Key, ".", "-"), Cron: definition.CronExpression,
			Name: definition.ActionName, Payload: encoded, Queue: definition.Queue,
			MaxAttempts: definition.MaxAttempts, Timezone: timezone,
		})
		if definition.StartImmediately {
			// 多个实例同时启动时按计划标识去重，只执行一次。
			if err := r.Enqueue(ctx, definition.ActionName, definition.Payload, EnqueueOptions{
				Queue: definition.Queue, MaxAttempts: definition.MaxAttempts, IdempotencyKey: "schedule-start:" + definition.Key,
			}); err != nil {
				return err
			}
		}
	}
	if err := r.client.SyncSchedules(ctx, schedules...); err != nil {
		return fmt.Errorf("sync task schedules: %w", err)
	}
	return nil
}

// executionScope 为任务调用写入执行尝试与日志作用域：沿用登记任务时的串联编号，记入任务编号、名称、队列与所属工作区。
func (r *Runtime) executionScope(ctx context.Context, next func(context.Context) error) error {
	info, _ := jetq.JobInfo(ctx)
	return next(r.scope(ctx, info, false))
}

// scope 返回携带任务执行尝试与日志作用域的上下文，finalizing 表示最终失败后的业务收尾。
func (r *Runtime) scope(ctx context.Context, info jetq.Info, finalizing bool) context.Context {
	ctx = withExecution(ctx, Execution{TaskRunID: info.ID, Attempt: info.Attempt, InstanceID: r.instanceID, Finalizing: finalizing})
	return logscope.With(ctx, logscope.Scope{
		TraceID: info.Header.Get(headerTraceID), TaskRunID: info.ID, Action: info.Name, Queue: info.Queue,
		WorkspaceID: info.Header.Get(headerWorkspace),
	})
}

// deferUnavailable 在所属工作区暂停或本实例未持有任务路由租约时推迟任务，推迟不计入尝试次数；所属工作区已删除或正在删除时任务直接最终失败。
func (r *Runtime) deferUnavailable(ctx context.Context, next func(context.Context) error) error {
	info, _ := jetq.JobInfo(ctx)
	if workspaceID := info.Header.Get(headerWorkspace); workspaceID != "" {
		var status domain.WorkspaceLifecycleStatus
		err := r.db.NewRaw("SELECT lifecycle_status FROM workspaces WHERE id = ?", workspaceID).Scan(ctx, &status)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read task workspace status: %w", err)
		}
		switch {
		case errors.Is(err, sql.ErrNoRows), status == domain.WorkspaceLifecycleDeleting, status == domain.WorkspaceLifecycleDeleted:
			return jetq.Permanent(fmt.Errorf("task workspace %s is removed", workspaceID))
		case status != domain.WorkspaceLifecycleActive:
			return jetq.Snooze(workspacePausedDelay)
		}
	}
	if route := info.Header.Get(headerRoute); route != "" {
		var held bool
		if err := r.db.NewRaw("SELECT ?", RouteHeldBy(route, r.InstanceID())).Scan(ctx, &held); err != nil {
			return fmt.Errorf("read task route: %w", err)
		}
		if !held {
			return jetq.Snooze(routeUnheldDelay)
		}
	}
	return next(ctx)
}

// jobLogHandler 写入 jetq 的日志，日志上下文已带任务队列时去掉同名的 queue 属性，队列由日志作用域写入。
type jobLogHandler struct{ slog.Handler }

// Handle 去掉与日志作用域重复的属性后写入日志。
func (h jobLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if logscope.From(ctx).Queue == "" {
		return h.Handler.Handle(ctx, record)
	}
	filtered := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key != "queue" {
			filtered.AddAttrs(attr)
		}
		return true
	})
	return h.Handler.Handle(ctx, filtered)
}

// WithAttrs 返回附加属性后的日志处理器。
func (h jobLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return jobLogHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup 返回进入属性分组后的日志处理器。
func (h jobLogHandler) WithGroup(name string) slog.Handler {
	return jobLogHandler{h.Handler.WithGroup(name)}
}
