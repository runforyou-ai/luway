// Package client 维护应用实时网关的连接、订阅与恢复游标。
package client

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/runforyou-ai/luway/internal/realtime/wire"
)

// ErrUnauthorized 表示当前身份无法取得实时连接。
var ErrUnauthorized = errors.New("realtime: unauthorized")

// Options 定义公共网关地址和短期票据获取方法。
type Options struct {
	URL      string
	GetToken func(context.Context) (string, error)
}

// Status 表示客户端连接状态。
type Status string

// 客户端连接状态。
const (
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusReconnecting Status = "reconnecting"
	StatusStopped      Status = "stopped"
	StateSubscribed           = "subscribed"
	StateInterrupted          = "interrupted"
	StateDenied               = "denied"
)

// State 描述频道就绪、恢复或中断。
type State struct {
	State     string
	Recovered bool
	Reason    string
	Err       error
}

// Event 是网关交付的业务事件。
type Event struct {
	Name     string
	Data     json.RawMessage
	ID       string
	Sequence uint64
}

// Client 持有一条实时连接及其业务频道。
type Client struct {
	opts     Options
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	writeMu  sync.Mutex
	conn     *websocket.Conn
	status   Status
	statuses []func(Status)
	subs     map[string]*Subscription
	socket   string
	info     json.RawMessage
}

// Subscription 保存一个频道的恢复游标与事件监听器。
type Subscription struct {
	client   *Client
	name     string
	epoch    string
	position uint64
	state    State
	changed  chan struct{}
	states   []func(State)
	events   []func(Event)
	seen     map[string]struct{}
	order    []string
}

// Connect 建立经过票据认证的连接，并开始断线重连。
func Connect(ctx context.Context, opts Options) (*Client, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := &Client{opts: opts, ctx: ctx, cancel: cancel, status: StatusConnecting, subs: map[string]*Subscription{}}
	if err := c.dial(); err != nil {
		cancel()
		return nil, err
	}
	context.AfterFunc(ctx, func() {
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.mu.Unlock()
	})
	go c.run()
	return c, nil
}

// dial 获取新票据并建立当前连接。
func (c *Client) dial() error {
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	ticket, err := c.opts.GetToken(ctx)
	if err != nil {
		return err
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, c.opts.URL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //clock:local
	if err = conn.WriteJSON(wire.Frame{Op: "auth", Ticket: ticket}); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //clock:local
	var ready wire.Frame
	if err = conn.ReadJSON(&ready); err != nil {
		return err
	}
	if ready.Op != "ready" {
		return ErrUnauthorized
	}
	_ = conn.SetReadDeadline(time.Time{})
	c.mu.Lock()
	c.conn = conn
	c.socket = ready.Socket
	c.info = ready.Info
	c.mu.Unlock()
	success = true
	c.setStatus(StatusConnected)
	return nil
}

// run 消费服务端帧，并在连接中断后使用新票据恢复订阅。
func (c *Client) run() {
	defer c.setStatus(StatusStopped)
	defer c.cancel()
	for c.ctx.Err() == nil {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		var frame wire.Frame
		err := conn.ReadJSON(&frame)
		if err == nil {
			if frame.Op == "bye" {
				_ = conn.Close()
			} else {
				c.receive(frame)
				continue
			}
		}
		_ = conn.Close()
		c.setStatus(StatusReconnecting)
		c.mu.Lock()
		subs := make([]*Subscription, 0, len(c.subs))
		for _, s := range c.subs {
			subs = append(subs, s)
		}
		c.mu.Unlock()
		for _, s := range subs {
			s.setState(State{State: StateInterrupted})
		}
		for {
			timer := time.NewTimer(time.Second)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			err = c.dial()
			if errors.Is(err, ErrUnauthorized) {
				return
			}
			if err != nil {
				continue
			}
			c.mu.Lock()
			subs = nil
			for _, s := range c.subs {
				subs = append(subs, s)
			}
			c.mu.Unlock()
			for _, s := range subs {
				_ = s.subscribe()
			}
			break
		}
	}
}

// receive 按序更新频道游标并分发业务事件。
func (c *Client) receive(f wire.Frame) {
	c.mu.Lock()
	s := c.subs[f.Channel]
	if s == nil {
		c.mu.Unlock()
		return
	}
	switch f.Op {
	case "event":
		if f.Sequence != 0 && f.Epoch == s.epoch && f.Sequence <= s.position {
			c.mu.Unlock()
			return
		}
		if f.Sequence != 0 {
			s.position = f.Sequence
			s.epoch = f.Epoch
		}
		if f.ID != "" && f.Sequence != 0 {
			if _, ok := s.seen[f.ID]; ok {
				c.mu.Unlock()
				return
			}
			if s.seen == nil {
				s.seen = map[string]struct{}{}
			}
			s.seen[f.ID] = struct{}{}
			s.order = append(s.order, f.ID)
			if len(s.order) > 1024 {
				delete(s.seen, s.order[0])
				s.order = s.order[1:]
			}
		}
		events := append([]func(Event){}, s.events...)
		c.mu.Unlock()
		for _, fn := range events {
			fn(Event{Name: f.Name, Data: f.Data, ID: f.ID, Sequence: f.Sequence})
		}
	case "checkpoint":
		s.epoch, s.position = f.Epoch, f.Position
		c.mu.Unlock()
	case "subscribed":
		s.epoch, s.position = f.Epoch, f.Position
		c.mu.Unlock()
		s.setState(State{State: StateSubscribed, Recovered: f.Recovered, Reason: f.Reason})
	case "denied":
		c.mu.Unlock()
		s.setState(State{State: "denied", Err: ErrUnauthorized})
	default:
		c.mu.Unlock()
	}
}

// send 串行发送订阅命令。
func (c *Client) send(f wire.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("realtime: closed")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second)) //clock:local
	err := conn.WriteJSON(f)
	if err != nil {
		_ = conn.Close()
	}
	return err
}

