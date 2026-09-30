//go:build server

package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/realtime"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
)

// mergeKey 标识发送队列中可合并的事件：变更通知按会话与种类，输入状态按会话与发送者，工作区动态按工作区、会话与原事件种类。
type mergeKey struct {
	frameType       protocol.Type
	conversationID  string
	senderSubjectID string
	workspaceID     string
	kind            protocol.Type
}

// connection 是一条成员实时事件流，写协程独占响应写入。
type connection struct {
	gateway *Gateway
	id      string
	cancel  context.CancelFunc

	// subjects、allowed、tokenSessionID 与 deviceID 在建立连接时写入，之后只读。
	subjects       []string
	allowed        map[protocol.Type]bool
	tokenSessionID string
	deviceID       string
	// workspaces 非空表示工作区动态事件流，按受众 Subject 记录所属工作区。
	workspaces map[string]string

	mu         sync.Mutex
	queue      []protocol.Frame
	merged     map[mergeKey]int
	controller *http.ResponseController
	// epoch 在清除队列时递增，写协程据此丢弃已取出但未发送的事件。
	epoch   int
	wake    chan struct{}
	closing bool
}

// newConnection 创建尚未输出事件流的连接，cancel 结束该连接的请求处理。
func newConnection(gateway *Gateway, cancel context.CancelFunc, route streamRoute) *connection {
	allowed := make(map[protocol.Type]bool, len(route.allowed))
	for _, frameType := range route.allowed {
		allowed[frameType] = true
	}
	return &connection{
		gateway:        gateway,
		id:             uuid.NewV7().String(),
		cancel:         cancel,
		subjects:       route.subjects,
		allowed:        allowed,
		tokenSessionID: route.tokenSessionID,
		deviceID:       route.deviceID,
		workspaces:     route.workspaces,
		merged:         map[mergeKey]int{},
		wake:           make(chan struct{}, 1),
	}
}

