//go:build server

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

const (
	outboxPollInterval              = 250 * time.Millisecond
	expiringMessageRecoveryInterval = time.Minute
	expiringMessageRecoveryBatch    = 200
	taskFinalizationTimeout         = 15 * time.Second
	taskMessageDuplicateWindow      = 10 * time.Minute
	// taskNakRetryDelay 是消息暂不可处理时的 NAK 重投延迟。
	taskNakRetryDelay = 15 * time.Second
	// taskBrokerOperationTimeout 是 JetStream 发布和确认操作的超时。
	taskBrokerOperationTimeout = 5 * time.Second
)

type taskMessage struct {
	RunID string `json:"run_id"`
}

// connectBroker 连接 NATS 并确保当前命名空间的 JetStream 资源存在。
func (r *Runtime) connectBroker(ctx context.Context) error {
	connection, err := nats.Connect(
		r.config.URL,
		nats.Name("server-tasks-"+r.config.Namespace),
		nats.Timeout(r.config.StartupTimeout),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.Warn("NATS 连接断开", "namespace", r.config.Namespace, "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("NATS 已重新连接", "namespace", r.config.Namespace)
		}),
	)
	if err != nil {
		return fmt.Errorf("connect to NATS: %w", err)
	}
	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()
		return fmt.Errorf("create JetStream client: %w", err)
	}
	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        r.config.streamName(),
		Description: "Server asynchronous Action tasks (" + r.config.Namespace + ")",
		Subjects:    []string{r.config.subjectPrefix() + ".>"},
		Retention:   jetstream.WorkQueuePolicy,
		MaxBytes:    r.config.MaxBytes,
		MaxAge:      r.config.MaxAge,
		Discard:     jetstream.DiscardNew,
		Storage:     jetstream.FileStorage,
		Replicas:    r.config.Replicas,
		Duplicates:  taskMessageDuplicateWindow,
	}); err != nil {
		connection.Close()
		return fmt.Errorf("ensure JetStream task stream: %w", err)
	}
	pools := make([]workerPoolRuntime, 0, len(r.config.WorkerPools))
	for _, pool := range r.config.WorkerPools {
		consumer, err := js.CreateOrUpdateConsumer(ctx, r.config.streamName(), jetstream.ConsumerConfig{
			Name:          r.config.consumerName(pool.Name),
			Durable:       r.config.consumerName(pool.Name),
			Description:   "Server " + pool.Name + " Action workers (" + r.config.Namespace + ")",
			AckPolicy:     jetstream.AckExplicitPolicy,
			AckWait:       leaseDuration,
			MaxDeliver:    -1,
			FilterSubject: r.config.filterSubject(pool.Name),
			MaxAckPending: pool.MaxAckPending,
			Replicas:      r.config.Replicas,
		})
		if err != nil {
			connection.Close()
			return fmt.Errorf("ensure JetStream %s task consumer: %w", pool.Name, err)
		}
		pools = append(pools, workerPoolRuntime{config: pool, consumer: consumer})
	}
	r.connection = connection
	r.jetstream = js
	r.workerPools = pools
	return nil
}

// startConsumers 启动各自有界并发的 JetStream 消费器。
func (r *Runtime) startConsumers(ctx, workCtx context.Context) error {
	for index := range r.workerPools {
		if err := r.startConsumer(ctx, workCtx, &r.workerPools[index]); err != nil {
			return err
		}
	}
	return nil
}

// startConsumer 启动一个 Worker Pool 的 JetStream 消费器，ctx 控制拉取，workCtx 控制任务执行。
func (r *Runtime) startConsumer(ctx, workCtx context.Context, pool *workerPoolRuntime) error {
	jobs := make(chan jetstream.Msg, pool.config.Workers*2)
	pool.jobs = jobs
	consumeContext, err := pool.consumer.Consume(func(message jetstream.Msg) {
		select {
		case jobs <- message:
		case <-ctx.Done():
		}
	},
		jetstream.PullMaxMessages(pool.config.Workers*2),
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, err error) {
			if ctx.Err() == nil {
				slog.Warn("NATS 任务消费异常", "namespace", r.config.Namespace, "pool", pool.config.Name, "error", err)
			}
		}),
	)
	if err != nil {
		return fmt.Errorf("start JetStream %s task consumer: %w", pool.config.Name, err)
	}
	pool.consumeContext = consumeContext
	for index := range pool.config.Workers {
		workerID := fmt.Sprintf("%s-%s-%d", r.instanceID, pool.config.Name, index+1)
		r.waitGroup.Add(1)
		go func() {
			defer r.waitGroup.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case message := <-jobs:
					// 停止拉取后不再开始新任务，消息交回 JetStream 重投。
					if ctx.Err() != nil {
						_ = message.Nak()
						return
					}
					r.processMessage(workCtx, workerID, message)
				}
			}
		}()
	}
	return nil
}

