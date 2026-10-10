//go:build server

package wechat

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/uptrace/bun"
)

const (
	// textSendTimeout 是文本请求项的发送超时。
	textSendTimeout = 20 * time.Second
	// mediaSendTimeout 按 10MB 图片素材上传与约 2 Mbps 上行带宽设定。
	mediaSendTimeout = 2 * time.Minute
	// busyRetryAfter 是微信限流或繁忙时重发前的等待时间。
	busyRetryAfter = 5 * time.Second
	// messageWindow 与 messageQuota 是用户发送消息后可回复的时长与条数。
	messageWindow = 48 * time.Hour
	messageQuota  = 5
	// interactionWindow 与 interactionQuota 是关注、扫码与点击菜单后可回复的时长与条数。
	interactionWindow = time.Minute
	interactionQuota  = 3
)

// Adapter 是公众号渠道的适配器：经临时素材接口取回入站媒体，经客服消息接口发送回复，并按用户互动给出回复窗口；使用渠道类型对应的接口调用凭据。
type Adapter struct {
	db          *bun.DB
	client      *wechat.Client
	channelType domain.ChannelType
}

// NewAdapter 创建 channelType 类型公众号渠道的适配器。
func NewAdapter(db *bun.DB, client *wechat.Client, channelType domain.ChannelType) *Adapter {
	return &Adapter{db: db, client: client, channelType: channelType}
}

// errAccountChanged 表示渠道不存在或渠道当前连接的公众号与目标公众号不同。
var errAccountChanged = errors.New("wechat channel account changed")

// checkAccount 确认渠道仍连接目标公众号，渠道不存在或已变化时返回 errAccountChanged。
func (a *Adapter) checkAccount(ctx context.Context, target channeladapter.Target) error {
	var appID *string
	err := a.db.NewSelect().TableExpr("channels").Column("provider_account_id").
		Where("id = ? AND workspace_id = ? AND type = ?", target.ChannelID, target.WorkspaceID, a.channelType).
		Scan(ctx, &appID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (appID == nil || *appID != target.AccountID)) {
		return errAccountChanged
	}
	if err != nil {
		return fmt.Errorf("load wechat channel account: %w", err)
	}
	return nil
}

// accessToken 返回渠道类型对应的公众号接口调用凭据。
func (a *Adapter) accessToken(ctx context.Context, appID string, force bool) (string, error) {
	if a.channelType == domain.ChannelTypeWechatAuthorization {
		return AuthorizationAccessToken(ctx, a.db, a.client, appID, force)
	}
	return KeyAccessToken(ctx, a.db, a.client, appID, force)
}

// credentialsRejected 判断获取接口调用凭据的错误需要管理员处理渠道连接后才能恢复；微信暂时不可达等其他失败可以稍后重试。
func credentialsRejected(err error) bool {
	var tokenError *TokenError
	if errors.As(err, &tokenError) {
		return tokenError.Failure != domain.WechatTokenFailureUnavailable
	}
	return errors.Is(err, ErrAccountNotConnected) || errors.Is(err, ErrNotAuthorized) || errors.Is(err, ErrPlatformNotConfigured)
}

// DownloadMedia 用渠道公众号的接口调用凭据下载临时素材，凭据被微信判定无效时强制刷新后重试一次；渠道公众号已变化、凭据不可用、素材无效或超过 maxSize 字节时返回包装 channeladapter.ErrMediaRejected 的错误。
func (a *Adapter) DownloadMedia(ctx context.Context, target channeladapter.Target, ref string, maxSize int64) (channeladapter.DownloadedMedia, error) {
	if err := a.checkAccount(ctx, target); err != nil {
		if errors.Is(err, errAccountChanged) {
			return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: %w", channeladapter.ErrMediaRejected, err)
		}
		return channeladapter.DownloadedMedia{}, err
	}
	for _, force := range []bool{false, true} {
		token, err := a.accessToken(ctx, target.AccountID, force)
		if errors.Is(err, ErrAccountNotConnected) || errors.Is(err, ErrNotAuthorized) || errors.Is(err, ErrPlatformNotConfigured) {
			return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: %w", channeladapter.ErrMediaRejected, err)
		}
		if err != nil {
			return channeladapter.DownloadedMedia{}, err
		}
		media, err := a.client.DownloadMedia(ctx, token, ref, maxSize)
		var apiError *wechat.APIError
		switch {
		case err == nil:
			return channeladapter.DownloadedMedia{Data: media.Data, FileName: media.FileName}, nil
		case errors.As(err, &apiError) && apiError.TokenInvalid() && !force:
			continue
		case errors.As(err, &apiError), errors.Is(err, wechat.ErrMediaTooLarge):
			return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: %w", channeladapter.ErrMediaRejected, err)
		default:
			return channeladapter.DownloadedMedia{}, err
		}
	}
	return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: wechat access token rejected", channeladapter.ErrMediaRejected)
}