// run 按入队顺序写出事件并定期发送心跳，直到请求结束、写入失败或连接进入关闭状态且队列发送完毕。
func (c *connection) run(ctx context.Context, writer http.ResponseWriter, controller *http.ResponseController) {
	ping := time.NewTicker(c.gateway.options.PingInterval)
	defer ping.Stop()

	for {
		c.mu.Lock()
		frames, epoch := c.queue, c.epoch
		c.queue = nil
		clear(c.merged)
		c.mu.Unlock()

		for _, frame := range frames {
			c.mu.Lock()
			discarded := c.epoch != epoch
			c.mu.Unlock()
			if discarded {
				break
			}
			if !c.write(writer, controller, frame) {
				return
			}
		}

		c.mu.Lock()
		pending, closing := len(c.queue), c.closing
		c.mu.Unlock()
		if pending > 0 {
			continue
		}
		if closing {
			return
		}
		select {
		case <-c.wake:
		case <-ping.C:
			if !c.write(writer, controller, protocol.Ping{}) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// write 在写截止时间内以单条 SSE data 行写出事件并立即下发，失败时返回 false 结束事件流。
func (c *connection) write(writer http.ResponseWriter, controller *http.ResponseController, frame protocol.Frame) bool {
	return writeEventFrame(writer, controller, c.gateway.options.WriteTimeout, frame, "connection_id", c.id)
}

// send 把事件加入发送队列；受众可下发事件之外的事件直接丢弃，变更通知按会话与种类保留最高版本，输入状态按会话与发送者保留最新一条，队列溢出时按慢连接结束事件流。
func (c *connection) send(frame protocol.Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing || !c.allowed[frame.FrameType()] {
		return
	}
	target := mergeTarget(frame)
	if index, exists := c.merged[target.key]; target.mergeable && exists {
		// 会话变更保留更高版本并合并双方的变化类别。
		if changed, ok := frame.(protocol.ConversationChanged); ok {
			queued := c.queue[index].(protocol.ConversationChanged)
			changes := queued.Changes | changed.Changes
			if changed.Version > queued.Version {
				queued = changed
			}
			queued.Changes = changes
			c.queue[index] = queued
			return
		}
		// 工作区动态合并双方的会话变化类别，以后到事件为准。
		if activity, ok := frame.(protocol.WorkspaceActivity); ok {
			activity.Changes |= c.queue[index].(protocol.WorkspaceActivity).Changes
			c.queue[index] = activity
			return
		}
		// 其余变更通知保留更高版本，输入状态没有版本，以后到事件替换。
		if !target.versioned || target.version > mergeTarget(c.queue[index]).version {
			c.queue[index] = frame
		}
		return
	}
	if len(c.queue) >= c.gateway.options.QueueSize {
		slog.Warn("实时事件流发送队列溢出，按慢连接结束", "connection_id", c.id, "queued", len(c.queue))
		c.beginClose(true)
		return
	}
	if target.mergeable {
		c.merged[target.key] = len(c.queue)
	}
	c.queue = append(c.queue, frame)
	c.signal()
}

// mergeSource 描述事件在发送队列中的合并方式。
type mergeSource struct {
	key     mergeKey
	version int64
	// versioned 为真时只保留更高版本，否则以后到事件替换。
	versioned bool
	mergeable bool
}

// mergeTarget 返回变更通知、设备工作水位与输入状态事件的合并方式，其他事件不可合并。
func mergeTarget(frame protocol.Frame) mergeSource {
	switch value := frame.(type) {
	case protocol.ConversationChanged:
		return mergeSource{key: mergeKey{frameType: protocol.TypeConversationChanged, conversationID: value.ConversationID}, version: value.Version, versioned: true, mergeable: true}
	case protocol.ConversationStateChanged:
		return mergeSource{key: mergeKey{frameType: protocol.TypeConversationStateChanged, conversationID: value.ConversationID}, version: value.Version, versioned: true, mergeable: true}
	case protocol.ConversationTyping:
		return mergeSource{key: mergeKey{frameType: protocol.TypeConversationTyping, conversationID: value.ConversationID, senderSubjectID: value.SenderSubjectID}, mergeable: true}
	case protocol.VisitorTyping:
		return mergeSource{key: mergeKey{frameType: protocol.TypeVisitorTyping, conversationID: value.ConversationID}, mergeable: true}
	case protocol.ReceptionChanged:
		return mergeSource{key: mergeKey{frameType: protocol.TypeReceptionChanged}, mergeable: true}
	case protocol.IdentityProfileChanged:
		return mergeSource{key: mergeKey{frameType: protocol.TypeIdentityProfileChanged}, version: value.Version, versioned: true, mergeable: true}
	case protocol.WorkspaceActivity:
		// 客服提醒逐条下发，其余工作区动态按工作区、会话与原事件种类合并。
		if value.Kind == protocol.TypeServiceAttention {
			return mergeSource{}
		}
		return mergeSource{key: mergeKey{frameType: protocol.TypeWorkspaceActivity, conversationID: value.ConversationID, workspaceID: value.WorkspaceID, kind: value.Kind}, mergeable: true}
	case protocol.DeviceWorkAdvanced:
		return mergeSource{key: mergeKey{frameType: protocol.TypeDeviceWorkAdvanced}, version: value.WorkSeq, versioned: true, mergeable: true}
	}
	return mergeSource{}
}

// tokenSession 返回事件流所属登录会话编号。
func (c *connection) tokenSession() string { return c.tokenSessionID }

// audienceSubjects 返回事件流加入的受众 Subject。
func (c *connection) audienceSubjects() []string { return c.subjects }

// revoke 清除未发送的事件并结束事件流。
func (c *connection) revoke(kind realtime.Kind) {
	slog.Info("实时事件流已撤销", "connection_id", c.id, "kind", kind)
	c.close(true)
}

// shutdown 在网关下线时停止接收新事件，剩余事件发送完毕后结束事件流。
func (c *connection) shutdown() { c.close(false) }

// close 停止接收新事件，按需清除未发送的事件，由写协程发送剩余事件后结束事件流。
func (c *connection) close(discard bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	c.beginClose(discard)
}

// beginClose 在持有连接锁时进入关闭状态并唤醒写协程。
func (c *connection) beginClose(discard bool) {
	c.closing = true
	if discard {
		c.queue = nil
		clear(c.merged)
		c.epoch++
	}
	c.signal()
}

// attach 登记事件流的响应控制器，连接已进入关闭状态时返回 false。
func (c *connection) attach(controller *http.ResponseController) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return false
	}
	c.controller = controller
	return true
}

// abort 取消请求处理，已输出事件流时让阻塞中的写入立即超时。
func (c *connection) abort() {
	c.cancel()
	c.mu.Lock()
	controller := c.controller
	c.mu.Unlock()
	if controller != nil {
		_ = controller.SetWriteDeadline(time.Now())
	}
}

// signal 唤醒写协程，已有待处理唤醒时直接返回。
func (c *connection) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