// stopConsumers 停止所有任务拉取并等待回调退出，缓冲中尚未开始的消息交回 JetStream 重投。
func (r *Runtime) stopConsumers() {
	for index := range r.workerPools {
		if r.workerPools[index].consumeContext != nil {
			r.workerPools[index].consumeContext.Stop()
		}
	}
	for index := range r.workerPools {
		if r.workerPools[index].consumeContext != nil {
			<-r.workerPools[index].consumeContext.Closed()
		}
	}
	for index := range r.workerPools {
		for drained := false; !drained; {
			select {
			case message := <-r.workerPools[index].jobs:
				_ = message.Nak()
			default:
				drained = true
			}
		}
	}
}

// runOutbox 持续将 PostgreSQL 发件箱可靠发布到 JetStream。
func (r *Runtime) runOutbox(ctx context.Context) {
	defer r.waitGroup.Done()
	ticker := time.NewTicker(outboxPollInterval)
	defer ticker.Stop()
	for {
		published, err := r.publishOne(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("发布任务消息失败", "namespace", r.config.Namespace, "error", err)
		}
		if published {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runExpiringMessageRecovery 在 JetStream 清理消息前重建非终态任务消息。
func (r *Runtime) runExpiringMessageRecovery(ctx context.Context) {
	defer r.waitGroup.Done()
	ticker := time.NewTicker(expiringMessageRecoveryInterval)
	defer ticker.Stop()
	// 在消息保留期结束前留出一次安全重建窗口。
	margin := min(taskMessageDuplicateWindow, r.config.MaxAge/10)
	recoveryAge := r.config.MaxAge - margin
	for {
		recovered, err := r.repository.recoverExpiringMessages(
			ctx,
			time.Now().UTC().Add(-recoveryAge),
			expiringMessageRecoveryBatch,
		)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("重建即将过期的任务消息失败", "namespace", r.config.Namespace, "error", err)
			}
		} else if recovered > 0 {
			slog.Info("已重建即将过期的任务消息", "namespace", r.config.Namespace, "count", recovered)
			if recovered == expiringMessageRecoveryBatch {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// publishOne 发布一条发件箱消息。
func (r *Runtime) publishOne(ctx context.Context) (bool, error) {
	record, err := r.repository.claimOutbox(ctx)
	if err != nil || record == nil {
		return false, err
	}
	payload, err := json.Marshal(taskMessage{RunID: record.TaskRunID})
	if err != nil {
		return true, err
	}
	publishCtx, cancel := context.WithTimeout(ctx, taskBrokerOperationTimeout)
	_, publishErr := r.jetstream.Publish(
		publishCtx,
		r.config.taskSubject(record.QueueName),
		payload,
		jetstream.WithMsgID(record.MessageID),
	)
	cancel()
	if publishErr != nil {
		if releaseErr := r.repository.releaseOutbox(ctx, record, publishErr); releaseErr != nil {
			return true, fmt.Errorf("publish task: %v; release outbox: %w", publishErr, releaseErr)
		}
		return true, publishErr
	}
	if err := r.repository.markPublished(ctx, record.TaskRunID, record.MessageID); err != nil {
		return true, err
	}
	return true, nil
}

// processMessage 认领并执行一条任务消息。
func (r *Runtime) processMessage(ctx context.Context, workerID string, message jetstream.Msg) {
	var envelope taskMessage
	if err := json.Unmarshal(message.Data(), &envelope); err != nil || envelope.RunID == "" {
		slog.Warn("丢弃无效任务消息", "subject", message.Subject())
		_ = message.TermWithReason("invalid task envelope")
		return
	}
	run, err := r.repository.claimRun(ctx, envelope.RunID, workerID)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("认领异步任务失败", "run_id", envelope.RunID, "error", err)
			_ = message.NakWithDelay(taskNakRetryDelay)
		}
		return
	}
	if run == nil {
		r.handleUnclaimedMessage(ctx, envelope.RunID, message)
		return
	}
	handler, exists := r.registry.lookup(run.ActionName)
	if !exists {
		r.finishFailedMessage(ctx, run, workerID, message, Permanent(fmt.Errorf("task action %q is not registered", run.ActionName)))
		return
	}
	handlerCtx, cancelHandler := context.WithCancel(withExecution(ctx, run, false, false))
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	heartbeatResult := make(chan error, 1)
	go func() {
		heartbeatResult <- r.heartbeat(heartbeatCtx, run.ID, workerID, message, cancelHandler)
	}()
	runErr := executeHandler(handlerCtx, handler, run.Payload)
	cancelHeartbeat()
	heartbeatErr := <-heartbeatResult
	cancelHandler()
	if heartbeatErr != nil {
		slog.Warn("异步任务心跳中断", "run_id", run.ID, "action", run.ActionName, "error", heartbeatErr)
	}
	runErr = resolveExecutionError(runErr, heartbeatErr)
	if runErr != nil && ctx.Err() != nil {
		r.releaseInterruptedMessage(run, workerID, message)
		return
	}
	if runErr != nil {
		r.finishFailedMessage(ctx, run, workerID, message, runErr)
		return
	}
	finalizeCtx, cancelFinalize := taskFinalizationContext(ctx)
	err = r.repository.completeRun(finalizeCtx, run.ID, workerID)
	cancelFinalize()
	if err != nil {
		slog.Warn("提交异步任务成功状态失败", "run_id", run.ID, "action", run.ActionName, "error", err)
		_ = message.NakWithDelay(taskNakRetryDelay)
		return
	}
	ackCtx, cancel := context.WithTimeout(context.Background(), taskBrokerOperationTimeout)
	defer cancel()
	if err := message.DoubleAck(ackCtx); err != nil {
		slog.Warn("确认异步任务消息失败", "run_id", run.ID, "action", run.ActionName, "error", err)
		return
	}
	slog.Info("异步任务执行成功", "run_id", run.ID, "action", run.ActionName, "attempt", run.Attempt)
}

// releaseInterruptedMessage 把因运行时停止而中断的任务退回可领取状态并交回 JetStream 重投。
func (r *Runtime) releaseInterruptedMessage(run *servermodels.TaskRun, workerID string, message jetstream.Msg) {
	releaseCtx, cancel := context.WithTimeout(context.Background(), taskFinalizationTimeout)
	defer cancel()
	if err := r.repository.releaseRun(releaseCtx, run.ID, workerID); err != nil {
		slog.Warn("退回中断的异步任务失败", "run_id", run.ID, "action", run.ActionName, "error", err)
		return
	}
	_ = message.Nak()
	slog.Info("异步任务因运行时停止退回队列", "run_id", run.ID, "action", run.ActionName, "attempt", run.Attempt)
}

// resolveExecutionError 保留 Action 结果，仅用心跳原因替换协作取消错误。
func resolveExecutionError(runErr, heartbeatErr error) error {
	if heartbeatErr != nil && errors.Is(runErr, context.Canceled) && !IsPermanent(runErr) {
		return heartbeatErr
	}
	return runErr
}

// executeHandler 把 Action panic 转成可记录和重试的任务错误。
func executeHandler(ctx context.Context, handler func(context.Context, json.RawMessage) error, payload json.RawMessage) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task action panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	return handler(ctx, payload)
}

// heartbeat 维持消息确认期限和数据库租约，失去任一所有权时取消 Action。
func (r *Runtime) heartbeat(ctx context.Context, runID, workerID string, message jetstream.Msg, cancelHandler context.CancelFunc) error {
	ticker := time.NewTicker(leaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := message.InProgress(); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				cancelHandler()
				return fmt.Errorf("extend NATS task acknowledgement: %w", err)
			}
			held, err := r.repository.extendLease(ctx, runID, workerID)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				cancelHandler()
				return err
			}
			if !held {
				cancelHandler()
				return fmt.Errorf("task run %s lease lost", runID)
			}
		}
	}
}

