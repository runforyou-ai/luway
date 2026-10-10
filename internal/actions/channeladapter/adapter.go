//go:build server

// Package channeladapter 定义外部消息平台接入渠道的能力接口与按渠道类型登记的注册表。
package channeladapter

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// Registry 按渠道类型登记适配器，调用方按所需能力接口查询。
type Registry struct {
	adapters map[domain.ChannelType]any
}

// NewRegistry 创建空的适配器注册表。
func NewRegistry() *Registry {
	return &Registry{adapters: map[domain.ChannelType]any{}}
}

// Register 登记渠道类型的适配器，适配器实现的能力接口决定它可提供的能力。
func (r *Registry) Register(channelType domain.ChannelType, adapter any) {
	r.adapters[channelType] = adapter
}

// Lookup 返回渠道类型的适配器所实现的能力接口 T，未登记或未实现时返回 false。
func Lookup[T any](r *Registry, channelType domain.ChannelType) (T, bool) {
	capability, ok := r.adapters[channelType].(T)
	return capability, ok
}

// Target 标识一次平台调用所属的渠道、平台账号与对方编号；Group 为真时 Recipient 是平台群聊编号。
type Target struct {
	WorkspaceID string
	ChannelID   string
	AccountID   string
	Recipient   string
	Group       bool
}

// Item 是一次平台发送请求的内容。
type Item struct {
	Body       string
	Attachment bool
}

// Attachment 是请求项携带的附件元数据与内容。
type Attachment struct {
	Name        string
	ContentType string
	ByteSize    int64
	ImageWidth  int
	ImageHeight int
	Content     io.Reader
}

// Request 是发送一个请求项所需的内容、附件与平台引用。
type Request struct {
	Item
	// File 在 Item.Attachment 为真时有值。
	File *Attachment
	// ReplyToMessageID 是被引用消息的平台消息编号，无引用时为空。
	ReplyToMessageID *string
}

// Outcome 是平台发送结果的归一化分类。
type Outcome string

const (
	// OutcomeSent 表示平台已接受请求。
	OutcomeSent Outcome = "sent"
	// OutcomeFailed 表示平台明确拒绝请求，重发同一请求不会成功。
	OutcomeFailed Outcome = "failed"
	// OutcomeRetry 表示平台限流，等待 RetryAfter 后可以重发。
	OutcomeRetry Outcome = "retry"
	// OutcomeUncertain 表示无法确定平台是否已接受请求。
	OutcomeUncertain Outcome = "uncertain"
)

// CodeReplyWindowClosed 是平台判定回复窗口已关闭时的失败原因码，渠道身份当时可用的回复窗口随之失效。
const CodeReplyWindowClosed = "reply_window_closed"

// Result 是一次平台发送的归一化结果。
type Result struct {
	Outcome Outcome
	// ProviderMessageID 是平台返回的消息编号，平台未返回时为空。
	ProviderMessageID string
	// Code 是可安全记录的失败原因码。
	Code       string
	RetryAfter time.Duration
}

// Outbound 是向外部平台发送消息的能力。
type Outbound interface {
	// Plan 把一条消息的正文与附件拆成按顺序发送的请求项。
	Plan(body string, attachment bool) []Item
	// SendTimeout 返回单个请求项的发送超时。
	SendTimeout(Item) time.Duration
	// Send 使用渠道当前凭据发送一个请求项并归一化平台结果。
	Send(context.Context, Target, Request) Result
}

// SessionSender 经本实例持有的渠道长连接发送外发请求。
type SessionSender interface {
	Send(ctx context.Context, target Target, request Request) Result
}

// Window 是平台按对方互动开启的回复窗口。
type Window struct {
	// Trigger 是开启窗口的平台互动，由适配器定义；同一触发动作只有开启时间最新的窗口可用于发送。
	Trigger   string
	OpenedAt  time.Time
	ExpiresAt time.Time
	// Quota 是窗口内允许发送的平台请求数，为空时不限条数。
	Quota *int
}

// ReplyWindows 是按对方互动开启回复窗口的能力，能力声明受回复窗口限制的渠道类型登记该能力。
type ReplyWindows interface {
	// ReplyWindow 返回入站事件开启的回复窗口，事件不开启窗口时返回 false。
	ReplyWindow(InboundEvent) (Window, bool)
}

// Avatar 是对方在平台中的当前头像。
type Avatar struct {
	// ID 是头像内容的平台稳定编号，编号相同的头像内容相同。
	ID string
	// Ref 是下载头像内容的平台引用。
	Ref string
}

// AvatarImage 是下载的头像图片。
type AvatarImage struct {
	ContentType string
	Data        []byte
}

// Avatars 是读取对方平台头像的能力，渠道身份按该能力同步头像。
type Avatars interface {
	// CurrentAvatar 返回对方当前头像，target.Recipient 是对方编号；对方没有头像时返回 nil。
	CurrentAvatar(ctx context.Context, target Target) (*Avatar, error)
	// DownloadAvatar 下载头像图片。
	DownloadAvatar(ctx context.Context, target Target, avatar Avatar) (AvatarImage, error)
}

