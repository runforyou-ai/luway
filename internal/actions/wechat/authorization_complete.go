//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CompleteAuthorizationAction 在公众号管理员授权后回跳时完成授权。
type CompleteAuthorizationAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewCompleteAuthorizationAction 创建授权回跳处理操作。
func NewCompleteAuthorizationAction(db *bun.DB, client *wechat.Client) *CompleteAuthorizationAction {
	return &CompleteAuthorizationAction{db: db, client: client}
}

// Execute 用授权码完成授权标识对应的授权并返回公众号名称。授权已由授权成功通知完成时返回同一结果；授权标识不存在时返回 ErrAuthorizationIntentNotFound。
func (a *CompleteAuthorizationAction) Execute(ctx context.Context, state, authorizationCode string) (AuthorizationResult, error) {
	if state == "" || authorizationCode == "" {
		return AuthorizationResult{}, ErrAuthorizationIntentNotFound
	}
	// 授权时间取数据库时刻，按微信通知的整秒精度记录。
	var at time.Time
	if err := a.db.NewSelect().ColumnExpr("date_trunc('second', now())").Scan(ctx, &at); err != nil {
		return AuthorizationResult{}, fmt.Errorf("read database time: %w", err)
	}
	return completeIntent(ctx, a.db, a.client, "state", state, authorizationCode, at)
}

// exchange 用授权码换取授权信息与公众号资料；公众号不是已认证服务号时返回 ErrNotVerifiedServiceAccount，平台凭据不可用时返回 ErrPlatformUnavailable。
func exchange(ctx context.Context, db *bun.DB, client *wechat.Client, platform *servermodels.WechatPlatform, authorizationCode string) (grant, error) {
	componentToken, err := PlatformAccessToken(ctx, db, client, false)
	if err != nil {
		return grant{}, platformError(err)
	}
	authorization, err := client.QueryAuthorization(ctx, componentToken, platform.ComponentAppID, authorizationCode)
	if err != nil {
		return grant{}, platformError(err)
	}
	profile, err := client.AuthorizerProfile(ctx, componentToken, platform.ComponentAppID, authorization.AppID)
	if err != nil {
		return grant{}, platformError(err)
	}
	if !profile.VerifiedServiceAccount() {
		return grant{}, ErrNotVerifiedServiceAccount
	}
	return grant{authorization: authorization, profile: profile}, nil
}

// completeIntent 用授权码完成 column 等于 value 的授权意图：换取授权后锁定意图，意图尚未完成时写入授权并记录完成时间，at 为授权发生的时间。
// 意图已由回跳或通知完成时，渠道当前授权就是本次授权则返回同一结果；意图已被重新发起取代时返回 ErrAuthorizationIntentNotFound，授权不晚于已记录的取消授权时返回 ErrAuthorizationRevoked。
func completeIntent(ctx context.Context, db *bun.DB, client *wechat.Client, column, value, authorizationCode string, at time.Time) (AuthorizationResult, error) {
	intent := &servermodels.WechatAuthorizationIntent{}
	err := db.NewSelect().Model(intent).Where("? = ?", bun.Ident(column), value).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthorizationResult{}, ErrAuthorizationIntentNotFound
	}
	if err != nil {
		return AuthorizationResult{}, fmt.Errorf("read wechat authorization intent: %w", err)
	}
	platform, err := loadPlatform(ctx, db.NewSelect())
	if err != nil {
		return AuthorizationResult{}, err
	}
	granted, err := exchange(ctx, db, client, platform, authorizationCode)
	if err != nil {
		return AuthorizationResult{}, err
	}
	var consumed, stale bool
	err = realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		channel, err := loadChannelModel(ctx, tx.NewSelect().For("UPDATE OF c"), domain.ChannelTypeWechatAuthorization, intent.WorkspaceID, intent.ChannelID)
		if err != nil {
			return err
		}
		current := &servermodels.WechatAuthorizationIntent{}
		err = tx.NewSelect().Model(current).Where("channel_id = ?", intent.ChannelID).Where("state = ?", intent.State).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAuthorizationIntentNotFound
		}
		if err != nil {
			return fmt.Errorf("lock wechat authorization intent: %w", err)
		}
		if current.CompletedAt != nil {
			consumed = true
			return nil
		}
		if _, err := tx.NewUpdate().Model(current).WherePK().Set("completed_at = now()").Exec(ctx); err != nil {
			return fmt.Errorf("complete wechat authorization intent: %w", err)
		}
		err = saveGrant(ctx, tx, channel, platform.ComponentAppID, granted, at)
		if errors.Is(err, errGrantStale) {
			stale = true
			return nil
		}
		return err
	})
	if err == nil && stale {
		return AuthorizationResult{}, ErrAuthorizationRevoked
	}
	if err != nil {
		return AuthorizationResult{}, err
	}
	if !consumed {
		slog.InfoContext(logscope.WithWorkspace(ctx, intent.WorkspaceID), "已保存公众号授权", "channel_id", intent.ChannelID, "app_id", granted.authorization.AppID)
	}
	if consumed {
		// 意图已完成时，以渠道当前的有效授权确认结果。
		var authorized bool
		if err := db.NewSelect().TableExpr("wechat_authorizations AS wa").
			Join("JOIN channels AS c ON c.id = wa.channel_id").
			ColumnExpr("count(*) > 0").
			Where("wa.channel_id = ?", intent.ChannelID).
			Where("c.provider_account_id = ?", granted.authorization.AppID).
			Where("wa.component_app_id = ?", platform.ComponentAppID).
			Where("wa.revoked_at IS NULL").
			Scan(ctx, &authorized); err != nil {
			return AuthorizationResult{}, fmt.Errorf("read wechat authorization: %w", err)
		}
		if !authorized {
			return AuthorizationResult{}, ErrAuthorizationIntentNotFound
		}
	}
	return AuthorizationResult{NickName: granted.profile.NickName}, nil
}

