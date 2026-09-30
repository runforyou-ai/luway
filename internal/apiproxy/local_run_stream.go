//go:build !server

package apiproxy

import (
	"log/slog"

	"github.com/runforyou-ai/cervi/internal/integration/agentruntime/runstream"
	"github.com/runforyou-ai/cervi/internal/realtime/protocol"
)

const (
	// localRunSnapshotPartBytes 是本机运行过程流快照单个分片的文本预算。
	localRunSnapshotPartBytes = 256 << 10
	// localRunPendingTextBytes 是本机运行过程流待发增量合并后的文本上限，超过即按慢消费者关闭，由界面重新取快照。
	localRunPendingTextBytes = 256 << 10
)

// LocalRunStreams 提供本机登记执行的运行的过程流。
type LocalRunStreams interface {
	// RunsLocally 判断运行是否已在本机登记执行。
	RunsLocally(runID string) bool
	// SubscribeLocalRunStream 订阅本机运行的过程流，返回订阅时的快照与取消订阅函数，回调在运行流锁内执行且不得阻塞；运行不在本机或过程流已结束时返回 false。
	SubscribeLocalRunStream(runID string, onDelta func(runstream.Delta), onEnd func()) (runstream.Snapshot, func(), bool)
}

// UseLocalRunStreams 让运行过程流优先读取本机执行的运行，在应用启动前调用。
func (b *Backend) UseLocalRunStreams(streams LocalRunStreams) {
	b.realtime.local = streams
}

// runsLocally 判断运行是否在本机执行。
func (c *realtimeClient) runsLocally(runID string) bool {
	return c.local != nil && c.local.RunsLocally(runID)
}

// startLocalRun 把本机运行的过程流登记为指定窗口的运行过程流并启动投递协程；运行不在本机、过程流已结束或通道代次已变化时返回 false。
func (c *realtimeClient) startLocalRun(owner string, generation int, runID string) (*realtimeSession, bool) {
	if c.local == nil {
		return nil, false
	}
	// 运行流回调只入队，事件由投递协程在锁外按序写出。
	queue := protocol.NewRunStreamQueue(runID, localRunPendingTextBytes)
	snapshot, unsubscribe, running := c.local.SubscribeLocalRunStream(runID, queue.Publish, queue.Finish)
	if !running {
		return nil, false
	}
	queue.Snapshot(snapshot, localRunSnapshotPartBytes)
	session := c.newRunSession(owner, runID, queue.Close)
	if !c.register(session, generation) {
		unsubscribe()
		return nil, false
	}
	go c.deliverLocal(session, queue, unsubscribe)
	return session, true
}

// deliverLocal 按序把队列中的快照分片、增量与结束事件经 Wails 事件投递给前端，事件流结束或关闭后取消订阅、解除登记并投递关闭事件。
func (c *realtimeClient) deliverLocal(session *realtimeSession, queue *protocol.RunStreamQueue, unsubscribe func()) {
	defer func() {
		unsubscribe()
		session.releaseAfter(session)
		session.emitClosed(session)
	}()
	for {
		frames, done := queue.Take()
		for _, frame := range frames {
			data, err := protocol.Encode(frame)
			if err != nil {
				slog.Warn("编码本机运行过程事件失败", "connection_id", session.id, "type", frame.FrameType(), "error", err)
				continue
			}
			session.emitFrame(session, string(data))
		}
		if done {
			return
		}
		if len(frames) == 0 {
			<-queue.Wake()
		}
	}
}