// ErrMediaRejected 表示平台明确拒绝媒体下载，重试不会成功。
var ErrMediaRejected = errors.New("channel media rejected")

// DownloadedMedia 是取回的媒体内容；平台随内容给出文件名时 FileName 有值。
type DownloadedMedia struct {
	Data     []byte
	FileName string
}

// MediaSource 是下载入站媒体内容的能力。
type MediaSource interface {
	// DownloadMedia 按平台媒体引用下载不超过 maxSize 字节的内容；平台明确拒绝或平台账号已变化时返回包装 ErrMediaRejected 的错误。
	DownloadMedia(ctx context.Context, target Target, ref string, maxSize int64) (DownloadedMedia, error)
}

// ErrConnectionRejected 表示平台拒绝渠道的长连接凭据或凭据缺失，凭据变更前重连不会成功。
var ErrConnectionRejected = errors.New("channel connection rejected")

// ErrConnectionReplaced 表示同一平台账号的其他连接已接管，当前连接被平台断开。
var ErrConnectionReplaced = errors.New("channel connection replaced")

// ConnectionRoute 返回渠道长连接的任务路由键：持有该路由租约的服务端实例维持连接，并执行该渠道的外发任务。
func ConnectionRoute(channelID string) string {
	return "channel_connection:" + channelID
}

// Connector 是经长连接接入外部平台的能力；连接由通用运行时按渠道的任务路由租约在单个服务端实例上维持，入站事件与外发请求都经该连接。
type Connector interface {
	// Revision 返回渠道当前连接配置的版本，与已建立连接的版本不同时运行时重建连接。
	Revision(ctx context.Context, target Target) (string, error)
	// Connect 读取渠道当前凭据并建立连接；凭据缺失或平台拒绝凭据时返回包装 ErrConnectionRejected 的错误。
	Connect(ctx context.Context, target Target) (Session, error)
}

// Session 是一条已建立的平台长连接。
type Session interface {
	// Revision 返回建立连接时的渠道配置版本。
	Revision() string
	// AccountID 返回连接所属的平台账号。
	AccountID() string
	// Serve 持续接收平台事件直至连接断开或 ctx 结束，被其他连接接管时返回包装 ErrConnectionReplaced 的错误。
	Serve(ctx context.Context, handle func(InboundEvent)) error
	// Send 经当前连接发送一个请求项并归一化平台结果。
	Send(ctx context.Context, target Target, request Request) Result
	// Close 关闭连接。
	Close() error
}

// InboundEventKind 是入站事件的归一化类型。
type InboundEventKind string

const (
	// InboundEventMessage 是对方在私聊中发送的消息。
	InboundEventMessage InboundEventKind = "message"
	// InboundEventGroupMention 是对方在群聊中提及机器人。
	InboundEventGroupMention InboundEventKind = "group_mention"
	// InboundEventEntered 是对方打开与机器人的私聊。
	InboundEventEntered InboundEventKind = "entered"
	// InboundEventInteraction 是对方在私聊之外与平台账号的互动，如关注、扫码与点击菜单，Action 给出互动类型。
	InboundEventInteraction InboundEventKind = "interaction"
)

// InboundMedia 是入站消息携带的平台媒体引用，内容由 MediaSource 按引用取回；平台随消息给出的大小与图片尺寸未知时为零。
type InboundMedia struct {
	Ref         string `json:"ref"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	ByteSize    int64  `json:"byteSize,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	// Caption 是随该媒体发送的说明，与媒体写成同一条消息。
	Caption string `json:"caption,omitempty"`
}

// InboundReply 是入站消息引用的同一平台会话内的消息编号与一层快照。
type InboundReply struct {
	MessageID   string `json:"messageId"`
	Body        string `json:"body,omitempty"`
	SenderName  string `json:"senderName,omitempty"`
	SenderIsBot bool   `json:"senderIsBot,omitempty"`
}

// InboundEvent 是长连接或回调推送收到的归一化入站事件。
type InboundEvent struct {
	Kind InboundEventKind `json:"kind"`
	// ID 是平台事件编号，同一平台账号内唯一。
	ID string `json:"id"`
	// Sender 是对方在平台中的编号。
	Sender string `json:"sender"`
	// SenderName 是平台给出的对方显示名称，平台未给出时为空。
	SenderName string `json:"senderName,omitempty"`
	// ChatID 是事件所在平台会话的编号，回复该会话时作为接收方；私聊时与 Sender 相同。
	ChatID string `json:"chatId"`
	// MessageID 是消息在平台会话内的编号，平台支持引用时有值。
	MessageID string `json:"messageId,omitempty"`
	// Reply 是消息引用的平台消息，未引用时为空。
	Reply      *InboundReply  `json:"reply,omitempty"`
	OccurredAt time.Time      `json:"occurredAt"`
	Text       string         `json:"text,omitempty"`
	Media      []InboundMedia `json:"media,omitempty"`
	// Action 是互动事件的平台互动类型，由适配器定义。
	Action string `json:"action,omitempty"`
	// Unsupported 表示消息类型不受支持，正文与媒体为空。
	Unsupported bool `json:"unsupported,omitempty"`
}
