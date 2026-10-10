// Package wecom 提供企业微信智能机器人长连接协议客户端。
package wecom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

const (
	// DefaultURL 是企业微信智能机器人长连接地址。
	DefaultURL = "wss://openws.work.weixin.qq.com"

	// 长连接帧的命令名。
	cmdSubscribe     = "aibot_subscribe"
	cmdPing          = "ping"
	cmdSendMessage   = "aibot_send_msg"
	cmdMsgCallback   = "aibot_msg_callback"
	cmdEventCallback = "aibot_event_callback"

	// eventDisconnected 是同一机器人的新连接完成订阅后旧连接收到的事件类型。
	eventDisconnected = "disconnected_event"

	// 主动发送的会话类型：1 为单聊 userid，2 为群聊 chatid。
	chatTypeSingle = 1
	chatTypeGroup  = 2

	// defaultHeartbeatInterval 是默认心跳间隔。
	defaultHeartbeatInterval = 30 * time.Second
	// defaultAckTimeout 是默认等待平台回执的时限。
	defaultAckTimeout = 10 * time.Second
	// maxFrameSize 是单帧读取上限。
	maxFrameSize = 1 << 20
	// maxMissedHeartbeats 是连续未收到心跳回执即断开的次数。
	maxMissedHeartbeats = 2
	// errcodeRateLimited 是平台频率限制的错误码。
	errcodeRateLimited = 45009
)

var (
	// ErrReplaced 表示同一机器人的新连接完成订阅，当前连接被平台断开。
	ErrReplaced = errors.New("wecom connection replaced by a newer subscription")
	// ErrClosed 表示连接已关闭，请求未写出。
	ErrClosed = errors.New("wecom connection closed")
	// ErrResultUnknown 表示请求已写出但未收到平台回执。
	ErrResultUnknown = errors.New("wecom request result unknown")
)

// APIError 描述平台回执中的非零错误码。
type APIError struct {
	Code    int
	Message string
}

// Error 返回平台错误码与错误说明。
func (e *APIError) Error() string {
	return "wecom errcode " + strconv.Itoa(e.Code) + ": " + e.Message
}

// RateLimited 判断错误是否为平台频率限制。
func (e *APIError) RateLimited() bool {
	return e.Code == errcodeRateLimited
}

// Credentials 定义智能机器人的长连接凭据。
type Credentials struct {
	BotID  string
	Secret string
}

// Dialer 建立企业微信智能机器人长连接。
type Dialer struct {
	URL               string
	WebSocket         *websocket.Dialer
	HeartbeatInterval time.Duration
	AckTimeout        time.Duration
}

// NewDialer 创建使用平台默认地址与环境代理的连接器。
func NewDialer() *Dialer {
	return &Dialer{
		URL:               DefaultURL,
		WebSocket:         websocket.DefaultDialer,
		HeartbeatInterval: defaultHeartbeatInterval,
		AckTimeout:        defaultAckTimeout,
	}
}

// frame 是长连接收发的统一帧结构。
type frame struct {
	Cmd     string          `json:"cmd,omitempty"`
	Headers frameHeaders    `json:"headers"`
	Body    json.RawMessage `json:"body,omitempty"`
	Errcode *int            `json:"errcode,omitempty"`
	Errmsg  string          `json:"errmsg,omitempty"`
}

// frameHeaders 是帧头，req_id 关联请求与回执。
type frameHeaders struct {
	ReqID string `json:"req_id"`
}

// Conn 是已完成订阅认证的长连接，写入并发安全。
type Conn struct {
	ws                *websocket.Conn
	heartbeatInterval time.Duration
	ackTimeout        time.Duration

	writeMu sync.Mutex
	seq     atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan frame
	closed  chan struct{}
	err     error
}

// Connect 建立长连接并完成订阅认证。
func (d *Dialer) Connect(ctx context.Context, credentials Credentials) (*Conn, error) {
	if credentials.BotID == "" || credentials.Secret == "" {
		return nil, connectiontest.InvalidConfigError(errors.New("wecom bot id and secret are required"))
	}
	// 平台握手区分请求头大小写，gorilla/websocket 按 RFC 原样写出 Sec-WebSocket-* 请求头。
	ws, response, err := d.WebSocket.DialContext(ctx, d.URL, nil) //nolint:bodyclose // gorilla/websocket 的握手响应体由调用方按需读取，无须关闭。
	if err != nil {
		if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
			return nil, connectiontest.HTTPStatusError(response.StatusCode)
		}
		return nil, connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	}
	ws.SetReadLimit(maxFrameSize)
	conn := &Conn{
		ws:                ws,
		heartbeatInterval: d.HeartbeatInterval,
		ackTimeout:        d.AckTimeout,
		pending:           map[string]chan frame{},
		closed:            make(chan struct{}),
	}

	// 订阅回执先于任何回调到达，此处同步读取。
	reqID := conn.nextReqID(cmdSubscribe)
	if err := conn.write(frame{Cmd: cmdSubscribe, Headers: frameHeaders{ReqID: reqID}}, map[string]string{"bot_id": credentials.BotID, "secret": credentials.Secret}); err != nil {
		ws.Close()
		return nil, connectiontest.ClassifyTransportError(connectiontest.StageConnect, err)
	}
	deadline := time.Now().Add(d.AckTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = ws.SetReadDeadline(deadline)
	var ack frame
	if err := ws.ReadJSON(&ack); err != nil {
		ws.Close()
		return nil, connectiontest.ClassifyTransportError(connectiontest.StageAuthenticate, err)
	}
	_ = ws.SetReadDeadline(time.Time{})
	if ack.Headers.ReqID != reqID || ack.Errcode == nil {
		ws.Close()
		return nil, connectiontest.NewError(connectiontest.StageAuthenticate, connectiontest.FailureProtocol, errors.New("unexpected wecom subscribe response"))
	}
	if *ack.Errcode != 0 {
		ws.Close()
		return nil, connectiontest.NewError(connectiontest.StageAuthenticate, connectiontest.FailureUnauthorized, &APIError{Code: *ack.Errcode, Message: ack.Errmsg})
	}
	return conn, nil
}

