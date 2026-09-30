package protocol

import (
	"log/slog"
	"sync"

	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
)

// RunStreamQueue 缓存一条运行过程流待写出的事件：快照分片先于增量，待发增量按序号相接合并为一条，源运行流结束后补发结束事件。
// 入队方法只更新缓存并唤醒写协程，可在运行流锁内调用；事件由单个写协程经 Take 取出后写出。
type RunStreamQueue struct {
	runID            string
	pendingTextBytes int
	wake             chan struct{}

	mu       sync.Mutex
	snapshot []RunStreamSnapshot
	delta    *runstream.Delta
	// ended 表示源运行流已结束，closing 表示事件流已关闭且不再写出任何事件。
	ended   bool
	closing bool
}

// NewRunStreamQueue 创建指定运行的事件队列，待发增量文本超过 pendingTextBytes 时按慢消费者关闭。
func NewRunStreamQueue(runID string, pendingTextBytes int) *RunStreamQueue {
	return &RunStreamQueue{runID: runID, pendingTextBytes: pendingTextBytes, wake: make(chan struct{}, 1)}
}

// Wake 返回有新事件或状态变化时收到信号的通道。
func (q *RunStreamQueue) Wake() <-chan struct{} {
	return q.wake
}

// Snapshot 把订阅时的快照按文本预算拆分为分片，排在全部增量之前写出。
func (q *RunStreamQueue) Snapshot(snapshot runstream.Snapshot, partBytes int) {
	parts := RunStreamSnapshotParts(snapshot, partBytes)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closing {
		return
	}
	q.snapshot = parts
	q.signal()
}

// Publish 合并待发增量；增量不相接或合并后超出文本上限时关闭事件流，由客户端重新取快照。
func (q *RunStreamQueue) Publish(delta runstream.Delta) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closing || q.ended {
		return
	}
	if q.delta == nil {
		q.delta = &delta
	} else if merged, ok := runstream.MergeDeltas(*q.delta, delta); ok {
		q.delta = &merged
	} else {
		slog.Warn("运行过程流待发增量不相接，结束事件流", "agent_run_id", q.runID,
			"pending_sequence", q.delta.Sequence, "base_sequence", delta.BaseSequence)
		q.closeLocked()
		return
	}
	if size := q.delta.TextBytes(); size > q.pendingTextBytes {
		slog.Warn("运行过程流待发增量超出上限，按慢消费者结束", "agent_run_id", q.runID, "pending_bytes", size)
		q.closeLocked()
		return
	}
	q.signal()
}

// Finish 登记源运行流已结束，剩余事件取出后补发结束事件。
func (q *RunStreamQueue) Finish() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closing || q.ended {
		return
	}
	q.ended = true
	q.signal()
}

// Close 清除未写出的事件并关闭事件流，不补发结束事件。
func (q *RunStreamQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeLocked()
}

// Closed 判断事件流是否已关闭。
func (q *RunStreamQueue) Closed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closing
}

// Take 按序取出待写出的事件，done 为 true 表示写出本批事件后事件流结束：源运行流已结束且没有其他待写事件时本批只含结束事件，事件流已关闭时本批为空。
func (q *RunStreamQueue) Take() ([]Frame, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closing {
		return nil, true
	}
	frames := make([]Frame, 0, len(q.snapshot)+1)
	for _, part := range q.snapshot {
		frames = append(frames, part)
	}
	if q.delta != nil {
		frames = append(frames, RunStreamDeltaFrame(*q.delta))
	}
	q.snapshot, q.delta = nil, nil
	if len(frames) == 0 && q.ended {
		return []Frame{RunStreamEnded{RunID: q.runID}}, true
	}
	return frames, false
}

// closeLocked 在持有队列锁时关闭事件流、清除未写出的事件并唤醒写协程。
func (q *RunStreamQueue) closeLocked() {
	q.closing = true
	q.snapshot, q.delta = nil, nil
	q.signal()
}

// signal 唤醒写协程，已有待处理唤醒时直接返回。
func (q *RunStreamQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
