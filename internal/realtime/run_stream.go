//go:build server

package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/runforyou-ai/einorun/stream"
	"github.com/runforyou-ai/luway/internal/common/logscope"
)

// RunSnapshot 是一次执行尝试的运行流快照：所属运行、执行尝试序号与展示状态，运行流标识即执行尝试的流编号。
type RunSnapshot struct {
	RunID   string
	Attempt int
	stream.Snapshot
}

// Clone 复制快照供独立读取。
func (s RunSnapshot) Clone() RunSnapshot {
	s.Snapshot = s.Snapshot.Clone()
	return s
}

// RunStreamEvent 是待投影到实时契约的运行增量或快照失效信号。
type RunStreamEvent struct {
	RunID    string
	StreamID string
	Attempt  int
	Sequence int64
	Delta    *stream.Delta
	// Invalidated 表示本批次的增量无法发布，客户端需要重读快照。
	Invalidated bool
	Ended       bool
}

// RunStreamSource 保存执行尝试的快照，按 50ms 合并增量后发布。
type RunStreamSource struct {
	publisher   *Publisher
	workspaceID string
	scope       logscope.Scope
	// admission 是执行方确认本次执行时分配的本实例准入序号，序号更大的登记取代旧登记。
	admission   uint64
	mu          sync.Mutex
	snapshot    RunSnapshot
	pending     *stream.Delta
	invalidated bool
	ended       bool
	stop        chan struct{}
	done        chan struct{}
}

// RunChannel 返回一个运行的精确私有频道。
func RunChannel(workspaceID, runID string) string { return "w." + workspaceID + ".runs." + runID }

// OpenRunStream 在当前执行实例按准入序号登记运行快照。
func OpenRunStream(ctx context.Context, workspaceID string, admission uint64, snapshot RunSnapshot) *RunStreamSource {
	return active.Load().OpenRunStream(ctx, workspaceID, admission, snapshot)
}

// OpenRunStream 按本实例的准入序号登记执行源：准入更晚的登记取代并结束旧登记，本实例已登记准入更晚的执行时返回不发布的执行源。
func (p *Publisher) OpenRunStream(ctx context.Context, workspaceID string, admission uint64, snapshot RunSnapshot) *RunStreamSource {
	s := &RunStreamSource{publisher: p, workspaceID: workspaceID, scope: logscope.From(ctx), admission: admission, snapshot: snapshot.Clone(), stop: make(chan struct{}), done: make(chan struct{})}
	if p == nil {
		close(s.done)
		return s
	}
	p.sourcesMu.Lock()
	previous := p.sources[snapshot.RunID]
	if previous != nil && previous.admission > admission {
		p.sourcesMu.Unlock()
		s.publisher = nil
		close(s.done)
		return s
	}
	p.sources[snapshot.RunID] = s
	p.sourcesMu.Unlock()
	if previous != nil {
		previous.End()
	}
	go s.run()
	return s
}

// Publish 应用业务增量并合并待发布内容，大增量通过快照恢复。
func (s *RunStreamSource) Publish(delta stream.Delta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	applied, err := s.snapshot.Apply(delta)
	if err != nil {
		s.invalidated = true
		slog.WarnContext(logscope.With(context.Background(), s.scope), "运行流增量无法应用", "agent_run_id", s.snapshot.RunID, "error", err)
		return
	}
	if !applied {
		return
	}
	if s.invalidated {
		return
	}
	if s.pending == nil {
		s.pending = &delta
	} else if merged, ok := stream.MergeDeltas(*s.pending, delta); ok {
		s.pending = &merged
	} else {
		s.invalidated = true
	}
	if s.pending != nil && (s.pending.TextBytes() > 256*1024 || len(s.pending.Operations) > 1024) {
		s.invalidated = true
	}
	if s.invalidated {
		s.pending = nil
	}
}

// run 定期发布合并后的增量，发布失败后下一批发送快照失效信号。
func (s *RunStreamSource) run() {
	defer close(s.done)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			s.flush(true)
			return
		case <-ticker.C:
			s.flush(false)
		}
	}
}

// flush 在锁外发布当前批次，失败时保留恢复信号。
func (s *RunStreamSource) flush(ended bool) {
	s.mu.Lock()
	if !ended && s.pending == nil && !s.invalidated {
		s.mu.Unlock()
		return
	}
	event := RunStreamEvent{RunID: s.snapshot.RunID, StreamID: s.snapshot.Stream, Attempt: s.snapshot.Attempt, Sequence: s.snapshot.Sequence, Delta: s.pending, Invalidated: s.invalidated, Ended: ended}
	s.pending, s.invalidated = nil, false
	s.mu.Unlock()
	if s.publisher.publishRun == nil {
		return
	}
	ctx, cancel := context.WithTimeout(logscope.With(context.Background(), s.scope), 3*time.Second)
	defer cancel()
	if err := s.publisher.publishRun(ctx, s.workspaceID, event); err != nil {
		s.mu.Lock()
		s.pending, s.invalidated = nil, true
		s.mu.Unlock()
		slog.WarnContext(ctx, "发布运行流事件失败", "agent_run_id", event.RunID, "error", err)
	}
}

// End 刷新最后一批并注销执行快照，持久状态由业务查询确认。
func (s *RunStreamSource) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		<-s.done
		return
	}
	s.ended = true
	close(s.stop)
	s.mu.Unlock()
	<-s.done
	if s.publisher != nil {
		s.publisher.sourcesMu.Lock()
		if s.publisher.sources[s.snapshot.RunID] == s {
			delete(s.publisher.sources, s.snapshot.RunID)
		}
		s.publisher.sourcesMu.Unlock()
	}
}
