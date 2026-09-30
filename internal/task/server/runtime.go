//go:build server

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	serverconfig "github.com/runforyou-ai/cervi/internal/config/server"
	"github.com/uptrace/bun"
)

var (
	_ Enqueuer   = (*Runtime)(nil)
	_ TxEnqueuer = (*Runtime)(nil)
)

// workerPoolRuntime 保存一个 Worker Pool 的 Broker 与消费状态。
type workerPoolRuntime struct {
	config         workerPoolConfig
	consumer       jetstream.Consumer
	consumeContext jetstream.ConsumeContext
	jobs           chan jetstream.Msg
}

// Runtime 运行服务端异步 Action、定时计划和可靠消息投递。
type Runtime struct {
	config     runtimeConfig
	repository *repository
	registry   *Registry
	schedules  []ScheduleDefinition
	instanceID string

	cancel      context.CancelFunc
	cancelWork  context.CancelFunc
	connection  *nats.Conn
	jetstream   jetstream.JetStream
	workerPools []workerPoolRuntime
	waitGroup   sync.WaitGroup
}

// New 创建服务端任务运行时。
func New(db *bun.DB, natsConfig serverconfig.NATSConfig) *Runtime {
	runtime := &Runtime{
		config: newConfig(natsConfig),
		// 创建任务仓储。
		repository: &repository{db: db},
		registry:   NewRegistry(),
		instanceID: uuid.New().String(),
	}
	runtime.registerPruneRuns()
	return runtime
}

// Registry 返回 Action 注册表。
func (r *Runtime) Registry() *Registry {
	return r.registry
}

// RegisterSchedule 注册一个由代码管理的定时 Action。
func (r *Runtime) RegisterSchedule(definition ScheduleDefinition) {
	r.schedules = append(r.schedules, definition)
}

// Enqueue 将 Action 输入持久化并等待可靠发布。
func (r *Runtime) Enqueue(ctx context.Context, actionName string, payload any, options EnqueueOptions) (string, error) {
	encoded, normalized, err := r.prepareEnqueue(actionName, payload, options)
	if err != nil {
		return "", err
	}
	// 在独立事务内创建任务运行记录和发件箱消息。
	var runID string
	err = r.repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var enqueueErr error
		runID, enqueueErr = enqueueIn(ctx, tx, actionName, encoded, normalized, "")
		return enqueueErr
	})
	return runID, err
}

// EnqueueIn 将 Action 输入加入调用方已经开启的业务事务。
func (r *Runtime) EnqueueIn(ctx context.Context, tx bun.IDB, actionName string, payload any, options EnqueueOptions) (string, error) {
	encoded, normalized, err := r.prepareEnqueue(actionName, payload, options)
	if err != nil {
		return "", err
	}
	return enqueueIn(ctx, tx, actionName, encoded, normalized, "")
}

// EnqueueManyIn 将一批 Action 输入加入调用方已经开启的业务事务，按输入顺序返回运行编号。
func (r *Runtime) EnqueueManyIn(ctx context.Context, tx bun.IDB, requests []EnqueueRequest) ([]string, error) {
	pending := make([]pendingRun, len(requests))
	for index, request := range requests {
		encoded, normalized, err := r.prepareEnqueue(request.ActionName, request.Payload, request.Options)
		if err != nil {
			return nil, err
		}
		pending[index] = pendingRun{actionName: request.ActionName, payload: encoded, options: normalized}
	}
	return enqueueRunsIn(ctx, tx, pending)
}

// prepareEnqueue 校验并编码一次任务投递输入。
func (r *Runtime) prepareEnqueue(actionName string, payload any, options EnqueueOptions) (json.RawMessage, EnqueueOptions, error) {
	if _, exists := r.registry.lookup(actionName); !exists {
		return nil, options, fmt.Errorf("task action %q is not registered", actionName)
	}
	encoded, err := encodePayload(payload)
	if err != nil {
		return nil, options, err
	}
	normalized, err := normalizeEnqueueOptions(options)
	if err != nil {
		return nil, options, err
	}
	return encoded, normalized, nil
}

// Start 校验计划、连接 NATS 并启动服务端任务循环。
func (r *Runtime) Start(parent context.Context) error {
	startupCtx, startupCancel := context.WithTimeout(parent, r.config.StartupTimeout)
	defer startupCancel()
	if err := r.syncSchedules(startupCtx); err != nil {
		return err
	}
	if err := r.connectBroker(startupCtx); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	// 任务执行使用独立于拉取循环的上下文，停止时先停拉取再限时等待在途任务。
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(parent))
	r.cancelWork = cancelWork
	if err := r.startConsumers(ctx, workCtx); err != nil {
		cancel()
		cancelWork()
		r.stopConsumers()
		r.waitGroup.Wait()
		r.connection.Close()
		r.resetBroker()
		return err
	}
	r.waitGroup.Add(3)
	go r.runOutbox(ctx)
	go r.runExpiringMessageRecovery(ctx)
	go r.runScheduler(ctx)
	slog.Info("服务端任务运行时已启动",
		"namespace", r.config.Namespace,
		"stream", r.config.streamName(),
		"standard_consumer", r.config.consumerName(workerPoolStandard),
		"standard_workers", r.workerCount(workerPoolStandard),
		"agent_consumer", r.config.consumerName(workerPoolAgent),
		"agent_workers", r.workerCount(workerPoolAgent),
		"knowledge_consumer", r.config.consumerName(workerPoolKnowledge),
		"knowledge_workers", r.workerCount(workerPoolKnowledge),
		"delivery_consumer", r.config.consumerName(workerPoolDelivery),
		"delivery_workers", r.workerCount(workerPoolDelivery),
		"evaluation_consumer", r.config.consumerName(workerPoolEvaluation),
		"evaluation_workers", r.workerCount(workerPoolEvaluation),
		"schedules", len(r.schedules),
	)
	return nil
}

// Stop 停止拉取和调度，限时等待在途任务后取消剩余任务，并关闭 NATS 连接。
func (r *Runtime) Stop() error {
	if r.cancel == nil {
		return nil
	}
	r.cancel()
	r.stopConsumers()
	// 在途任务超出等待时长时取消执行，被取消的任务退回可领取状态。
	stopped := make(chan struct{})
	go func() {
		r.waitGroup.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(r.config.ShutdownGracePeriod):
		r.cancelWork()
		<-stopped
	}
	r.cancelWork()
	if r.connection != nil {
		if err := r.connection.Drain(); err != nil {
			r.connection.Close()
			r.resetBroker()
			return fmt.Errorf("drain NATS connection: %w", err)
		}
		r.connection.Close()
	}
	r.resetBroker()
	slog.Info("服务端任务运行时已停止", "namespace", r.config.Namespace)
	return nil
}

// workerCount 返回指定 Worker Pool 的并发数。
func (r *Runtime) workerCount(pool string) int {
	for _, item := range r.config.WorkerPools {
		if item.Name == pool {
			return item.Workers
		}
	}
	return 0
}

// resetBroker 清理已经停止的 Broker 生命周期状态。
func (r *Runtime) resetBroker() {
	r.cancel = nil
	r.cancelWork = nil
	r.connection = nil
	r.jetstream = nil
	r.workerPools = nil
}
