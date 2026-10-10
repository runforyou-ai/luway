//go:build server

package realtime

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/clusterbus"
	"github.com/runforyou-ai/support/arr"
)

const (
	// pendingLimit 是等待发布的不同合并键通知上限；同一合并键的通知在队列中合并，不占用新位置。
	pendingLimit = 16384
	// pendingSignalReserve 是客户端通知达到 pendingLimit 后为服务端实例之间的信号保留的位置数。
	pendingSignalReserve = 4096
	// pendingHardLimit 是等待发布的不同合并键通知总量上限，达到后服务端实例之间的信号同样丢弃。
	pendingHardLimit = pendingLimit + pendingSignalReserve
	// revocationConcurrency 是同一批已提交通知中并发执行的授权撤销数。
	revocationConcurrency = 16
	// subscribeSyncTimeout 是等待运行结束通知订阅生效的时限。
	subscribeSyncTimeout = 3 * time.Second
)

// active 是当前进程接收已提交通知的发布器，由组合根在启动全部会写入通知的组件之前启动、在它们停止之后停止；
// 为空时客户端通知丢弃并记录 WARN，由客户端兜底探针恢复，唤醒信号仍在进程内送达。
var active atomic.Pointer[Publisher]

// droppedWarned 标记发布器为空期间是否已记录丢弃通知的 WARN，每段空缺只记录一次，发布器启动时重置。
var droppedWarned atomic.Bool

// Publisher 在提交后执行授权撤销，经 jetcast 或消息总线合并发布业务通知，并发布运行流。
type Publisher struct {
	membersMu sync.RWMutex
	members   MemberPublisher
	bus       *clusterbus.Bus
	send      func(topic string, data []byte) error
	// ready 在有通知等待发布时唤醒发布协程，容量为 1。
	ready chan struct{}
	stop  chan struct{}
	done  chan struct{}

	// pending 是等待发布的通知，按合并键合并；sequence 是最近一次入队的序号，发布时按序号排序。
	pendingMu sync.Mutex
	pending   map[mergeKey]queuedNotification
	sequence  uint64
	// dropped 是达到 pendingLimit 时丢弃的客户端通知累计条数。
	dropped atomic.Int64
	// droppedSignals 是达到 pendingHardLimit 时丢弃的服务端实例之间信号累计条数。
	droppedSignals atomic.Int64

	// sources 是本实例正在执行的运行流，按运行编号登记。
	sourcesMu   sync.Mutex
	sources     map[string]*RunStreamSource
	nc          *nats.Conn
	runPrefix   string
	snapshotSub *nats.Subscription
	publishRun  func(context.Context, string, RunStreamEvent) error
	// reconnectDone 在总线重连监测协程退出时关闭。
	reconnectDone chan struct{}
}

// MemberPublisher 发布实时事件并强制撤销失权主体连接。
type MemberPublisher interface {
	Publish(context.Context, Notification) error
}

// SetMembers 注入 Jetcast 实时传输。
func (p *Publisher) SetMembers(members MemberPublisher) {
	p.membersMu.Lock()
	defer p.membersMu.Unlock()
	p.members = members
}

// Payload 是受众通知的广播消息体；零值字段省略，输入状态停止时同样省略 active，接收方按零值处理。
type Payload struct {
	Kind             Kind                       `json:"kind"`
	ConversationID   string                     `json:"conversationId,omitempty"`
	ConversationType domain.ConversationType    `json:"conversationType,omitempty"`
	Version          int64                      `json:"version,string,omitempty"`
	Changes          domain.ConversationChanges `json:"changes,omitempty"`
	TokenSessionID   string                     `json:"tokenSessionId,omitempty"`
	SenderSubjectID  string                     `json:"senderSubjectId,omitempty"`
	Active           bool                       `json:"active,omitempty"`
	AgentID          string                     `json:"agentId,omitempty"`
	ChannelID        string                     `json:"channelId,omitempty"`
	NotificationID   string                     `json:"notificationId,omitempty"`
	Title            string                     `json:"title,omitempty"`
	Body             string                     `json:"body,omitempty"`
	View             domain.NotificationView    `json:"view,omitempty"`
}

// NewPublisher 创建经指定消息总线发布通知的发布器。
func NewPublisher(bus *clusterbus.Bus) *Publisher {
	return &Publisher{bus: bus, pending: map[mergeKey]queuedNotification{}, sources: map[string]*RunStreamSource{}}
}

// Bus 返回发布器使用的消息总线，供内部运行信号订阅。
func (p *Publisher) Bus() *clusterbus.Bus {
	return p.bus
}

// Topic 生成受众通知的广播主题。
func Topic(workspaceID string, audienceKind AudienceKind, audienceID string) string {
	return "realtime." + workspaceID + "." + string(audienceKind) + "." + audienceID
}

