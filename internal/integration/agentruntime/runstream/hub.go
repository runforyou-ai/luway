package runstream

import (
	"log/slog"
	"sync"
)

// Hub 保存一次执行尝试运行流的最新快照，并按序向订阅方推送之后的增量。
type Hub struct {
	mu          sync.Mutex
	snapshot    Snapshot
	subscribers map[*Subscription]struct{}
	ended       bool
}

// Subscription 定义一次运行流订阅，回调在运行流锁内串行执行，不得阻塞，也不得调用 Close 或 Subscribe。
type Subscription struct {
	hub     *Hub
	onDelta func(Delta)
	onEnd   func()
}

// NewHub 创建从指定快照开始的运行流。
func NewHub(snapshot Snapshot) *Hub {
	return &Hub{snapshot: snapshot, subscribers: make(map[*Subscription]struct{})}
}

// Publish 把增量应用到快照并推送给订阅方，增量无法应用时结束运行流。
func (h *Hub) Publish(delta Delta) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	if _, err := h.snapshot.Apply(delta); err != nil {
		slog.Warn("Agent 运行流增量无法应用，结束运行流",
			"agent_run_id", delta.RunID, "stream_id", delta.StreamID, "sequence", delta.Sequence, "error", err)
		h.endLocked()
		return
	}
	for subscription := range h.subscribers {
		subscription.onDelta(delta)
	}
}

// Subscribe 返回当前快照并登记之后的增量回调，运行流已结束时返回 false。
func (h *Hub) Subscribe(onDelta func(Delta), onEnd func()) (Snapshot, *Subscription, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return Snapshot{}, nil, false
	}
	subscription := &Subscription{hub: h, onDelta: onDelta, onEnd: onEnd}
	h.subscribers[subscription] = struct{}{}
	return h.snapshot.Clone(), subscription, true
}

// End 在执行尝试退出时结束运行流。
func (h *Hub) End() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.endLocked()
}

// endLocked 标记运行流结束并通知全部订阅方，调用方持有运行流锁；已结束时直接返回。
func (h *Hub) endLocked() {
	if h.ended {
		return
	}
	h.ended = true
	for subscription := range h.subscribers {
		delete(h.subscribers, subscription)
		subscription.onEnd()
	}
}

// Close 取消订阅并停止回调。
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	delete(s.hub.subscribers, s)
}
