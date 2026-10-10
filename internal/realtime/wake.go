//go:build server

package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// wakeReconnectCheckInterval 是检查消息总线连接状态的间隔，连接恢复时唤醒全部等待信号的订阅方补查一次。
const wakeReconnectCheckInterval = time.Second

// wakeRegistry 是本进程等待唤醒信号的订阅，按主题登记补发信号的函数。
var wakeRegistry = struct {
	sync.Mutex
	topics map[string]map[*chan struct{}]func()
}{topics: map[string]map[*chan struct{}]func(){}}

// AgentLaneInputAdded 构造输入队列新增持久输入的唤醒信号，正在执行该队列运行的服务端实例据此读取新增输入。
func AgentLaneInputAdded(workspaceID, laneID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceAgentLane, AudienceID: laneID, Kind: KindAgentInputAdded}
}

// AgentToolCallSettled 构造工具调用已写入结果的唤醒信号，等待该调用结果的服务端实例据此读取调用记录。
func AgentToolCallSettled(workspaceID, toolCallID string) Notification {
	return Notification{WorkspaceID: workspaceID, AudienceKind: AudienceAgentToolCall, AudienceID: toolCallID, Kind: KindAgentToolCallSettled}
}

// WatchAgentLaneInputs 订阅输入队列的新增输入信号，返回合并后的信号通道与取消订阅函数。
func WatchAgentLaneInputs(workspaceID, laneID string) (<-chan struct{}, func()) {
	return watchWake(Topic(workspaceID, AudienceAgentLane, laneID))
}

// WatchAgentToolCallSettled 订阅工具调用的结果写入信号，返回合并后的信号通道与取消订阅函数。
func WatchAgentToolCallSettled(workspaceID, toolCallID string) (<-chan struct{}, func()) {
	return watchWake(Topic(workspaceID, AudienceAgentToolCall, toolCallID))
}

// watchWake 在本进程登记主题的唤醒订阅，发布器已启动时同时经消息总线订阅并等待订阅生效，返回容量为 1 的信号通道：
// 短时间内多次信号合并为一次，消息总线断开后恢复连接时补发一次信号；总线订阅失败时只收到本进程信号，由调用方的兜底查询覆盖。
func watchWake(topic string) (<-chan struct{}, func()) {
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	wakeRegistry.Lock()
	subscriptions := wakeRegistry.topics[topic]
	if subscriptions == nil {
		subscriptions = map[*chan struct{}]func(){}
		wakeRegistry.topics[topic] = subscriptions
	}
	subscriptions[&wake] = signal
	wakeRegistry.Unlock()
	unregister := func() {
		wakeRegistry.Lock()
		defer wakeRegistry.Unlock()
		delete(wakeRegistry.topics[topic], &wake)
		if len(wakeRegistry.topics[topic]) == 0 {
			delete(wakeRegistry.topics, topic)
		}
	}
	publisher := active.Load()
	if publisher == nil {
		return wake, unregister
	}
	unsubscribe, err := publisher.bus.Subscribe(topic, func([]byte) { signal() })
	if err != nil {
		slog.WarnContext(context.Background(), "订阅唤醒信号失败", "topic", topic, "error", err)
		return wake, unregister
	}
	ctx, cancel := context.WithTimeout(context.Background(), subscribeSyncTimeout)
	defer cancel()
	if err := publisher.bus.Sync(ctx); err != nil {
		slog.WarnContext(ctx, "唤醒信号订阅确认失败", "topic", topic, "error", err)
	}
	return wake, func() {
		unsubscribe()
		unregister()
	}
}

// signalLocalWakes 把已提交事务中的唤醒信号直接送达本进程的订阅。
func signalLocalWakes(notifications []Notification) {
	wakeRegistry.Lock()
	defer wakeRegistry.Unlock()
	for _, notification := range notifications {
		if notification.AudienceKind != AudienceAgentLane && notification.AudienceKind != AudienceAgentToolCall {
			continue
		}
		for _, signal := range wakeRegistry.topics[Topic(notification.WorkspaceID, notification.AudienceKind, notification.AudienceID)] {
			signal()
		}
	}
}

// watchReconnect 按间隔检查消息总线连接状态，从断开恢复为可用时向本进程全部唤醒订阅补发一次信号，直到发布器停止。
func (p *Publisher) watchReconnect() {
	defer close(p.reconnectDone)
	ticker := time.NewTicker(wakeReconnectCheckInterval)
	defer ticker.Stop()
	connected := p.bus.Connected()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
		}
		current := p.bus.Connected()
		if current && !connected {
			slog.InfoContext(context.Background(), "消息总线已恢复连接，唤醒等待信号的订阅")
			wakeRegistry.Lock()
			for _, subscriptions := range wakeRegistry.topics {
				for _, signal := range subscriptions {
					signal()
				}
			}
			wakeRegistry.Unlock()
		}
		connected = current
	}
}
