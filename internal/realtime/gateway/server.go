//go:build server

// Package gateway 通过应用 WebSocket 管理实时身份、授权、历史和连接。
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/runforyou-ai/luway/internal/messaging"
	"github.com/runforyou-ai/luway/internal/realtime/wire"
)

// User 是经过业务身份复核的连接主体。
type User struct {
	ID       string
	Session  string
	Info     any
	Channels []string
}

// Event 是向一个或多个业务频道发布的事件。
type Event struct {
	ID       string
	Name     string
	Channels []string
	Data     json.RawMessage
}

// Target 定义连接撤销的主体及可选会话范围。
type Target struct {
	User    string `json:"user"`
	Session string `json:"session,omitempty"`
	ID      string `json:"id,omitempty"`
}

// Server 持有共享 NATS 连接及本地 WebSocket 会话。
type Server struct {
	connection   *messaging.Connection
	Authenticate func(context.Context, string) (User, error)
	Authorize    func(context.Context, User, string) (bool, error)
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	sockets      map[*socket]struct{}
	generation   uint64
	revocations  []revocation
	control      *nats.Subscription
	wait         sync.WaitGroup
}

// revocation 保存有界的认证窗口撤销记录。
type revocation struct {
	generation uint64
	target     Target
}

// New 创建与应用身份体系连接的实时网关。
func New(c *messaging.Connection) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{connection: c, ctx: ctx, cancel: cancel, sockets: map[*socket]struct{}{}}
}

