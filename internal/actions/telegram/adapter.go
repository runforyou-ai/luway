//go:build server

package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/channeladapter"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

const (
	// telegramTextSendTimeout 是文本请求项的发送超时。
	telegramTextSendTimeout = 20 * time.Second
	// telegramMediaSendTimeout 按 50 MiB 渠道上限与约 2 Mbps 上行带宽设定。
	telegramMediaSendTimeout = 5 * time.Minute
)

// Adapter 是 Telegram 渠道的适配器。
type Adapter struct {
	db         bun.IDB
	sender     telegram.Sender
	downloader telegram.MediaDownloader
	photos     telegram.ProfilePhotoAPI
}

// NewAdapter 创建 Telegram 渠道适配器。
func NewAdapter(db bun.IDB, sender telegram.Sender, downloader telegram.MediaDownloader, photos telegram.ProfilePhotoAPI) *Adapter {
	return &Adapter{db: db, sender: sender, downloader: downloader, photos: photos}
}

// Plan 把一条消息作为一个请求项发送，附件说明随附件发送。
func (a *Adapter) Plan(body string, attachment bool) []channeladapter.Item {
	return []channeladapter.Item{{Body: body, Attachment: attachment}}
}

// SendTimeout 返回文本或附件请求项的发送超时。
func (a *Adapter) SendTimeout(item channeladapter.Item) time.Duration {
	if item.Attachment {
		return telegramMediaSendTimeout
	}
	return telegramTextSendTimeout
}

// Send 使用平台账号对应的机器人凭据发送请求项。
func (a *Adapter) Send(ctx context.Context, target channeladapter.Target, request channeladapter.Request) channeladapter.Result {
	token, err := a.token(ctx, target)
	switch {
	case errors.Is(err, errAccountChanged):
		return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "account_changed"}
	case errors.Is(err, sql.ErrNoRows):
		return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: "invalid_token"}
	case err != nil:
		return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: "credentials_unavailable", RetryAfter: time.Second}
	}
	var messageID int64
	if request.File == nil {
		messageID, err = a.sender.SendText(ctx, token, telegram.TextMessage{ChatID: target.Recipient, Body: request.Body, ReplyMessageID: request.ReplyToMessageID})
	} else {
		file := request.File
		messageID, err = a.sender.SendMedia(ctx, token, telegram.MediaMessage{
			ChatID: target.Recipient, FileName: file.Name, ContentType: file.ContentType, ByteSize: file.ByteSize,
			ImageWidth: file.ImageWidth, ImageHeight: file.ImageHeight, Content: file.Content,
			Caption: request.Body, ReplyMessageID: request.ReplyToMessageID,
		})
	}
	if err == nil && messageID > 0 {
		return channeladapter.Result{Outcome: channeladapter.OutcomeSent, ProviderMessageID: strconv.FormatInt(messageID, 10)}
	}
	var failure *telegram.SendError
	if !errors.As(err, &failure) {
		return channeladapter.Result{Outcome: channeladapter.OutcomeUncertain, Code: "unknown_result"}
	}
	switch failure.Code {
	case "rate_limited":
		return channeladapter.Result{Outcome: channeladapter.OutcomeRetry, Code: failure.Code, RetryAfter: failure.RetryAfter}
	case "invalid_token", "invalid_recipient", "invalid_message", "recipient_unavailable", "message_rejected":
		return channeladapter.Result{Outcome: channeladapter.OutcomeFailed, Code: failure.Code}
	default:
		return channeladapter.Result{Outcome: channeladapter.OutcomeUncertain, Code: failure.Code}
	}
}

// DownloadMedia 使用签发文件引用的机器人下载入站媒体；机器人已变化时文件引用失效。
func (a *Adapter) DownloadMedia(ctx context.Context, target channeladapter.Target, ref string, maxSize int64) (channeladapter.DownloadedMedia, error) {
	token, err := a.token(ctx, target)
	if errors.Is(err, errAccountChanged) || errors.Is(err, sql.ErrNoRows) {
		return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: Telegram bot changed or token is missing", channeladapter.ErrMediaRejected)
	}
	if err != nil {
		return channeladapter.DownloadedMedia{}, fmt.Errorf("load Telegram media bot: %w", err)
	}
	data, err := a.downloader.DownloadMedia(ctx, token, ref, maxSize)
	if err == nil {
		return channeladapter.DownloadedMedia{Data: data}, nil
	}
	// 网络、超时、限流和平台不可用等待重试，平台明确拒绝直接失败。
	_, kind, classified := connectiontest.Details(err)
	switch {
	case !classified:
		return channeladapter.DownloadedMedia{}, err
	case kind == connectiontest.FailureTimeout, kind == connectiontest.FailureNetwork, kind == connectiontest.FailureTLS,
		kind == connectiontest.FailureUnavailable, kind == connectiontest.FailureRateLimited:
		return channeladapter.DownloadedMedia{}, err
	default:
		return channeladapter.DownloadedMedia{}, fmt.Errorf("%w: %w", channeladapter.ErrMediaRejected, err)
	}
}

// errAccountChanged 表示渠道当前连接的平台账号已不是目标平台账号。
var errAccountChanged = errors.New("channel account changed")

// token 读取渠道当前的机器人凭据；渠道已改连其他机器人时返回 errAccountChanged，渠道或凭据缺失时返回 sql.ErrNoRows。
func (a *Adapter) token(ctx context.Context, target channeladapter.Target) (string, error) {
	var current struct {
		AccountID *string `bun:"provider_account_id"`
		Token     *string `bun:"bot_token"`
	}
	err := a.db.NewSelect().TableExpr("telegram_channel_settings AS tcs").ColumnExpr("ch.provider_account_id, tcs.bot_token").
		Join("JOIN channels AS ch ON ch.id = tcs.channel_id AND ch.workspace_id = tcs.workspace_id").
		Where("ch.id = ? AND ch.workspace_id = ? AND ch.type = ?", target.ChannelID, target.WorkspaceID, domain.ChannelTypeTelegram).
		Scan(ctx, &current)
	switch {
	case err != nil:
		return "", err
	case current.AccountID == nil || *current.AccountID != target.AccountID:
		return "", errAccountChanged
	case current.Token == nil || *current.Token == "":
		return "", sql.ErrNoRows
	}
	return *current.Token, nil
}

// CurrentAvatar 使用渠道当前机器人读取对方当前头像，对方没有头像时返回 nil。
func (a *Adapter) CurrentAvatar(ctx context.Context, target channeladapter.Target) (*channeladapter.Avatar, error) {
	token, err := a.token(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("load Telegram avatar bot: %w", err)
	}
	userID, err := strconv.ParseInt(target.Recipient, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse Telegram user id: %w", err)
	}
	photo, err := a.photos.GetUserProfilePhoto(ctx, token, userID)
	if err != nil || photo == nil {
		return nil, err
	}
	return &channeladapter.Avatar{ID: photo.UniqueID, Ref: photo.FileID}, nil
}

// DownloadAvatar 使用渠道当前机器人下载头像图片。
func (a *Adapter) DownloadAvatar(ctx context.Context, target channeladapter.Target, avatar channeladapter.Avatar) (channeladapter.AvatarImage, error) {
	token, err := a.token(ctx, target)
	if err != nil {
		return channeladapter.AvatarImage{}, fmt.Errorf("load Telegram avatar bot: %w", err)
	}
	photo, err := a.photos.DownloadPhoto(ctx, token, avatar.Ref)
	if err != nil {
		return channeladapter.AvatarImage{}, err
	}
	return channeladapter.AvatarImage{ContentType: photo.ContentType, Data: photo.Data}, nil
}
