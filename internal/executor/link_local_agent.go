//go:build !server && !ios && !android

package executor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"
	"uuid"

	"github.com/cenkalti/backoff/v5"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// updateFlushInterval 是合并过程更新后上报的间隔。
	updateFlushInterval = 300 * time.Millisecond
	// maxUpdateBatch 是一次上报的过程更新条数上限。
	maxUpdateBatch = 200
)

// runLocalAgent 在这条连接的本机 Agent 会话中执行一轮，过程更新合并后按序上报，权限请求交给服务端裁决；全部过程更新上报完毕后返回结果，失败原因写入结果的 Error。
func (l *Link) runLocalAgent(ctx context.Context, operation appservice.ComputerOperationItem) domain.ComputerOutcome {
	agent, environment, dir, ok := l.options.Host.localAgent(ctx, operation.Operation.LocalAgent, operation.Operation.Folder)
	if !ok {
		return domain.ComputerOutcome{Error: fmt.Sprintf("这台电脑上没有本机 Agent %s。", operation.Operation.LocalAgent)}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.ComputerOutcome{Error: fmt.Sprintf("无法创建会话文件夹：%v", err)}
	}
	sink := newOperationSink(withOperationTrace(l.ctx, operation), l, operation.ID)
	outcome, err := l.agents.run(ctx, agent, environment, dir, operation.Operation, sink)
	sink.close()
	if err != nil {
		return domain.ComputerOutcome{Error: err.Error()}
	}
	return outcome
}

// operationSink 把一轮的过程更新合并后按序号上报，连续的回复或思考片段合并为一条，并把权限请求交给服务端裁决。
type operationSink struct {
	// ctx 是上报使用的 context：随连接结束取消，带有操作派发时的串联编号。
	ctx  context.Context
	link *Link
	id   string
	wake chan struct{}
	done chan struct{}
	// flushed 在上报协程退出后关闭。
	flushed chan struct{}

	// flushing 让上报依次进行。
	flushing sync.Mutex
	mu       sync.Mutex
	// seq 是最近一条已分配的序号。
	seq int
	// pending 是尚未上报的更新，序号连续。
	pending []appservice.ComputerOperationUpdate
}

// newOperationSink 创建操作的过程上报并启动上报协程，ctx 是上报使用的 context。
func newOperationSink(ctx context.Context, link *Link, id string) *operationSink {
	sink := &operationSink{ctx: ctx, link: link, id: id, wake: make(chan struct{}, 1), done: make(chan struct{}), flushed: make(chan struct{})}
	go sink.loop()
	return sink
}

// update 登记一条过程更新：与最后一条未上报的同类回复或思考片段合并，其余分配下一个序号。
func (s *operationSink) update(update domain.ToolCallUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last := len(s.pending) - 1; last >= 0 && update.Text != "" && s.pending[last].Update.Kind == update.Kind &&
		(update.Kind == domain.ToolCallUpdateMessage || update.Kind == domain.ToolCallUpdateThought) {
		s.pending[last].Update.Text += update.Text
		return
	}
	s.seq++
	s.pending = append(s.pending, appservice.ComputerOperationUpdate{Seq: s.seq, Update: update})
	signal(s.wake)
}

// permission 先上报已有的过程更新，再提交权限请求并等待领取时取回的裁决结果；ctx 结束时撤回等待并返回错误，日志与请求沿用这一轮的串联编号。
func (s *operationSink) permission(ctx context.Context, request domain.LocalAgentPermission) (string, error) {
	if traceID := logscope.From(s.ctx).TraceID; traceID != "" {
		ctx = logscope.WithTrace(ctx, traceID)
	}
	s.flush(ctx)
	id := uuid.NewV7().String()
	result := make(chan string, 1)
	s.link.mu.Lock()
	s.link.permissions[id] = result
	s.link.mu.Unlock()
	defer func() {
		s.link.mu.Lock()
		delete(s.link.permissions, id)
		s.link.mu.Unlock()
	}()
	if err := s.link.retry(ctx, func() error {
		return s.link.client.requestComputerPermission(ctx, s.id, appservice.ComputerPermissionInput{ID: id, Permission: request})
	}); err != nil {
		return "", err
	}
	slog.InfoContext(ctx, "本机 Agent 请求权限，等待裁决", "operation_id", s.id, "permission_id", id)
	select {
	case optionID := <-result:
		return optionID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// loop 按间隔或新更新唤醒上报，关闭后上报剩余更新再退出。
func (s *operationSink) loop() {
	defer close(s.flushed)
	ticker := time.NewTicker(updateFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			s.flush(s.ctx)
			return
		case <-ticker.C:
		case <-s.wake:
			// 合并短时间内连续到达的片段。
			time.Sleep(updateFlushInterval)
		}
		s.flush(s.ctx)
	}
}

// flush 依次上报全部尚未上报的更新，上报中的更新移出待上报队列，之后到达的片段不再并入；失败时按退避重试到成功或 ctx 结束，ctx 结束时放回队列。
// 同一时刻只有一次上报，序号按顺序送达。
func (s *operationSink) flush(ctx context.Context) {
	s.flushing.Lock()
	defer s.flushing.Unlock()
	for {
		s.mu.Lock()
		batch := s.pending[:min(len(s.pending), maxUpdateBatch)]
		s.pending = s.pending[len(batch):]
		s.mu.Unlock()
		if len(batch) == 0 {
			return
		}
		if err := s.link.retry(ctx, func() error {
			return s.link.client.reportComputerOperationUpdates(ctx, s.id, appservice.ComputerUpdatesInput{Updates: batch})
		}); err != nil {
			s.mu.Lock()
			s.pending = slices.Concat(batch, s.pending)
			s.mu.Unlock()
			return
		}
	}
}

// close 停止接收更新并等待剩余更新上报完毕。
func (s *operationSink) close() {
	close(s.done)
	<-s.flushed
}

// retry 执行请求，失败时按退避重试到成功、ctx 结束或服务端拒绝这条连接；服务端拒绝时返回该错误。
func (l *Link) retry(ctx context.Context, request func() error) error {
	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		err := request()
		if err != nil && l.rejected(err) {
			return struct{}{}, backoff.Permanent(err)
		}
		return struct{}{}, err
	}, backoff.WithBackOff(newRetryBackOff()), backoff.WithMaxElapsedTime(0), backoff.WithNotify(func(err error, _ time.Duration) {
		slog.WarnContext(ctx, "执行器请求失败，稍后重试", "server", l.options.ServerURL, "error", err)
	}))
	return err
}