// Start 订阅全部网关独立接收的撤销控制消息。
func (s *Server) Start(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var err error
	s.control, err = s.connection.Conn.Subscribe(messaging.RealtimeEvents+messaging.RealtimeRevoke, func(m *nats.Msg) {
		var target Target
		if json.Unmarshal(m.Data, &target) != nil {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.generation++
		if len(s.revocations) == 1024 {
			copy(s.revocations, s.revocations[1:])
			s.revocations = s.revocations[:1023]
		}
		s.revocations = append(s.revocations, revocation{generation: s.generation, target: target})
		for c := range s.sockets {
			if c.user.ID == target.User && (target.Session == "" || target.Session == c.user.Session) {
				c.cancel()
			}
		}
	})
	if err != nil {
		return err
	}
	return s.connection.Conn.FlushWithContext(ctx)
}

// Close 关闭网关及其所有连接并等待处理协程结束。
func (s *Server) Close() error {
	s.mu.Lock()
	s.cancel()
	for c := range s.sockets {
		c.cancel()
	}
	s.mu.Unlock()
	if s.control != nil {
		_ = s.control.Unsubscribe()
	}
	s.wait.Wait()
	return nil
}

// Disconnect 持久发布指定主体的连接撤销。
func (s *Server) Disconnect(ctx context.Context, target Target) error {
	if target.ID == "" {
		target.ID = uuid.NewV7().String()
	}
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	_, err = s.connection.JS.Publish(ctx, messaging.RealtimeInput+messaging.RealtimeRevoke, data, jetstream.WithMsgID("revoke/"+target.ID+"/"+target.User+"/"+target.Session))
	return err
}

// Broadcast 发布业务事件并等待持久事件的存储确认。
func (s *Server) Broadcast(ctx context.Context, event Event) error {
	if event.ID == "" {
		event.ID = uuid.NewV7().String()
	}
	for _, channel := range event.Channels {
		frame := wire.Frame{Op: "event", Channel: channel, ID: event.ID, Name: event.Name, Data: event.Data}
		data, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		if strings.HasSuffix(channel, ".typing") {
			err = s.connection.Conn.Publish(messaging.RealtimeLive+channel, data)
		} else {
			_, err = s.connection.JS.Publish(ctx, messaging.RealtimeInput+channel, data, jetstream.WithMsgID(event.ID+"/"+channel))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ServeHTTP 升级实时连接，并以首帧短期票据完成认证。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	s.wait.Add(1)
	s.mu.Unlock()
	defer s.wait.Done()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //clock:local
	var auth wire.Frame
	if conn.ReadJSON(&auth) != nil || auth.Op != "auth" || auth.Ticket == "" {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	authCtx, end := context.WithTimeout(ctx, 5*time.Second)
	user, err := s.Authenticate(authCtx, auth.Ticket)
	end()
	if err != nil {
		_ = conn.WriteJSON(wire.Frame{Op: "bye", Reason: "unauthorized"})
		return
	}
	c := &socket{server: s, conn: conn, ctx: ctx, cancel: cancel, user: user, ticket: auth.Ticket, subscriptions: map[string]*subscription{}, in: make(chan queuedEvent, 256), commands: make(chan wire.Frame, 16)}
	s.mu.Lock()
	revoked := len(s.revocations) > 0 && generation+1 < s.revocations[0].generation
	for _, entry := range s.revocations {
		if entry.generation > generation && entry.target.User == user.ID && (entry.target.Session == "" || entry.target.Session == user.Session) {
			revoked = true
			break
		}
	}
	if revoked || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.sockets[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.sockets, c)
		s.mu.Unlock()
		for _, sub := range c.subscriptions {
			_ = sub.nats.Unsubscribe()
		}
	}()
	_ = conn.SetReadDeadline(time.Time{})
	info, err := json.Marshal(user.Info)
	if err != nil {
		return
	}
	if c.write(wire.Frame{Op: "ready", Socket: uuid.NewV7().String(), Info: info}) != nil {
		return
	}
	go c.read()
	c.run()
}

// subscription 保存频道的底层订阅和已发送游标。
type subscription struct {
	nats     *nats.Subscription
	position uint64
	epoch    string
}

// queuedEvent 记录连接发送预算中的帧与字节数。
type queuedEvent struct {
	frame wire.Frame
	bytes int64
}

// socket 管理单个客户端的串行命令、事件与写入。
type socket struct {
	server        *Server
	conn          *websocket.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	user          User
	ticket        string
	subscriptions map[string]*subscription
	in            chan queuedEvent
	pendingBytes  atomic.Int64
	commands      chan wire.Frame
}

// read 顺序读取客户端命令并交给连接处理循环。
func (c *socket) read() {
	defer c.cancel()
	for {
		var f wire.Frame
		if c.conn.ReadJSON(&f) != nil {
			return
		}
		select {
		case c.commands <- f:
		case <-c.ctx.Done():
			return
		}
	}
}

// write 在限定时限内写出一帧。
func (c *socket) write(f wire.Frame) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //clock:local
	return c.conn.WriteJSON(f)
}

// run 串行处理订阅和事件，并定期复核身份及底层连接。
func (c *socket) run() {
	reconnects := c.server.connection.Conn.Stats().Reconnects
	health := time.NewTicker(time.Second)
	defer health.Stop()
	verify := time.NewTicker(10 * time.Second)
	defer verify.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-health.C:
			if !c.server.connection.Conn.IsConnected() || c.server.connection.Conn.Stats().Reconnects != reconnects {
				return
			}
		case <-verify.C:
			ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
			user, err := c.server.Authenticate(ctx, c.ticket)
			if err == nil && !reflect.DeepEqual(user, c.user) {
				err = errors.New("authorization changed")
			}
			if err == nil {
				_, err = c.server.connection.JS.AccountInfo(ctx)
			}
			if err == nil {
				for channel := range c.subscriptions {
					var allowed bool
					allowed, err = c.server.allowed(ctx, user, channel)
					if err != nil || !allowed {
						err = errors.New("authorization changed")
						break
					}
				}
			}
			cancel()
			if err != nil {
				return
			}
			for channel, sub := range c.subscriptions {
				if !strings.HasSuffix(channel, ".typing") {
					if c.recover(channel, sub, false) != nil {
						return
					}
				}
			}
		case f := <-c.commands:
			switch f.Op {
			case "sub":
				if err := c.subscribe(f); err != nil {
					return
				}
			case "unsub":
				if sub := c.subscriptions[f.Channel]; sub != nil {
					_ = sub.nats.Unsubscribe()
					delete(c.subscriptions, f.Channel)
				}
			default:
				return
			}
		case item := <-c.in:
			c.pendingBytes.Add(-item.bytes)
			f := item.frame
			sub := c.subscriptions[f.Channel]
			if sub == nil {
				continue
			}
			if f.Sequence != 0 {
				if f.Sequence <= sub.position {
					continue
				}
				if f.Previous > sub.position {
					if c.recover(f.Channel, sub, false) != nil {
						return
					}
					if f.Sequence <= sub.position {
						continue
					}
				}
				sub.position = f.Sequence
			}
			f.Epoch = sub.epoch
			if c.write(f) != nil {
				return
			}
		}
	}
}

// allowed 复核频道格式及当前业务授权。
func (s *Server) allowed(ctx context.Context, user User, channel string) (bool, error) {
	if channel == "" || len(channel) > 256 || strings.ContainsAny(channel, "*> /\\\t\r\n") {
		return false, nil
	}
	for _, name := range user.Channels {
		if name == channel {
			return true, nil
		}
	}
	if s.Authorize == nil {
		return false, nil
	}
	return s.Authorize(ctx, user, channel)
}

