// Package clusterbus 在同一部署的多个进程之间传递消息：按主题广播给全部进程，或把单向消息发给指定进程。
//
// 每个进程以实例编号加入总线。发往本进程的消息与本进程的广播订阅在进程内投递，其余消息经传输层送达；收到的消息按到达顺序在同一个分发协程中依次交给处理函数，处理函数不得阻塞；超过传输层单片上限的消息拆成分片按序发送，接收方收齐后再投递。传输层尽力送达，断连期间的消息直接丢弃。
package clusterbus

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runforyou-ai/support/random"
)

const (
	// dispatchQueueSize 是等待分发的消息上限，队列已满时丢弃新消息。
	dispatchQueueSize = 4096
	// compressBytes 是消息按 gzip 压缩的最小编码长度。
	compressBytes = 32 << 10
	// partialTimeout 是分片消息未收齐时保留已收分片的时限。
	partialTimeout = time.Minute
	// partMarker 是分片的首字节，完整消息以 JSON 或 gzip 开头。
	partMarker = 0x01
	// partHeaderBytes 是分片头的长度：标记、16 字节消息编号、序号与总数。
	partHeaderBytes = 1 + 16 + 4 + 4
)

// 传输层驱动名称。
const (
	// DriverPostgres 表示经 PostgreSQL LISTEN/NOTIFY 传输。
	DriverPostgres = "postgres"
	// DriverNATS 表示经 Core NATS 传输。
	DriverNATS = "nats"
)

// ErrClosed 表示总线已关闭。
var ErrClosed = errors.New("clusterbus: closed")

// MessageHandler 处理发给本实例的单向消息，from 是发送方实例编号。
type MessageHandler func(from string, data []byte)

// transport 在实例之间传送编码后的分片。
type transport interface {
	// start 建立连接并开始把收到的分片交给 receive；已接受的消息之后异步发送失败时以失败条数调用 failed。
	start(receive func(frame []byte), failed func(count int)) error
	// frameBytes 返回单个分片的长度上限。
	frameBytes() int
	// broadcast 把一条消息的全部分片按序发给订阅该主题的全部实例。
	broadcast(topic string, frames [][]byte) error
	// direct 把一条消息的全部分片按序发给指定实例。
	direct(instanceID string, frames [][]byte) error
	// subscribe 在本实例出现该主题的首个订阅时登记接收。
	subscribe(topic string) error
	// unsubscribe 在本实例不再有该主题的订阅时取消接收。
	unsubscribe(topic string)
	// sync 等待之前登记的接收生效。
	sync(ctx context.Context) error
	// connected 返回传输层当前是否可用。
	connected() bool
	// close 断开连接并停止接收。
	close() error
}

// envelope 是传输层上的一条总线消息：Topic 非空为广播，否则为发给目标实例的单向消息。
type envelope struct {
	From  string `json:"f"`
	Topic string `json:"o,omitempty"`
	Kind  string `json:"k,omitempty"`
	Data  []byte `json:"d,omitempty"`
}

// subscription 是本实例对一个广播主题的一次订阅。
type subscription struct {
	handle func(data []byte)
}

// partial 是正在拼接的分片消息。
type partial struct {
	parts    [][]byte
	received int
	created  time.Time
}

// Stats 是总线自启动以来的累计计数：Sent 为传输层接受发送的消息数，其中之后异步发送失败的消息同时计入 Failed；Failed 为发送失败与因分发队列已满丢弃的消息数。
type Stats struct {
	Sent   uint64
	Failed uint64
}

// Bus 是本实例加入的消息总线。
type Bus struct {
	id        string
	driver    string
	transport transport
	sent      atomic.Uint64
	failed    atomic.Uint64

	mu       sync.Mutex
	topics   map[string]map[*subscription]struct{}
	handlers map[string]MessageHandler
	partials map[[16]byte]*partial
	started  bool
	closed   bool

	dispatch chan func()
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// newBus 创建使用指定驱动传输层的总线。
func newBus(instanceID, driver string, transport transport) *Bus {
	ctx, cancel := context.WithCancel(context.Background())
	return &Bus{
		id: instanceID, driver: driver, transport: transport,
		topics: map[string]map[*subscription]struct{}{}, handlers: map[string]MessageHandler{}, partials: map[[16]byte]*partial{},
		dispatch: make(chan func(), dispatchQueueSize), ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
}

// ID 返回本实例编号。
func (b *Bus) ID() string {
	return b.id
}

// Driver 返回传输层驱动名称。
func (b *Bus) Driver() string {
	return b.driver
}

// Start 启动分发协程并建立传输层连接。
func (b *Bus) Start() error {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return nil
	}
	b.started = true
	b.mu.Unlock()
	go b.runDispatch()
	if err := b.transport.start(b.receive, func(count int) { b.failed.Add(uint64(count)) }); err != nil {
		return err
	}
	b.mu.Lock()
	topics := slices.Collect(maps.Keys(b.topics))
	b.mu.Unlock()
	// 启动前登记的订阅在传输层就绪后补登记。
	for _, topic := range topics {
		if err := b.transport.subscribe(topic); err != nil {
			return err
		}
	}
	slog.InfoContext(context.Background(), "消息总线已启动", "instance_id", b.id, "driver", b.driver)
	return nil
}

// Close 断开传输层并停止分发协程。
func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	started := b.started
	b.mu.Unlock()
	b.cancel()
	err := b.transport.close()
	if started {
		<-b.done
		slog.InfoContext(context.Background(), "消息总线已关闭", "instance_id", b.id, "driver", b.driver)
	}
	return err
}