// WatchAgentRunEnded 订阅指定运行的结束通知并等待订阅生效，收到后调用 handle；返回取消订阅函数。发布器未启动时不订阅。
func WatchAgentRunEnded(workspaceID, runID string, handle func()) func() {
	publisher := active.Load()
	if publisher == nil {
		return func() {}
	}
	unsubscribe, err := publisher.bus.Subscribe(Topic(workspaceID, AudienceAgentRun, runID), func([]byte) { handle() })
	if err != nil {
		slog.WarnContext(context.Background(), "订阅运行结束通知失败", "agent_run_id", runID, "error", err)
		return func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), subscribeSyncTimeout)
	defer cancel()
	if err := publisher.bus.Sync(ctx); err != nil {
		slog.WarnContext(ctx, "运行结束通知订阅确认失败", "agent_run_id", runID, "error", err)
	}
	return unsubscribe
}

// Start 开始接收已提交通知并在总线恢复连接时补发唤醒信号；消息总线由调用方启动与关闭。
func (p *Publisher) Start() error {
	if p.nc != nil {
		var err error
		// 快照请求逐个在独立协程中回复，大快照不阻塞其他运行的请求。
		p.snapshotSub, err = p.nc.Subscribe(p.runPrefix+".snapshot."+p.bus.ID(), func(msg *nats.Msg) { go p.serveRunSnapshot(msg) })
		if err != nil {
			return err
		}
		if err = p.nc.FlushTimeout(3 * time.Second); err != nil {
			_ = p.snapshotSub.Unsubscribe()
			return err
		}
	}
	p.begin(p.bus.Broadcast)
	p.reconnectDone = make(chan struct{})
	go p.watchReconnect()
	slog.InfoContext(context.Background(), "实时通知发布器已启动", "instance_id", p.bus.ID())
	return nil
}

// begin 使用指定发送函数启动发布协程，并开始接收已提交通知。
func (p *Publisher) begin(send func(topic string, data []byte) error) {
	p.send = send
	p.ready = make(chan struct{}, 1)
	p.pendingMu.Lock()
	p.pending = map[mergeKey]queuedNotification{}
	p.pendingMu.Unlock()
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.run()
	active.Store(p)
	droppedWarned.Store(false)
}

// Stop 停止接收通知、等待发布协程退出并结束本实例的运行流，尚未发布的通知直接丢弃；执行中的运行流随执行尝试结束。
func (p *Publisher) Stop() error {
	if p.stop == nil {
		return nil
	}
	active.CompareAndSwap(p, nil)
	close(p.stop)
	<-p.done
	if p.reconnectDone != nil {
		<-p.reconnectDone
		p.reconnectDone = nil
	}
	p.stop = nil
	if p.snapshotSub != nil {
		_ = p.snapshotSub.Unsubscribe()
	}
	p.sourcesMu.Lock()
	sources := slices.Collect(maps.Values(p.sources))
	p.sourcesMu.Unlock()
	for _, source := range sources {
		source.End()
	}
	slog.InfoContext(context.Background(), "实时通知发布器已停止")
	return nil
}

// Publish 不经写事务直接把通知放入发布队列，发布器未启动时丢弃。
func Publish(notifications ...Notification) {
	if len(notifications) == 0 {
		return
	}
	publisher := active.Load()
	if publisher == nil {
		warnDropped(len(notifications))
		return
	}
	publisher.enqueue(notifications)
}

// deliver 把已提交事务的通知放入发布队列，并把其中的唤醒信号直接送达本进程订阅；发布器未启动时丢弃客户端通知。
func deliver(notifications []Notification) {
	if len(notifications) == 0 {
		return
	}
	if publisher := active.Load(); publisher != nil {
		publisher.enqueue(notifications)
	} else {
		warnDropped(len(notifications))
	}
	// 唤醒信号同时直接送达本进程订阅，覆盖订阅时发布器尚未启动的情况；信号按主题合并，重复送达无副作用。
	signalLocalWakes(notifications)
}

// warnDropped 在发布器为空期间首次丢弃通知时记录 WARN。
func warnDropped(count int) {
	if droppedWarned.CompareAndSwap(false, true) {
		slog.WarnContext(context.Background(), "实时通知发布器未启动，丢弃通知", "count", count)
	}
}

// queuedNotification 是等待发布的一条通知及其最近一次入队的序号。
type queuedNotification struct {
	notification Notification
	sequence     uint64
}