// finishFailedMessage 提交 Action 错误并安排重试或终止消息。
func (r *Runtime) finishFailedMessage(ctx context.Context, run *servermodels.TaskRun, workerID string, message jetstream.Msg, runErr error) {
	willRetry := !IsPermanent(runErr) && run.Attempt < run.MaxAttempts
	if !willRetry {
		finalizeCtx, cancelFinalize := taskFinalizationContext(ctx)
		finalizeErr := r.finalizeActionFailure(finalizeCtx, run, runErr)
		cancelFinalize()
		if finalizeErr != nil {
			slog.Warn("提交异步任务业务失败终态失败", "run_id", run.ID, "action", run.ActionName, "error", finalizeErr)
			_ = message.NakWithDelay(taskNakRetryDelay)
			return
		}
	}
	finalizeCtx, cancelFinalize := taskFinalizationContext(ctx)
	retry, err := r.repository.failRun(finalizeCtx, run, workerID, runErr, IsPermanent(runErr))
	cancelFinalize()
	if err != nil {
		slog.Warn("提交异步任务失败状态失败", "run_id", run.ID, "action", run.ActionName, "error", err)
		_ = message.NakWithDelay(taskNakRetryDelay)
		return
	}
	if retry {
		ackCtx, cancel := context.WithTimeout(context.Background(), taskBrokerOperationTimeout)
		ackErr := message.DoubleAck(ackCtx)
		cancel()
		if ackErr != nil {
			slog.Warn("确认待重试任务消息失败", "run_id", run.ID, "action", run.ActionName, "error", ackErr)
		}
		slog.Warn("异步任务等待重试", "run_id", run.ID, "action", run.ActionName, "attempt", run.Attempt, "error", runErr)
		return
	}
	_ = message.TermWithReason(truncateError(runErr))
	slog.Error("异步任务执行失败", "run_id", run.ID, "action", run.ActionName, "attempt", run.Attempt, "error", runErr)
}