// Private 返回指定业务频道的订阅。
func (c *Client) Private(name string) *Subscription {
	c.mu.Lock()
	if existing := c.subs[name]; existing != nil {
		c.mu.Unlock()
		return existing
	}
	s := &Subscription{client: c, name: name, changed: make(chan struct{})}
	c.subs[name] = s
	c.mu.Unlock()
	_ = s.subscribe()
	return s
}

// subscribe 按保存的游标登记频道。
func (s *Subscription) subscribe() error {
	s.client.mu.Lock()
	frame := wire.Frame{Op: "sub", Channel: s.name, Epoch: s.epoch, Position: s.position}
	s.client.mu.Unlock()
	return s.client.send(frame)
}

// setState 更新频道状态并唤醒等待者。
func (s *Subscription) setState(state State) {
	s.client.mu.Lock()
	s.state = state
	close(s.changed)
	s.changed = make(chan struct{})
	fns := append([]func(State){}, s.states...)
	s.client.mu.Unlock()
	for _, fn := range fns {
		fn(state)
	}
}

// Ready 等待频道完成订阅和历史恢复。
func (s *Subscription) Ready(ctx context.Context) error {
	for {
		s.client.mu.Lock()
		state, changed := s.state, s.changed
		s.client.mu.Unlock()
		if state.State == StateSubscribed {
			return nil
		}
		if state.Err != nil {
			return state.Err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.client.ctx.Done():
			return s.client.ctx.Err()
		case <-changed:
		}
	}
}

// OnState 注册频道状态监听器。
func (s *Subscription) OnState(fn func(State)) {
	s.client.mu.Lock()
	s.states = append(s.states, fn)
	state := s.state
	s.client.mu.Unlock()
	if state.State != "" {
		fn(state)
	}
}

// ListenAll 注册全部业务事件监听器。
func (s *Subscription) ListenAll(fn func(Event)) {
	s.client.mu.Lock()
	s.events = append(s.events, fn)
	s.client.mu.Unlock()
}

// Leave 取消频道订阅并删除恢复游标。
func (s *Subscription) Leave() error {
	s.client.mu.Lock()
	delete(s.client.subs, s.name)
	s.client.mu.Unlock()
	return s.client.send(wire.Frame{Op: "unsub", Channel: s.name})
}

// Status 返回当前连接状态。
func (c *Client) Status() Status { c.mu.Lock(); defer c.mu.Unlock(); return c.status }

// SocketID 返回当前连接编号。
func (c *Client) SocketID() string { c.mu.Lock(); defer c.mu.Unlock(); return c.socket }

// Info 解码当前连接的业务身份信息。
func (c *Client) Info() json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append(json.RawMessage(nil), c.info...)
}

// OnStatus 注册连接状态监听器。
func (c *Client) OnStatus(fn func(Status)) {
	c.mu.Lock()
	c.statuses = append(c.statuses, fn)
	status := c.status
	c.mu.Unlock()
	fn(status)
}

// setStatus 更新并发布连接状态。
func (c *Client) setStatus(status Status) {
	c.mu.Lock()
	c.status = status
	fns := append([]func(Status){}, c.statuses...)
	c.mu.Unlock()
	for _, fn := range fns {
		fn(status)
	}
}

// Close 取消重连并关闭底层连接。
func (c *Client) Close() error {
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