// saveGrant 在事务内为已锁定的授权接入渠道写入授权、公众号资料与授权方凭据并通知成员；at 早于已记录的授权时间或不晚于取消授权时间时不写入并返回 errGrantStale。
// 渠道首次授权时登记公众号 AppID；授权的公众号与渠道已连接的不一致时返回 ErrAuthorizedAccountMismatch，已被其他授权接入渠道占用时返回 ErrAppIDTaken，该公众号的密钥接入渠道已启用时返回 channelaction.ErrWechatAccountEnabledElsewhere。
func saveGrant(ctx context.Context, tx bun.Tx, channel *servermodels.Channel, componentAppID string, granted grant, at time.Time) error {
	appID := granted.authorization.AppID
	err := claimAppID(ctx, tx, channel, appID)
	if errors.Is(err, errAppIDChanged) {
		return ErrAuthorizedAccountMismatch
	}
	if err != nil {
		return err
	}
	profile := granted.profile
	result, err := tx.NewInsert().Model(&servermodels.WechatAuthorization{
		ChannelID: channel.ID, WorkspaceID: channel.WorkspaceID, ComponentAppID: componentAppID,
		RefreshToken: granted.authorization.RefreshToken, PermissionIDs: granted.authorization.PermissionIDs,
		NickName: profile.NickName, HeadImageURL: profile.HeadImageURL, PrincipalName: profile.PrincipalName, UserName: profile.UserName,
		AuthorizedAt: at,
	}).
		On("CONFLICT (channel_id) DO UPDATE").
		Set("component_app_id = EXCLUDED.component_app_id").
		Set("refresh_token = EXCLUDED.refresh_token").
		Set("permission_ids = EXCLUDED.permission_ids").
		Set("nick_name = EXCLUDED.nick_name").
		Set("head_image_url = EXCLUDED.head_image_url").
		Set("principal_name = EXCLUDED.principal_name").
		Set("user_name = EXCLUDED.user_name").
		Set("authorized_at = EXCLUDED.authorized_at").
		Set("revoked_at = NULL").
		Where("wa.authorized_at <= EXCLUDED.authorized_at").
		Where("wa.revoked_at IS NULL OR wa.revoked_at < EXCLUDED.authorized_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("save wechat authorization: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save wechat authorization: %w", err)
	}
	if rows == 0 {
		return errGrantStale
	}
	if err := saveAccessToken(ctx, tx, authorizationTokenKey(appID), granted.authorization.AccessToken); err != nil {
		return err
	}
	realtime.Notify(ctx, realtime.ServiceInboxChannelChanged(channel.WorkspaceID, channel.ID))
	return nil
}
