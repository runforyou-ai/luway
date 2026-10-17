//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ApplyAuthorizationEventAction 处理授权成功与授权更新通知。
type ApplyAuthorizationEventAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewApplyAuthorizationEventAction 创建授权通知处理任务。
func NewApplyAuthorizationEventAction(db *bun.DB, client *wechat.Client) *ApplyAuthorizationEventAction {
	return &ApplyAuthorizationEventAction{db: db, client: client}
}

// Execute 授权成功时按预授权码完成对应的授权意图，授权更新时刷新授权接入渠道的授权与公众号资料；找不到对应授权意图或渠道、早于已记录授权状态的通知不生效。
// 授权不满足接入条件、平台未配置或微信拒绝请求时记录警告后结束，其余失败返回错误由任务重试。
func (a *ApplyAuthorizationEventAction) Execute(ctx context.Context, input AuthorizationEventInput) error {
	at := time.Unix(input.CreateTime, 0)
	var err error
	switch input.InfoType {
	case wechat.InfoTypeAuthorized:
		_, err = completeIntent(ctx, a.db, a.client, "pre_auth_code", input.PreAuthCode, input.AuthorizationCode, at)
	case wechat.InfoTypeUpdateAuthorized:
		err = a.update(ctx, input, at)
	default:
		return nil
	}
	switch {
	case errors.Is(err, ErrAuthorizationIntentNotFound), errors.Is(err, ErrAuthorizationRevoked):
		return nil
	case errors.Is(err, ErrNotVerifiedServiceAccount), errors.Is(err, ErrAppIDTaken), errors.Is(err, channelaction.ErrWechatAccountEnabledElsewhere), errors.Is(err, ErrAuthorizedAccountMismatch),
		errors.Is(err, ErrPlatformNotConfigured), errors.As(err, new(*wechat.APIError)):
		slog.WarnContext(ctx, "微信授权通知未生效", "info_type", input.InfoType, "authorizer_app_id", input.AppID, "error", err)
		return nil
	}
	return err
}

// update 用授权更新通知的授权码刷新授权接入渠道的授权与公众号资料。
func (a *ApplyAuthorizationEventAction) update(ctx context.Context, input AuthorizationEventInput, at time.Time) error {
	platform, err := loadPlatform(ctx, a.db.NewSelect())
	if err != nil {
		return err
	}
	var channelID string
	err = a.db.NewSelect().TableExpr("channels AS c").
		Join("JOIN wechat_authorizations AS wa ON wa.channel_id = c.id").
		Column("c.id").
		Where("c.type = ?", domain.ChannelTypeWechatAuthorization).
		Where("c.provider_account_id = ?", input.AppID).
		Where("wa.component_app_id = ?", platform.ComponentAppID).
		Scan(ctx, &channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAuthorizationIntentNotFound
	}
	if err != nil {
		return fmt.Errorf("find wechat authorization channel: %w", err)
	}
	granted, err := exchange(ctx, a.db, a.client, platform, input.AuthorizationCode)
	if err != nil {
		return err
	}
	// saved 记录写入授权的渠道，事务提交后记录日志。
	var saved *servermodels.Channel
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		channel := &servermodels.Channel{}
		if err := tx.NewSelect().Model(channel).Where("c.id = ?", channelID).For("UPDATE OF c").Scan(ctx); err != nil {
			return fmt.Errorf("lock wechat channel: %w", err)
		}
		err := saveGrant(ctx, tx, channel, platform.ComponentAppID, granted, at)
		if errors.Is(err, errGrantStale) {
			return nil
		}
		if err == nil {
			saved = channel
		}
		return err
	})
	if err == nil && saved != nil {
		slog.InfoContext(logscope.WithWorkspace(ctx, saved.WorkspaceID), "已保存公众号授权", "channel_id", saved.ID, "app_id", granted.authorization.AppID)
	}
	return err
}

// revokeAuthorization 在事务内把第三方平台 componentAppID 下公众号 appID 的授权标记为在 at 取消并删除授权方凭据，通知渠道成员并返回被取消的授权；at 早于已记录的授权时间或已在更晚时间取消时不生效。
func revokeAuthorization(ctx context.Context, tx bun.Tx, componentAppID, appID string, at time.Time) ([]revokedAuthorization, error) {
	var revoked []revokedAuthorization
	err := tx.NewRaw(`UPDATE wechat_authorizations AS wa SET revoked_at = ?
FROM channels AS c
WHERE c.id = wa.channel_id AND c.type = ? AND c.provider_account_id = ? AND wa.component_app_id = ?
	AND wa.authorized_at <= ? AND (wa.revoked_at IS NULL OR wa.revoked_at < ?)
RETURNING wa.channel_id, wa.workspace_id`, at, domain.ChannelTypeWechatAuthorization, appID, componentAppID, at, at).Scan(ctx, &revoked)
	if err != nil {
		return nil, fmt.Errorf("revoke wechat authorization: %w", err)
	}
	if len(revoked) == 0 {
		return nil, nil
	}
	if err := deleteAccessToken(ctx, tx, authorizationTokenKey(appID)); err != nil {
		return nil, err
	}
	for _, authorization := range revoked {
		realtime.Notify(ctx, realtime.ServiceInboxChannelChanged(authorization.WorkspaceID, authorization.ChannelID))
	}
	return revoked, nil
}