// subscribe 建立接收屏障，恢复历史，再确认频道就绪。
func (c *socket) subscribe(f wire.Frame) error {
	if _, exists := c.subscriptions[f.Channel]; exists {
		return nil
	}
	if len(c.subscriptions) >= 1000 {
		return errors.New("subscription limit")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	user, err := c.server.Authenticate(ctx, c.ticket)
	allowed := false
	if err == nil {
		allowed, err = c.server.allowed(ctx, user, f.Channel)
	}
	if err != nil {
		return err
	}
	if !allowed {
		return c.write(wire.Frame{Op: "denied", Channel: f.Channel})
	}
	subject := messaging.RealtimeEvents + f.Channel
	ephemeral := strings.HasSuffix(f.Channel, ".typing")
	if ephemeral {
		subject = messaging.RealtimeLive + f.Channel
	}
	sub := &subscription{position: f.Position, epoch: f.Epoch}
	sub.nats, err = c.server.connection.Conn.Subscribe(subject, func(m *nats.Msg) {
		var event wire.Frame
		if json.Unmarshal(m.Data, &event) != nil {
			return
		}
		event.Channel = f.Channel
		event.Sequence, _ = strconv.ParseUint(m.Header.Get("Nats-Sequence"), 10, 64)
		event.Previous, _ = strconv.ParseUint(m.Header.Get("Nats-Last-Sequence"), 10, 64)
		size := int64(len(m.Data))
		if c.pendingBytes.Add(size) > 4<<20 {
			c.pendingBytes.Add(-size)
			c.cancel()
			return
		}
		select {
		case c.in <- queuedEvent{frame: event, bytes: size}:
		case <-c.ctx.Done():
		default:
			c.cancel()
		}
	})
	if err != nil {
		return err
	}
	if err = sub.nats.SetPendingLimits(256, 4<<20); err != nil {
		_ = sub.nats.Unsubscribe()
		return err
	}
	c.subscriptions[f.Channel] = sub
	if err = c.server.connection.Conn.FlushWithContext(ctx); err != nil {
		return err
	}
	if ephemeral {
		return c.write(wire.Frame{Op: "subscribed", Channel: f.Channel, Reason: "ephemeral"})
	}
	return c.recover(f.Channel, sub, true)
}

// recover 读取频道在当前屏障前的历史并验证历史连续性。
func (c *socket) recover(channel string, sub *subscription, subscribing bool) error {
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	user, err := c.server.Authenticate(ctx, c.ticket)
	if err != nil {
		return err
	}
	allowed, err := c.server.allowed(ctx, user, channel)
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("authorization revoked")
	}
	stream, err := c.server.connection.JS.Stream(ctx, messaging.RealtimeStream)
	if err != nil {
		return err
	}
	info := stream.CachedInfo()
	epoch := strconv.FormatInt(info.Created.UnixNano(), 10)
	head := info.State.LastSeq
	recovered := sub.epoch == epoch && sub.position <= head && info.State.FirstSeq <= sub.position+1
	reason := ""
	if sub.epoch == "" {
		reason = "initial"
	} else if sub.epoch != epoch {
		reason = "epoch"
	} else if !recovered {
		reason = "expired"
	}
	var frames []wire.Frame
	bytes := 0
	if recovered {
		for pos := sub.position + 1; pos <= head; {
			msg, err := stream.GetMsg(ctx, pos, jetstream.WithGetMsgSubject(messaging.RealtimeInput+channel))
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				break
			}
			if err != nil {
				return err
			}
			if msg.Sequence > head {
				break
			}
			var event wire.Frame
			if err = json.Unmarshal(msg.Data, &event); err != nil {
				return err
			}
			event.Sequence, event.Epoch = msg.Sequence, epoch
			bytes += len(msg.Data)
			frames = append(frames, event)
			pos = msg.Sequence + 1
			if len(frames) > 1000 || bytes > 4<<20 {
				recovered = false
				reason = "limit"
				break
			}
		}
		fresh, err := c.server.connection.JS.Stream(ctx, messaging.RealtimeStream)
		if err != nil {
			return err
		}
		if fresh.CachedInfo().Created != info.Created || fresh.CachedInfo().State.FirstSeq > sub.position+1 {
			recovered = false
			reason = "expired"
		}
	}
	if recovered {
		for _, event := range frames {
			if err = c.write(event); err != nil {
				return err
			}
		}
	}
	sub.position, sub.epoch = head, epoch
	op := "subscribed"
	if !subscribing && recovered {
		op = "checkpoint"
	}
	if err = c.write(wire.Frame{Op: op, Channel: channel, Epoch: epoch, Position: head, Recovered: recovered, Reason: reason}); err != nil {
		return fmt.Errorf("write subscription: %w", err)
	}
	return nil
}