// enqueue 先以有限并发执行已提交的授权撤销并等待全部完成，再在锁内把业务通知并入待发布集合；相同合并键保留最高版本并合并变化类别，版本相同时保留后到的通知并移到队尾；
// 不同合并键的通知达到 pendingLimit 时丢弃新增的客户端通知并记录 WARN 与累计条数，服务端实例之间的运行结束、新增输入与工具调用结果信号照常入队，达到 pendingHardLimit 时同样丢弃并记录 ERROR 与累计条数。
func (p *Publisher) enqueue(notifications []Notification) {
	// 授权变化在提交后以有限并发直接处理，全部完成后再登记业务通知；队列容量只约束可由快照补偿的业务通知。
	queued := make([]Notification, 0, len(notifications))
	var revocations sync.WaitGroup
	slots := make(chan struct{}, revocationConcurrency)
	for _, notification := range notifications {
		if IsAuthorizationChange(notification.Kind) || notification.Kind == KindConversationRemoved {
			slots <- struct{}{}
			revocations.Go(func() {
				defer func() { <-slots }()
				p.publish(notification)
			})
		} else {
			queued = append(queued, notification)
		}
	}
	revocations.Wait()
	notifications = queued
	var dropped, droppedSignals int64
	p.pendingMu.Lock()
	for _, notification := range notifications {
		key := notificationKey(notification)
		current, exists := p.pending[key]
		// 客户端通知达到 pendingLimit、全部通知达到 pendingHardLimit 时不再新增位置。
		serverSignal := notification.AudienceKind == AudienceAgentRun || notification.AudienceKind == AudienceAgentLane || notification.AudienceKind == AudienceAgentToolCall
		if !exists && len(p.pending) >= pendingLimit && !serverSignal {
			dropped++
			continue
		}
		if !exists && len(p.pending) >= pendingHardLimit {
			droppedSignals++
			continue
		}
		if exists {
			changes := current.notification.Changes | notification.Changes
			if notification.Version < current.notification.Version {
				notification = current.notification
			}
			notification.Changes = changes
		}
		p.sequence++
		p.pending[key] = queuedNotification{notification: notification, sequence: p.sequence}
	}
	p.pendingMu.Unlock()
	select {
	case p.ready <- struct{}{}:
	default:
	}
	if dropped > 0 {
		slog.WarnContext(context.Background(), "实时通知发布队列已满，丢弃通知", "count", dropped, "dropped_total", p.dropped.Add(dropped))
	}
	if droppedSignals > 0 {
		slog.ErrorContext(context.Background(), "实时通知发布队列达到总量上限，丢弃服务端信号", "count", droppedSignals, "dropped_total", p.droppedSignals.Add(droppedSignals))
	}
}

// takePending 取出全部等待发布的通知，按最近一次入队的顺序返回。
func (p *Publisher) takePending() []Notification {
	p.pendingMu.Lock()
	queued := slices.SortedFunc(maps.Values(p.pending), func(a, b queuedNotification) int { return cmp.Compare(a.sequence, b.sequence) })
	clear(p.pending)
	p.pendingMu.Unlock()
	return arr.Map(queued, func(item queuedNotification) Notification { return item.notification })
}

// run 按最近一次入队的顺序逐条发布等待中的通知，直到发布器停止；并发事务之间的通知可能乱序。
func (p *Publisher) run() {
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		case <-p.ready:
			for _, notification := range p.takePending() {
				p.publish(notification)
			}
		}
	}
}

// publish 发布单条通知，授权撤销失败记录错误，其余发布失败记录警告。
func (p *Publisher) publish(notification Notification) {
	if notification.AudienceKind != AudienceAgentRun && notification.AudienceKind != AudienceAgentLane && notification.AudienceKind != AudienceAgentToolCall {
		p.membersMu.RLock()
		members := p.members
		p.membersMu.RUnlock()
		if members != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := members.Publish(ctx, notification)
			cancel()
			if err != nil {
				logCtx := logscope.WithWorkspace(context.Background(), notification.WorkspaceID)
				if IsAuthorizationChange(notification.Kind) || notification.Kind == KindConversationRemoved {
					slog.ErrorContext(logCtx, "撤销实时授权失败", "kind", notification.Kind, "audience_kind", notification.AudienceKind, "audience_id", notification.AudienceID, "error", err)
				} else {
					slog.WarnContext(logCtx, "发布实时通知失败", "kind", notification.Kind, "audience_kind", notification.AudienceKind, "audience_id", notification.AudienceID, "error", err)
				}
			}
		}
		return
	}
	data, err := json.Marshal(Payload{
		Kind: notification.Kind, ConversationID: notification.ConversationID, ConversationType: notification.ConversationType, Version: notification.Version, Changes: notification.Changes,
		TokenSessionID: notification.TokenSessionID, SenderSubjectID: notification.SenderSubjectID, Active: notification.Active,
		AgentID: notification.AgentID, ChannelID: notification.ChannelID, NotificationID: notification.NotificationID, Title: notification.Title, Body: notification.Body, View: notification.View,
	})
	if err == nil {
		err = p.send(Topic(notification.WorkspaceID, notification.AudienceKind, notification.AudienceID), data)
	}
	if err != nil {
		slog.WarnContext(logscope.WithWorkspace(context.Background(), notification.WorkspaceID), "实时通知发布失败",
			"audience_kind", notification.AudienceKind,
			"audience_id", notification.AudienceID,
			"kind", notification.Kind,
			"conversation_id", notification.ConversationID,
			"version", notification.Version,
			"token_session_id", notification.TokenSessionID,
			"error", err,
		)
	}
}