// finalizeActionFailure 在任务最终失败前提交已注册的业务收尾。
func (r *Runtime) finalizeActionFailure(ctx context.Context, run *servermodels.TaskRun, runErr error) error {
	finalize, exists := r.registry.lookupTerminalFailure(run.ActionName)
	if !exists {
		return nil
	}
	return finalize(withExecution(ctx, run, true, false), run.Payload, runErr)
}

// taskFinalizationContext 为任务终态写入保留独立的短超时窗口。
func taskFinalizationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), taskFinalizationTimeout)
}

// handleUnclaimedMessage 根据数据库状态处理重复或暂不可执行的消息。
func (r *Runtime) handleUnclaimedMessage(ctx context.Context, runID string, message jetstream.Msg) {
	// 读取任务当前状态。
	var run servermodels.TaskRun
	err := r.repository.db.NewSelect().Model(&run).Where("tr.id = ?", runID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		slog.Warn("丢弃不存在的任务消息", "run_id", runID)
		_ = message.TermWithReason("task run does not exist")
		return
	}
	if err != nil {
		slog.Warn("读取任务状态失败，等待重投", "run_id", runID, "error", err)
		_ = message.NakWithDelay(taskNakRetryDelay)
		return
	}
	if run.Status == statusRunning && run.Attempt >= run.MaxAttempts && run.LeaseExpiresAt != nil && !run.LeaseExpiresAt.After(time.Now()) {
		exhaustedErr := errors.New("task worker lease expired after final attempt")
		finalizeCtx, cancelFinalize := taskFinalizationContext(ctx)
		finalize, exists := r.registry.lookupTerminalFailure(run.ActionName)
		var finalizeErr error
		if exists {
			finalizeErr = finalize(withExecution(finalizeCtx, &run, true, true), run.Payload, exhaustedErr)
		}
		cancelFinalize()
		if finalizeErr != nil {
			slog.Warn("提交耗尽任务业务失败终态失败", "run_id", run.ID, "action", run.ActionName, "error", finalizeErr)
			_ = message.NakWithDelay(taskNakRetryDelay)
			return
		}
		if exhausted, failErr := r.repository.failExhaustedRun(ctx, runID); failErr != nil {
			slog.Warn("终结耗尽任务失败，等待重投", "run_id", runID, "action", run.ActionName, "error", failErr)
			_ = message.NakWithDelay(taskNakRetryDelay)
			return
		} else if exhausted {
			slog.Error("异步任务在最终尝试中失去租约", "run_id", runID, "action", run.ActionName)
			_ = message.TermWithReason(exhaustedErr.Error())
			return
		}
	}
	switch run.Status {
	case statusSucceeded, statusFailed:
		ackCtx, cancel := context.WithTimeout(context.Background(), taskBrokerOperationTimeout)
		_ = message.DoubleAck(ackCtx)
		cancel()
	default:
		delay := time.Until(run.AvailableAt)
		if run.Status == statusRunning && run.LeaseExpiresAt != nil {
			delay = time.Until(*run.LeaseExpiresAt)
		}
		if delay < taskNakRetryDelay {
			delay = taskNakRetryDelay
		}
		_ = message.NakWithDelay(min(delay, time.Minute))
	}
}