// Serve 持续读取回调并按到达顺序交给 handle，直到连接断开、被新连接替换或 ctx 结束；handle 须尽快返回。
func (c *Conn) Serve(ctx context.Context, handle func(Callback)) error {
	go c.heartbeat()
	stop := context.AfterFunc(ctx, func() { c.shutdown(ctx.Err()) })
	defer stop()
	for {
		var received frame
		if err := c.ws.ReadJSON(&received); err != nil {
			c.shutdown(fmt.Errorf("read wecom frame: %w", err))
			return c.closeErr()
		}
		switch received.Cmd {
		case "":
			c.deliverAck(received)
		case cmdMsgCallback, cmdEventCallback:
			callback, err := parseCallback(received)
			if err != nil {
				slog.WarnContext(ctx, "企业微信回调无法解析，已跳过", "cmd", received.Cmd, "req_id", received.Headers.ReqID, "error", err)
				continue
			}
			if callback.Event != nil && callback.Event.Type == eventDisconnected {
				c.shutdown(ErrReplaced)
				return ErrReplaced
			}
			handle(callback)
		}
	}
}

// SendMarkdown 主动发送 Markdown 消息并等待平台回执；group 为真时 chatID 是群聊编号，否则是单聊成员的 userid。
func (c *Conn) SendMarkdown(ctx context.Context, chatID string, group bool, content string) error {
	chatType := chatTypeSingle
	if group {
		chatType = chatTypeGroup
	}
	body := map[string]any{
		"chatid":    chatID,
		"chat_type": chatType,
		"msgtype":   "markdown",
		"markdown":  map[string]string{"content": content},
	}
	return c.request(ctx, cmdSendMessage, body)
}

// Close 关闭连接并结束 Serve。
func (c *Conn) Close() error {
	c.shutdown(ErrClosed)
	return nil
}

// Done 返回连接结束时关闭的通道。
func (c *Conn) Done() <-chan struct{} {
	return c.closed
}

// heartbeat 按间隔发送心跳，连续未收到回执时断开连接。
func (c *Conn) heartbeat() {
	ticker := time.NewTicker(c.heartbeatInterval)
	defer ticker.Stop()
	missed := 0
	for {
		select {
		case <-c.closed:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), c.heartbeatInterval)
		err := c.request(ctx, cmdPing, nil)
		cancel()
		if err == nil {
			missed = 0
			continue
		}
		missed++
		if missed >= maxMissedHeartbeats {
			c.shutdown(errors.New("wecom heartbeat timeout"))
			return
		}
	}
}

// request 写出请求帧并等待同一 req_id 的回执。
func (c *Conn) request(ctx context.Context, cmd string, body any) error {
	reqID := c.nextReqID(cmd)
	ack := make(chan frame, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[reqID] = ack
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
	}()

	if err := c.write(frame{Cmd: cmd, Headers: frameHeaders{ReqID: reqID}}, body); err != nil {
		c.shutdown(fmt.Errorf("write wecom frame: %w", err))
		return ErrResultUnknown
	}
	timer := time.NewTimer(c.ackTimeout)
	defer timer.Stop()
	select {
	case received := <-ack:
		if received.Errcode != nil && *received.Errcode != 0 {
			return &APIError{Code: *received.Errcode, Message: received.Errmsg}
		}
		return nil
	case <-timer.C:
		return ErrResultUnknown
	case <-c.closed:
		return ErrResultUnknown
	case <-ctx.Done():
		return ErrResultUnknown
	}
}

// write 串行写出一帧，body 为空时省略。
func (c *Conn) write(header frame, body any) error {
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		header.Body = data
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(c.ackTimeout))
	return c.ws.WriteJSON(header)
}

// deliverAck 把回执交给等待中的请求。
func (c *Conn) deliverAck(received frame) {
	c.mu.Lock()
	ack, ok := c.pending[received.Headers.ReqID]
	c.mu.Unlock()
	if !ok {
		return
	}
	// 重复回执只保留首个。
	select {
	case ack <- received:
	default:
	}
}

// nextReqID 生成以命令名为前缀的连接内唯一请求编号。
func (c *Conn) nextReqID(cmd string) string {
	return cmd + "_" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "_" + strconv.FormatUint(c.seq.Add(1), 10)
}

// shutdown 记录首个结束原因并关闭底层连接。
func (c *Conn) shutdown(cause error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = cause
	close(c.closed)
	c.ws.Close()
}

// closeErr 返回连接结束原因。
func (c *Conn) closeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