// Stats 返回总线自启动以来的累计计数。
func (b *Bus) Stats() Stats {
	return Stats{Sent: b.sent.Load(), Failed: b.failed.Load()}
}

// Connected 返回传输层当前是否可用。
func (b *Bus) Connected() bool {
	return b.transport.connected()
}

// Subscribe 登记本实例对广播主题的订阅并返回取消函数；订阅经 Sync 确认后才保证收到之后的广播。
func (b *Bus) Subscribe(topic string, handle func(data []byte)) (func(), error) {
	current := &subscription{handle: handle}
	b.mu.Lock()
	subscriptions := b.topics[topic]
	first := subscriptions == nil
	if first {
		subscriptions = map[*subscription]struct{}{}
		b.topics[topic] = subscriptions
	}
	subscriptions[current] = struct{}{}
	started := b.started
	b.mu.Unlock()
	if first && started {
		if err := b.transport.subscribe(topic); err != nil {
			b.unsubscribe(topic, current)
			return nil, err
		}
	}
	var once sync.Once
	return func() { once.Do(func() { b.unsubscribe(topic, current) }) }, nil
}

// unsubscribe 移除一次订阅，主题不再有订阅时取消传输层接收。
func (b *Bus) unsubscribe(topic string, current *subscription) {
	b.mu.Lock()
	subscriptions := b.topics[topic]
	delete(subscriptions, current)
	last := subscriptions != nil && len(subscriptions) == 0
	if last {
		delete(b.topics, topic)
	}
	b.mu.Unlock()
	if last {
		b.transport.unsubscribe(topic)
	}
}

// Sync 等待之前登记的订阅在传输层生效。
func (b *Bus) Sync(ctx context.Context) error {
	return b.transport.sync(ctx)
}

// HandleMessage 登记本实例处理指定种类单向消息的函数。
func (b *Bus) HandleMessage(kind string, handle MessageHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[kind] = handle
}

// Broadcast 把消息交给本实例的订阅并发给其他实例。
func (b *Bus) Broadcast(topic string, data []byte) error {
	b.deliverBroadcast(topic, data)
	frames, err := b.encode(envelope{From: b.id, Topic: topic, Data: data})
	if err != nil {
		return err
	}
	return b.count(b.transport.broadcast(topic, frames))
}

// Send 把单向消息发给指定实例，发给本实例时在进程内投递。
func (b *Bus) Send(instanceID, kind string, data []byte) error {
	if instanceID == b.id {
		b.deliverMessage(b.id, kind, data)
		return nil
	}
	frames, err := b.encode(envelope{From: b.id, Kind: kind, Data: data})
	if err != nil {
		return err
	}
	return b.count(b.transport.direct(instanceID, frames))
}

// count 按传输层是否接受一条消息计入发送数或失败数，并原样返回错误。
func (b *Bus) count(err error) error {
	if err != nil {
		b.failed.Add(1)
		return err
	}
	b.sent.Add(1)
	return nil
}

