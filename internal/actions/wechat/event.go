//go:build server

package wechat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// ErrPushUnauthorized 表示推送的时间戳、签名或接收方校验失败。
var ErrPushUnauthorized = errors.New("wechat push unauthorized")

const (
	// RefreshPlatformTokenActionName 是刷新平台接口调用凭据的任务 Action 名称。
	RefreshPlatformTokenActionName = "wechat.refresh_platform_token"
	// RefreshPlatformTokenScheduleKey 是定时续期平台凭据的计划标识。
	RefreshPlatformTokenScheduleKey = "wechat-platform-token"
	// tokenRefreshAhead 是收到验证票据时提前刷新平台凭据的剩余有效期。
	tokenRefreshAhead = 30 * time.Minute
)

// RefreshPlatformTokenEnqueueOptions 是投递刷新平台凭据任务的选项：同一时刻最多一个待执行的刷新，失败由定时续期重新触发。
var RefreshPlatformTokenEnqueueOptions = servertask.EnqueueOptions{Queue: servertask.QueueMaintenance, MaxAttempts: 1, IdempotencyKey: "wechat-platform-token"}

// RefreshPlatformTokenInput 是刷新平台凭据任务的输入。
type RefreshPlatformTokenInput struct{}

// ReceivePlatformEventAction 接收开放平台推送到授权事件接收地址的通知。
type ReceivePlatformEventAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewReceivePlatformEventAction 创建授权事件接收操作。
func NewReceivePlatformEventAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *ReceivePlatformEventAction {
	return &ReceivePlatformEventAction{db: db, enqueuer: enqueuer}
}

// Execute 校验并解密通知：验证票据写入平台配置，平台凭据不存在或即将到期时在同一事务中投递刷新任务；授权成功与授权更新投递处理任务；取消授权按通知时间标记渠道授权已取消。尚未配置平台时返回 ErrPlatformNotConfigured，校验失败时返回 ErrPushUnauthorized。
func (a *ReceivePlatformEventAction) Execute(ctx context.Context, query wechat.PushQuery, body []byte) error {
	platform, err := loadPlatform(ctx, a.db.NewSelect())
	if err != nil {
		return err
	}
	cipher, err := wechat.NewCipher(platform.Token, platform.EncodingAESKey, platform.ComponentAppID)
	if err != nil {
		return err
	}
	// 推送时间戳由微信生成，按本机时钟校验时效。
	plain, err := cipher.Open(query, body, time.Now()) //clock:local
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	event, err := wechat.ParseComponentEvent(plain)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPushUnauthorized, err)
	}
	switch event.InfoType {
	case wechat.InfoTypeComponentVerifyTicket:
	case wechat.InfoTypeAuthorized, wechat.InfoTypeUpdateAuthorized:
		return a.enqueueAuthorization(ctx, event)
	case wechat.InfoTypeUnauthorized:
		var revoked []revokedAuthorization
		err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
			var err error
			revoked, err = revokeAuthorization(ctx, tx, platform.ComponentAppID, event.AuthorizerAppID, time.Unix(event.CreateTime, 0))
			return err
		})
		if err != nil {
			return err
		}
		for _, authorization := range revoked {
			slog.InfoContext(logscope.WithWorkspace(ctx, authorization.WorkspaceID), "公众号授权已取消", "channel_id", authorization.ChannelID, "app_id", event.AuthorizerAppID)
		}
		return nil
	default:
		slog.InfoContext(ctx, "微信开放平台通知未处理", "info_type", event.InfoType, "authorizer_app_id", event.AuthorizerAppID)
		return nil
	}
	if event.ComponentVerifyTicket == "" {
		return fmt.Errorf("%w: verify ticket missing", ErrPushUnauthorized)
	}
	return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		result, err := tx.NewUpdate().Model((*servermodels.WechatPlatform)(nil)).
			Where("component_app_id = ?", platform.ComponentAppID).
			Set("verify_ticket = ?", event.ComponentVerifyTicket).
			Set("verify_ticket_received_at = now()").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save wechat verify ticket: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("save wechat verify ticket: %w", err)
		}
		if rows == 0 {
			return ErrPlatformNotConfigured
		}
		// 平台凭据不存在或剩余有效期不足时投递刷新。
		var fresh bool
		if err := tx.NewSelect().Model((*servermodels.WechatAccessToken)(nil)).
			ColumnExpr("count(*) > 0").
			Where("credential = ? AND app_id = ?", domain.WechatCredentialPlatform, platform.ComponentAppID).
			Where("expires_at > now() + make_interval(secs => ?)", tokenRefreshAhead.Seconds()).
			Scan(ctx, &fresh); err != nil {
			return fmt.Errorf("read wechat platform token: %w", err)
		}
		if fresh {
			return nil
		}
		_, err = a.enqueuer.EnqueueIn(ctx, RefreshPlatformTokenActionName, RefreshPlatformTokenInput{}, RefreshPlatformTokenEnqueueOptions)
		return err
	})
}

// enqueueAuthorization 投递处理授权成功或授权更新通知的任务。
func (a *ReceivePlatformEventAction) enqueueAuthorization(ctx context.Context, event wechat.ComponentEvent) error {
	if event.AuthorizerAppID == "" || event.AuthorizationCode == "" {
		return fmt.Errorf("%w: authorization code missing", ErrPushUnauthorized)
	}
	return serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		_, err := a.enqueuer.EnqueueIn(ctx, ApplyAuthorizationEventActionName, AuthorizationEventInput{
			InfoType: event.InfoType, AppID: event.AuthorizerAppID, AuthorizationCode: event.AuthorizationCode,
			PreAuthCode: event.PreAuthCode, CreateTime: event.CreateTime,
		}, ApplyAuthorizationEventEnqueueOptions)
		return err
	})
}

// RefreshPlatformTokenAction 在平台凭据剩余有效期不足时获取新凭据。
type RefreshPlatformTokenAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewRefreshPlatformTokenAction 创建刷新平台凭据任务。
func NewRefreshPlatformTokenAction(db *bun.DB, client *wechat.Client) *RefreshPlatformTokenAction {
	return &RefreshPlatformTokenAction{db: db, client: client}
}

// Execute 平台凭据剩余有效期不足时用已保存的验证票据获取新凭据；获取失败的原因写入凭据状态，由下一次定时续期重新触发。
func (a *RefreshPlatformTokenAction) Execute(ctx context.Context, _ RefreshPlatformTokenInput) error {
	_, err := platformAccessToken(ctx, a.db, a.client, tokenRefreshAhead, false)
	if errors.Is(err, ErrPlatformNotConfigured) || errors.Is(err, ErrVerifyTicketMissing) {
		return nil
	}
	if errors.Is(err, ErrTokenSuperseded) {
		return nil
	}
	if errors.Is(err, ErrTokenFailed) {
		slog.WarnContext(ctx, "微信平台凭据获取失败", "error", err)
		return nil
	}
	return err
}
