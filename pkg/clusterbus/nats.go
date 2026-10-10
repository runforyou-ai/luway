package clusterbus

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// natsDefaultMaxPayload 是尚未取得服务器参数时使用的单条消息上限。
	natsDefaultMaxPayload = 1 << 20
	// natsSyncTimeout 是调用方未设置截止时间时等待订阅确认的时限。
	natsSyncTimeout = 5 * time.Second
)

// NATSOptions 是 NATS 传输层的连接参数。
type NATSOptions struct {
	// URL 是 NATS 服务器地址。
	URL string
	// Namespace 是全部主题的前缀，同一部署的实例使用相同命名空间。
	Namespace string
}

// NewNATS 创建经 Core NATS 传输消息的总线。
func NewNATS(instanceID string, options NATSOptions) *Bus {
	return newBus(instanceID, DriverNATS, &natsTransport{options: options, instanceID: instanceID, topics: map[string]*nats.Subscription{}})
}

// natsTransport 以命名空间下的主题广播消息，以实例主题定向发送消息。
type natsTransport struct {
	options    NATSOptions
	instanceID string
	receive    func([]byte)

	mu         sync.Mutex
	connection *nats.Conn
	topics     map[string]*nats.Subscription
}

// start 建立 NATS 连接并订阅本实例主题；NATS 暂不可用时后台重连，不阻塞启动。
func (t *natsTransport) start(receive func([]byte), _ func(int)) error {
	t.receive = receive
	connection, err := nats.Connect(
		t.options.URL,
		nats.Name("server-bus-"+t.options.Namespace),
		nats.NoEcho(),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		// 断连后发送立即返回错误。
		nats.ReconnectBufSize(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.WarnContext(context.Background(), "总线 NATS 连接断开", "namespace", t.options.Namespace, "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.InfoContext(context.Background(), "总线 NATS 已重新连接", "namespace", t.options.Namespace)
		}),
	)
	if err != nil {
		return fmt.Errorf("connect bus NATS: %w", err)
	}
	if _, err := connection.Subscribe(t.directSubject(t.instanceID), t.handle); err != nil {
		connection.Close()
		return fmt.Errorf("subscribe bus instance subject: %w", err)
	}
	// 已连上时等待本实例主题订阅生效；尚未连上时订阅在连上后登记。
	if connection.IsConnected() {
		if err := connection.FlushTimeout(natsSyncTimeout); err != nil {
			slog.WarnContext(context.Background(), "总线 NATS 订阅确认失败", "namespace", t.options.Namespace, "error", err)
		}
	}
	t.mu.Lock()
	t.connection = connection
	t.mu.Unlock()
	return nil
}

// topicSubject 返回广播主题的 NATS Subject。
func (t *natsTransport) topicSubject(topic string) string {
	return t.options.Namespace + "." + topic
}

// directSubject 返回实例定向消息的 NATS Subject。
func (t *natsTransport) directSubject(instanceID string) string {
	return t.options.Namespace + ".instance." + instanceID
}

// broadcast 把消息发布到广播主题。
func (t *natsTransport) broadcast(topic string, frames [][]byte) error {
	return t.publish(t.topicSubject(topic), frames)
}

// direct 把消息发布到目标实例主题。
func (t *natsTransport) direct(instanceID string, frames [][]byte) error {
	return t.publish(t.directSubject(instanceID), frames)
}

// publish 依次发布一条消息的全部分片。
func (t *natsTransport) publish(subject string, frames [][]byte) error {
	t.mu.Lock()
	connection := t.connection
	t.mu.Unlock()
	if connection == nil {
		return ErrClosed
	}
	for _, frame := range frames {
		if err := connection.Publish(subject, frame); err != nil {
			return err
		}
	}
	return nil
}

// handle 把收到的分片交给总线。
func (t *natsTransport) handle(message *nats.Msg) {
	t.receive(message.Data)
}

// frameBytes 返回 NATS 服务器允许的单条消息上限，尚未连上时使用默认上限。
func (t *natsTransport) frameBytes() int {
	t.mu.Lock()
	connection := t.connection
	t.mu.Unlock()
	if connection != nil {
		if limit := int(connection.MaxPayload()); limit > 0 {
			return limit
		}
	}
	return natsDefaultMaxPayload
}

// subscribe 订阅广播主题。
func (t *natsTransport) subscribe(topic string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connection == nil || t.topics[topic] != nil {
		return nil
	}
	subscription, err := t.connection.Subscribe(t.topicSubject(topic), t.handle)
	if err != nil {
		return fmt.Errorf("subscribe bus topic: %w", err)
	}
	t.topics[topic] = subscription
	return nil
}

// unsubscribe 取消广播主题的订阅。
func (t *natsTransport) unsubscribe(topic string) {
	t.mu.Lock()
	subscription := t.topics[topic]
	delete(t.topics, topic)
	t.mu.Unlock()
	if subscription != nil {
		if err := subscription.Unsubscribe(); err != nil {
			slog.WarnContext(context.Background(), "取消总线主题订阅失败", "topic", topic, "error", err)
		}
	}
}

// sync 等待 NATS 服务器确认之前的订阅。
func (t *natsTransport) sync(ctx context.Context) error {
	t.mu.Lock()
	connection := t.connection
	t.mu.Unlock()
	if connection == nil {
		return ErrClosed
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, natsSyncTimeout)
		defer cancel()
	}
	return connection.FlushWithContext(ctx)
}

// connected 返回 NATS 连接当前是否可用。
func (t *natsTransport) connected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connection != nil && t.connection.IsConnected()
}

// close 关闭 NATS 连接。
func (t *natsTransport) close() error {
	t.mu.Lock()
	connection := t.connection
	t.connection = nil
	t.topics = map[string]*nats.Subscription{}
	t.mu.Unlock()
	if connection != nil {
		connection.Close()
	}
	return nil
}