// encode 把消息编码为 JSON，超过压缩阈值时 gzip 压缩，超过传输层单片上限时拆成带分片头的分片。
func (b *Bus) encode(message envelope) ([][]byte, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode bus message: %w", err)
	}
	if len(payload) >= compressBytes {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(payload); err != nil {
			return nil, fmt.Errorf("compress bus message: %w", err)
		}
		if err := writer.Close(); err != nil {
			return nil, fmt.Errorf("compress bus message: %w", err)
		}
		payload = compressed.Bytes()
	}
	limit := b.transport.frameBytes()
	if len(payload) <= limit {
		return [][]byte{payload}, nil
	}
	id := random.Bytes(16)
	size := limit - partHeaderBytes
	parts := (len(payload) + size - 1) / size
	frames := make([][]byte, parts)
	for part := range parts {
		chunk := payload[part*size : min(len(payload), (part+1)*size)]
		frame := make([]byte, partHeaderBytes, partHeaderBytes+len(chunk))
		frame[0] = partMarker
		copy(frame[1:17], id)
		binary.BigEndian.PutUint32(frame[17:21], uint32(part))
		binary.BigEndian.PutUint32(frame[21:25], uint32(parts))
		frames[part] = append(frame, chunk...)
	}
	return frames, nil
}

// receive 处理传输层收到的分片：完整消息直接解析，分片收齐后拼接再解析；其他实例的广播与单向消息进入分发队列。
func (b *Bus) receive(frame []byte) {
	payload := frame
	if len(frame) > 0 && frame[0] == partMarker {
		payload = b.assemble(frame)
		if payload == nil {
			return
		}
	}
	if len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err == nil {
			payload, err = io.ReadAll(reader)
		}
		if err != nil {
			slog.WarnContext(context.Background(), "总线消息无法解压", "error", err)
			return
		}
	}
	var message envelope
	if err := json.Unmarshal(payload, &message); err != nil {
		slog.WarnContext(context.Background(), "总线消息无法解析", "error", err)
		return
	}
	if message.From == b.id {
		return
	}
	if message.Topic != "" {
		b.deliverBroadcast(message.Topic, message.Data)
		return
	}
	b.deliverMessage(message.From, message.Kind, message.Data)
}

// assemble 登记一个分片，消息的分片收齐时返回拼接后的内容，否则返回 nil；超过保留时限仍未收齐的分片被丢弃。
func (b *Bus) assemble(frame []byte) []byte {
	if len(frame) < partHeaderBytes {
		slog.WarnContext(context.Background(), "总线分片无法解析", "bytes", len(frame))
		return nil
	}
	var id [16]byte
	copy(id[:], frame[1:17])
	part, parts := binary.BigEndian.Uint32(frame[17:21]), binary.BigEndian.Uint32(frame[21:25])
	if parts == 0 || part >= parts {
		slog.WarnContext(context.Background(), "总线分片无法解析", "part", part, "parts", parts)
		return nil
	}
	now := time.Now() //clock:local
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, pending := range b.partials {
		if now.Sub(pending.created) > partialTimeout {
			delete(b.partials, key)
		}
	}
	pending := b.partials[id]
	if pending == nil {
		pending = &partial{parts: make([][]byte, parts), created: now}
		b.partials[id] = pending
	}
	if int(parts) != len(pending.parts) || pending.parts[part] != nil {
		return nil
	}
	pending.parts[part] = frame[partHeaderBytes:]
	pending.received++
	if pending.received < len(pending.parts) {
		return nil
	}
	delete(b.partials, id)
	return bytes.Join(pending.parts, nil)
}

// deliverBroadcast 把广播放入分发队列，交给本实例该主题的全部订阅。
func (b *Bus) deliverBroadcast(topic string, data []byte) {
	b.mu.Lock()
	subscriptions := b.topics[topic]
	handlers := make([]func([]byte), 0, len(subscriptions))
	for current := range subscriptions {
		handlers = append(handlers, current.handle)
	}
	b.mu.Unlock()
	if len(handlers) == 0 {
		return
	}
	b.enqueue(func() {
		for _, handle := range handlers {
			handle(data)
		}
	})
}

// deliverMessage 把单向消息放入分发队列，交给本实例该种类的处理函数。
func (b *Bus) deliverMessage(from, kind string, data []byte) {
	b.mu.Lock()
	handle := b.handlers[kind]
	b.mu.Unlock()
	if handle == nil {
		return
	}
	b.enqueue(func() { handle(from, data) })
}

// enqueue 把一次分发放入队列，队列已满或总线已关闭时丢弃。
func (b *Bus) enqueue(deliver func()) {
	select {
	case <-b.ctx.Done():
	case b.dispatch <- deliver:
	default:
		b.failed.Add(1)
		slog.WarnContext(context.Background(), "总线分发队列已满，丢弃消息", "instance_id", b.id)
	}
}

// runDispatch 按入队顺序依次执行分发，直到总线关闭。
func (b *Bus) runDispatch() {
	defer close(b.done)
	for {
		select {
		case <-b.ctx.Done():
			return
		case deliver := <-b.dispatch:
			deliver()
		}
	}
}
