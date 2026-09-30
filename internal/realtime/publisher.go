//go:build server

package realtime

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/nats-io/nats.go"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/domain"
)

// publishQueueSize 是等待发布的已提交事务批次上限。
const publishQueueSize = 1024

// active 是当前进程接收已提交通知的发布器；未启动时通知直接丢弃，由客户端兜底探针恢复。
var active atomic.Pointer[Publisher]

// Publisher 通过 Core NATS 异步发布受众通知，发布失败只记录日志。
type Publisher struct {
	config     serverconfig.NATSConfig
	connection *nats.Conn
	send       func(subject string, data []byte) error
	queue      chan []Notification
	stop       chan struct{}
	done       chan struct{}
}

// Payload 是 NATS 通知消息体；零值字段省略，输入状态停止时同样省略 active，接收方按零值处理。
type Payload struct {
	Kind             Kind                          `json:"kind"`
	ConversationID   string                        `json:"conversationId,omitempty"`
	ConversationType domain.ConversationType       `json:"conversationType,omitempty"`
	Version          int64                         `json:"version,string,omitempty"`
	Changes          domain.ConversationChanges    `json:"changes,omitempty"`
	TokenSessionID   string                        `json:"tokenSessionId,omitempty"`
	SenderSubjectID  string                        `json:"senderSubjectId,omitempty"`
	Active           bool                          `json:"active,omitempty"`
	ServiceSessionID string                        `json:"serviceSessionId,omitempty"`
	AttentionReason  domain.ServiceAttentionReason `json:"attentionReason,omitempty"`
	DeviceID         string                        `json:"deviceId,omitempty"`
	AssistantID      string                        `json:"assistantId,omitempty"`
}

// NewPublisher 创建使用指定 NATS 命名空间的通知发布器。
func NewPublisher(config serverconfig.NATSConfig) *Publisher {
	return &Publisher{config: config}
}

// Connection 返回发布器持有的 NATS 连接，供实时网关订阅受众通知；Start 之前为 nil。
func (p *Publisher) Connection() *nats.Conn {
	return p.connection
}

// Subject 生成受众通知的 NATS Subject。
func Subject(namespace, organizationID string, audienceKind AudienceKind, audienceID string) string {
	return namespace + ".realtime." + organizationID + "." + string(audienceKind) + "." + audienceID
}

// Start 建立 NATS 连接并开始接收已提交通知；NATS 暂不可用时后台重连，不阻塞启动。
func (p *Publisher) Start() error {
	connection, err := nats.Connect(
		p.config.URL,
		nats.Name("server-realtime-"+p.config.Namespace),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		// 断连后发布立即返回错误，丢失的通知由兜底探针恢复。
		nats.ReconnectBufSize(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				slog.Warn("实时通知 NATS 连接断开", "namespace", p.config.Namespace, "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("实时通知 NATS 已重新连接", "namespace", p.config.Namespace)
		}),
	)
	if err != nil {
		return fmt.Errorf("connect realtime NATS: %w", err)
	}
	p.connection = connection
	p.begin(connection.Publish)
	slog.Info("实时通知发布器已启动", "namespace", p.config.Namespace)
	return nil
}

// begin 使用指定发送函数启动发布协程，并开始接收已提交通知。
func (p *Publisher) begin(send func(subject string, data []byte) error) {
	p.send = send
	p.queue = make(chan []Notification, publishQueueSize)
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.run()
	active.Store(p)
}

// Stop 停止接收通知、等待发布协程退出并关闭 NATS 连接，尚未发布的通知直接丢弃。
func (p *Publisher) Stop() error {
	if p.stop == nil {
		return nil
	}
	active.CompareAndSwap(p, nil)
	close(p.stop)
	<-p.done
	p.stop = nil
	if p.connection != nil {
		p.connection.Close()
		p.connection = nil
	}
	slog.Info("实时通知发布器已停止", "namespace", p.config.Namespace)
	return nil
}

// Publish 不经写事务直接把通知放入发布队列，发布器未启动时丢弃。
func Publish(notifications ...Notification) {
	if publisher := active.Load(); publisher != nil && len(notifications) > 0 {
		publisher.enqueue(notifications)
	}
}

// enqueue 把一个已提交事务的通知放入发布队列，队列已满时丢弃。
func (p *Publisher) enqueue(notifications []Notification) {
	select {
	case p.queue <- notifications:
	default:
		slog.Warn("实时通知发布队列已满，丢弃通知", "namespace", p.config.Namespace, "count", len(notifications))
	}
}

// run 按已提交批次的入队顺序逐条发布通知，直到发布器停止；并发事务之间的通知可能乱序。
func (p *Publisher) run() {
	defer close(p.done)
	for {
		select {
		case <-p.stop:
			return
		case notifications := <-p.queue:
			for _, notification := range notifications {
				p.publish(notification)
			}
		}
	}
}

// publish 发布单条通知，失败时记录 WARN 日志。
func (p *Publisher) publish(notification Notification) {
	data, err := json.Marshal(Payload{
		Kind: notification.Kind, ConversationID: notification.ConversationID, ConversationType: notification.ConversationType, Version: notification.Version, Changes: notification.Changes,
		TokenSessionID: notification.TokenSessionID, SenderSubjectID: notification.SenderSubjectID, Active: notification.Active,
		ServiceSessionID: notification.ServiceSessionID, AttentionReason: notification.AttentionReason,
		DeviceID: notification.DeviceID, AssistantID: notification.AssistantID,
	})
	if err == nil {
		err = p.send(Subject(p.config.Namespace, notification.OrganizationID, notification.AudienceKind, notification.AudienceID), data)
	}
	if err != nil {
		slog.Warn("实时通知发布失败",
			"namespace", p.config.Namespace,
			"organization_id", notification.OrganizationID,
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
