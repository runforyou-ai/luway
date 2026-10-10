//go:build server

// Package wecombot 实现企业微信智能机器人渠道的平台适配：长连接、外发与入站媒体下载。
package wecombot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wecom"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

const (
	// wecomBotSendTimeout 是单条消息的发送超时。
	wecomBotSendTimeout = 15 * time.Second
	// wecomBotMarkdownLimit 是平台单条 Markdown 正文的字节上限。
	wecomBotMarkdownLimit = 20480
	// wecomBotRateLimitWait 是平台限流后的重发等待时间，平台限制每会话每分钟 30 条。
	wecomBotRateLimitWait = time.Minute
)

// Adapter 是企业微信智能机器人渠道的适配器。
type Adapter struct {
	db       bun.IDB
	dialer   *wecom.Dialer
	http     connectiontest.HTTPDoer
	sessions channeladapter.SessionSender
}

// NewAdapter 创建企业微信智能机器人渠道适配器，外发请求经 sessions 由本实例持有的长连接发送。
func NewAdapter(db bun.IDB, dialer *wecom.Dialer, http connectiontest.HTTPDoer, sessions channeladapter.SessionSender) *Adapter {
	return &Adapter{db: db, dialer: dialer, http: http, sessions: sessions}
}

// Plan 按平台 Markdown 字节上限切分正文，附件不经本渠道发送。
func (w *Adapter) Plan(body string, _ bool) []channeladapter.Item {
	var items []channeladapter.Item
	for len(body) > wecomBotMarkdownLimit {
		// 在上限内最后一个换行处切分，没有换行时在字符边界切分。
		cut := wecomBotMarkdownLimit
		for cut > 0 && !utf8.RuneStart(body[cut]) {
			cut--
		}
		for i := cut - 1; i > cut/2; i-- {
			if body[i] == '\n' {
				cut = i + 1
				break
			}
		}
		items = append(items, channeladapter.Item{Body: body[:cut]})
		body = body[cut:]
	}
	return append(items, channeladapter.Item{Body: body})
}

// SendTimeout 返回单条消息的发送超时。
func (w *Adapter) SendTimeout(channeladapter.Item) time.Duration {
	return wecomBotSendTimeout
}

// Send 经本实例持有的长连接发送请求项，外发任务按渠道长连接路由由持有连接的实例执行。
func (w *Adapter) Send(ctx context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	return w.sessions.Send(ctx, target, request)
}

// wecomBotCredentials 是渠道当前的机器人凭据与配置版本。
type wecomBotCredentials struct {
	AccountID *string   `bun:"provider_account_id"`
	Secret    string    `bun:"secret"`
	UpdatedAt time.Time `bun:"updated_at"`
}

// credentials 读取渠道当前的机器人凭据，渠道或凭据缺失时返回 sql.ErrNoRows。
func (w *Adapter) credentials(ctx context.Context, target channeladapter.Target) (wecomBotCredentials, error) {
	var current wecomBotCredentials
	err := w.db.NewSelect().TableExpr("wecom_bot_channel_settings AS wbs").ColumnExpr("ch.provider_account_id, wbs.secret, wbs.updated_at").
		Join("JOIN channels AS ch ON ch.id = wbs.channel_id AND ch.workspace_id = wbs.workspace_id").
		Where("ch.id = ? AND ch.workspace_id = ? AND ch.type = ?", target.ChannelID, target.WorkspaceID, domain.ChannelTypeWeComBot).
		Scan(ctx, &current)
	if err == nil && (current.AccountID == nil || current.Secret == "") {
		err = sql.ErrNoRows
	}
	return current, err
}

// Revision 以凭据更新时间作为连接配置版本。
func (w *Adapter) Revision(ctx context.Context, target channeladapter.Target) (string, error) {
	current, err := w.credentials(ctx, target)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(current.UpdatedAt.UnixMicro(), 10), nil
}

// Connect 用渠道当前凭据建立长连接并完成订阅认证。
func (w *Adapter) Connect(ctx context.Context, target channeladapter.Target) (channeladapter.Session, error) {
	current, err := w.credentials(ctx, target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: WeCom bot credentials are missing", channeladapter.ErrConnectionRejected)
	}
	if err != nil {
		return nil, err
	}
	conn, err := w.dialer.Connect(ctx, wecom.Credentials{BotID: *current.AccountID, Secret: current.Secret})
	if _, kind, ok := connectiontest.Details(err); ok && (kind == connectiontest.FailureUnauthorized || kind == connectiontest.FailureInvalidConfig) {
		return nil, fmt.Errorf("%w: %w", channeladapter.ErrConnectionRejected, err)
	}
	if err != nil {
		return nil, err
	}
	return &wecomBotSession{conn: conn, accountID: *current.AccountID, revision: strconv.FormatInt(current.UpdatedAt.UnixMicro(), 10)}, nil
}

