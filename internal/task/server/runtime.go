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

	cancel context.CancelFunc
	wait   sync.WaitGroup
}

// Options 定义任务队列在 NATS 中的位置：Namespace 区分同一 NATS 上的部署，Replicas 是任务 stream 的副本数。
type Options struct {
	Namespace string
	Replicas  int
}

// New 创建服务端任务运行时并确保 JetStream 中的任务 stream 存在：db 用于读取工作区状态与路由租约，
// leaseDB 是续期路由租约专用的连接池，instanceID 是本进程的实例编号。
func New(ctx context.Context, js jetstream.JetStream, options Options, db, leaseDB *bun.DB, instanceID string) (*Runtime, error) {
	client, err := jetq.New(ctx, js,
		jetq.WithStreamName(strings.ToUpper(options.Namespace)+"_JOBS"),
		jetq.WithSubjectPrefix(options.Namespace+"-jobs"),
		jetq.WithReplicas(max(options.Replicas, 1)),
		jetq.WithDeadLetterMaxAge(FailureRetention),
		jetq.WithUniqueLockTTL(uniqueLockTTL),
		jetq.WithLogger(slog.New(jobLogHandler{slog.Default().Handler()})),
	)
	if err != nil {
		return nil, fmt.Errorf("create task queue: %w", err)
	}
	return &Runtime{Routes: NewRoutes(leaseDB, instanceID), client: client, db: db, registry: NewRegistry()}, nil
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
	job, opts, err := r.prepare(ctx, actionName, payload, options, id)
	if err != nil {
		return "", err
	}
	serverstorage.AfterCommit(ctx, func(ctx context.Context) error { return r.publish(ctx, actionName, job, opts) })
	return id, nil
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
func (r *Runtime) prepare(ctx context.Context, actionName string, payload any, options EnqueueOptions, id string) (jetq.RawJob, []jetq.EnqueueOption, error) {
	if !r.registry.registered(actionName) {
		return jetq.RawJob{}, nil, fmt.Errorf("task action %q is not registered", actionName)
	}
	queue := strings.TrimSpace(options.Queue)
	if queue == "" {
		queue = QueueDefault
	}
	if !knownQueue(queue) {
		return jetq.RawJob{}, nil, fmt.Errorf("unknown task queue %q", queue)
	}
	if options.MaxAttempts < 0 {
		return jetq.RawJob{}, nil, errors.New("task max attempts must not be negative")
	}
	encoded, err := encodePayload(payload)
	if err != nil {
		return jetq.RawJob{}, nil, err
	}
	opts := []jetq.EnqueueOption{jetq.JobID(id), jetq.OnQueue(queue)}
	if options.MaxAttempts > 0 {
		opts = append(opts, jetq.MaxAttempts(options.MaxAttempts))
	}
	if key := strings.TrimSpace(options.IdempotencyKey); key != "" {
		opts = append(opts, jetq.UniqueUntilDone(actionName+":"+key))
	}
	if options.Delay > 0 {
		opts = append(opts, jetq.Delay(options.Delay))
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
	return jetq.RawJob{Name: actionName, Payload: encoded}, opts, nil
}

// publish 把任务写入 JetStream：同键任务尚未结束时视为已投递；投递结果不确定时任务可能已写入，按已投递处理并记录错误。
func (r *Runtime) publish(ctx context.Context, actionName string, job jetq.RawJob, opts []jetq.EnqueueOption) error {
	id, err := r.client.Enqueue(ctx, job, opts...)
	switch {
	case err == nil, errors.Is(err, jetq.ErrDuplicate):
		return nil
	case errors.Is(err, jetq.ErrUncertain):
		slog.ErrorContext(ctx, "任务投递结果不确定", "task_name", actionName, "task_id", id, "error", err)
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
	for name, action := range r.registry.actions {
		worker.HandleRaw(name, func(ctx context.Context, payload json.RawMessage) error { return action.handler(ctx, payload) }, jetq.OnRawFailure(r.terminalFailure(action)))
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
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
		encoded, err := encodePayload(definition.Payload)
		if err != nil {
			return err
		}
		opts := []jetq.EnqueueOption{jetq.OnQueue(definition.Queue)}
		if definition.MaxAttempts > 0 {
			opts = append(opts, jetq.MaxAttempts(definition.MaxAttempts))
		}
		// 计划标识中的点号不能出现在主题片段中，按横线写入。
		schedule := jetq.Cron(strings.ReplaceAll(definition.Key, ".", "-"), definition.CronExpression, jetq.RawJob{Name: definition.ActionName, Payload: encoded}, opts...)
		if definition.Timezone != "" && definition.Timezone != "UTC" {
			schedule = schedule.In(definition.Timezone)
		}
		schedules = append(schedules, schedule)
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

// terminalFailure 返回任务最终失败时执行业务收尾的回调，回调上下文已携带任务的日志作用域与收尾执行尝试；收尾失败只记录日志。
func (r *Runtime) terminalFailure(action registeredAction) func(context.Context, json.RawMessage, error) {
	return func(ctx context.Context, payload json.RawMessage, runErr error) {
		if action.terminalFailure == nil {
			return
		}
		if err := action.terminalFailure(ctx, payload, runErr); err != nil {
			slog.ErrorContext(ctx, "异步任务失败收尾出错", "error", err)
		}
	}
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