// Plan 把一条消息作为一个客服消息请求发送，附件不带说明；超过渠道文本字节上限的文本按上限拆成多段，优先在换行处断开。
func (a *Adapter) Plan(body string, attachment bool) []channeladapter.Item {
	if attachment {
		return []channeladapter.Item{{Attachment: true}}
	}
	limit := domain.ChannelCapabilitiesOf(a.channelType).TextByteLimit
	var items []channeladapter.Item
	for rest := body; ; {
		if len(rest) <= limit {
			return append(items, channeladapter.Item{Body: rest})
		}
		// 在上限内按完整字符断开，后半段有换行时在最后一个换行处断开。
		cut := limit
		for !utf8.RuneStart(rest[cut]) {
			cut--
		}
		if newline := strings.LastIndexByte(rest[:cut], '\n'); newline >= limit/2 {
			cut = newline + 1
		}
		items = append(items, channeladapter.Item{Body: strings.TrimRight(rest[:cut], "\n")})
		rest = rest[cut:]
	}
}

// SendTimeout 返回文本或附件请求项的发送超时，附件含上传临时素材的时间。
func (a *Adapter) SendTimeout(item channeladapter.Item) time.Duration {
	if item.Attachment {
		return mediaSendTimeout
	}
	return textSendTimeout
}

// Send 用渠道公众号的接口调用凭据发送客服消息，附件先上传为临时素材；凭据被微信判定无效时强制刷新后重试一次。
func (a *Adapter) Send(ctx context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	if err := a.checkAccount(ctx, target); err != nil {
		if errors.Is(err, errAccountChanged) {
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "account_changed"}
		}
		return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "credentials_unavailable", RetryAfter: time.Second}
	}
	message := wechat.CustomMessage{ToUser: target.Recipient, Text: request.Body}
	var content []byte
	if request.File != nil {
		// 图片与语音分别上传为对应类型的临时素材，内容读入内存以便凭据刷新后重新上传。
		switch contentType := strings.ToLower(request.File.ContentType); {
		case strings.HasPrefix(contentType, "image/"):
			message.MediaType = wechat.MediaTypeImage
		case strings.HasPrefix(contentType, "audio/"):
			message.MediaType = wechat.MediaTypeVoice
		default:
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "message_rejected"}
		}
		var err error
		if content, err = io.ReadAll(request.File.Content); err != nil {
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "attachment_unavailable"}
		}
		message.Text = ""
	}
	for _, force := range []bool{false, true} {
		token, err := a.accessToken(ctx, target.AccountID, force)
		if credentialsRejected(err) {
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "invalid_token"}
		}
		if err != nil {
			return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "credentials_unavailable", RetryAfter: busyRetryAfter}
		}
		// 上传失败时消息尚未发送，可以安全重发。
		if request.File != nil {
			message.MediaID, err = a.client.UploadMedia(ctx, token, message.MediaType, request.File.Name, request.File.ContentType, bytes.NewReader(content))
			var apiError *wechat.APIError
			switch {
			case errors.As(err, &apiError) && apiError.TokenInvalid() && !force:
				continue
			case errors.As(err, &apiError) && apiError.Busy():
				return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "rate_limited", RetryAfter: busyRetryAfter}
			case errors.As(err, &apiError):
				return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "message_rejected"}
			case err != nil:
				return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "attachment_upload_failed", RetryAfter: busyRetryAfter}
			}
		}
		err = a.client.SendCustomMessage(ctx, token, message)
		var apiError *wechat.APIError
		switch {
		case err == nil:
			return channeladapter.Result{Outcome: channeladapter.OutcomeSent}
		case errors.As(err, &apiError) && apiError.TokenInvalid() && !force:
			continue
		case errors.As(err, &apiError) && apiError.ReplyWindowClosed():
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: channeladapter.CodeReplyWindowClosed}
		case errors.As(err, &apiError) && apiError.RecipientUnavailable():
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "recipient_unavailable"}
		case errors.As(err, &apiError) && apiError.Busy():
			return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "rate_limited", RetryAfter: busyRetryAfter}
		case errors.As(err, &apiError) && apiError.MessageRejected():
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "invalid_message"}
		case errors.As(err, &apiError) && apiError.TokenInvalid():
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "invalid_token"}
		case errors.As(err, &apiError):
			return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "message_rejected"}
		default:
			return channeladapter.Result{Outcome: channeladapter.OutcomeUncertain, Code: "unknown_result"}
		}
	}
	return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "invalid_token"}
}

// ReplyWindow 按微信客服消息规则给出回复窗口：用户发送消息后 48 小时内可回复 5 条，关注、扫码与点击菜单后 1 分钟内可回复 3 条。
func (a *Adapter) ReplyWindow(event channeladapter.InboundEvent) (channeladapter.Window, bool) {
	trigger, duration, quota := triggerMessage, messageWindow, messageQuota
	switch event.Kind {
	case channeladapter.InboundEventMessage:
	case channeladapter.InboundEventInteraction:
		trigger, duration, quota = event.Action, interactionWindow, interactionQuota
	default:
		return channeladapter.Window{}, false
	}
	return channeladapter.Window{Trigger: trigger, OpenedAt: event.OccurredAt, ExpiresAt: event.OccurredAt.Add(duration), Quota: &quota}, true
}