// wecomBotMediaRef 是入站媒体的下载地址与解密密钥。
type wecomBotMediaRef struct {
	URL    string `json:"url"`
	AESKey string `json:"aesKey"`
}

// DownloadMedia 下载并解密入站媒体，地址过期或解密失败时平台引用已不可用。
func (w *Adapter) DownloadMedia(ctx context.Context, _ channeladapter.Target, ref string, maxSize int64) (channeladapter.DownloadedMedia, error) {
	var media wecomBotMediaRef
	if err := json.Unmarshal([]byte(ref), &media); err != nil {
		return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: invalid WeCom media reference", channeladapter.ErrMediaRejected)
	}
	downloaded, err := wecom.DownloadMedia(ctx, w.http, wecom.Media{URL: media.URL, AESKey: media.AESKey}, maxSize)
	if err == nil {
		return channeladapter.DownloadedMedia{Data: downloaded.Data, FileName: downloaded.FileName}, nil
	}
	// 网络、超时和平台不可用等待重试，其余失败直接结束。
	_, kind, classified := connectiontest.Details(err)
	if classified && (kind == connectiontest.FailureTimeout || kind == connectiontest.FailureNetwork || kind == connectiontest.FailureTLS || kind == connectiontest.FailureUnavailable) {
		return channeladapter.DownloadedMedia{}, err
	}
	return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: %w", channeladapter.ErrMediaRejected, err)
}

// wecomBotSession 是已完成订阅的智能机器人长连接。
type wecomBotSession struct {
	conn      *wecom.Conn
	accountID string
	revision  string
}

// Revision 返回建立连接时的凭据版本。
func (s *wecomBotSession) Revision() string {
	return s.revision
}

// AccountID 返回连接所属的机器人编号。
func (s *wecomBotSession) AccountID() string {
	return s.accountID
}

// Serve 把私聊消息、群聊消息与进入会话事件归一化后交给 handle。
func (s *wecomBotSession) Serve(ctx context.Context, handle func(channeladapter.InboundEvent)) error {
	err := s.conn.Serve(ctx, func(callback wecom.Callback) {
		event := channeladapter.InboundEvent{ID: callback.MsgID, Sender: callback.UserID, ChatID: callback.ChatID, OccurredAt: callback.CreatedAt}
		if event.ChatID == "" {
			event.ChatID = callback.UserID
		}
		if event.ID == "" {
			event.ID = callback.ReqID
		}
		// 平台未给出发生时间时以本机收到时刻代替来源时间。
		if event.OccurredAt.IsZero() {
			event.OccurredAt = time.Now().UTC() //clock:local
		}
		switch {
		case callback.Event != nil && callback.Event.Type == "enter_chat":
			event.Kind = channeladapter.InboundEventEntered
		case callback.Message != nil && callback.ChatType == wecom.ChatTypeGroup:
			event.Kind = channeladapter.InboundEventGroupMention
		case callback.Message != nil:
			event.Kind = channeladapter.InboundEventMessage
			event.Text = callback.Message.Text
			event.Unsupported = callback.Message.Type == wecom.MessageTypeUnsupported
			for _, media := range callback.Message.Media {
				ref, _ := json.Marshal(wecomBotMediaRef{URL: media.URL, AESKey: media.AESKey})
				event.Media = append(event.Media, channeladapter.InboundMedia{Ref: string(ref), FileName: string(media.Kind)})
			}
		default:
			return
		}
		handle(event)
	})
	if errors.Is(err, wecom.ErrReplaced) {
		return fmt.Errorf("%w: %w", channeladapter.ErrConnectionReplaced, err)
	}
	return err
}

// Send 以 Markdown 主动发送消息给单聊成员或群聊。
func (s *wecomBotSession) Send(ctx context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	if request.File != nil {
		return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "attachment_unsupported"}
	}
	err := s.conn.SendMarkdown(ctx, target.Recipient, target.Group, request.Body)
	var apiErr *wecom.APIError
	switch {
	case err == nil:
		return channeladapter.Result{Outcome: channeladapter.OutcomeSent}
	case errors.Is(err, wecom.ErrClosed):
		return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "connection_unavailable", RetryAfter: time.Second}
	case errors.As(err, &apiErr) && apiErr.RateLimited():
		return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "rate_limited", RetryAfter: wecomBotRateLimitWait}
	case errors.As(err, &apiErr):
		return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "message_rejected"}
	default:
		return channeladapter.Result{Outcome: channeladapter.OutcomeUncertain, Code: "unknown_result"}
	}
}

// Close 关闭长连接。
func (s *wecomBotSession) Close() error {
	return s.conn.Close()
}
