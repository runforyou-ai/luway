//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CheckAuthorizationAction 立即重新获取授权接入公众号的接口调用凭据。
type CheckAuthorizationAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewCheckAuthorizationAction 创建授权接入凭据检测操作。
func NewCheckAuthorizationAction(db *bun.DB, client *wechat.Client) *CheckAuthorizationAction {
	return &CheckAuthorizationAction{db: db, client: client}
}

// Execute 重新获取授权方接口调用凭据，结果写入凭据状态并通知成员后返回渠道详情；渠道没有有效授权时返回 ErrNotAuthorized。
func (a *CheckAuthorizationAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*AuthorizationChannel, error) {
	channel, err := loadChannelModel(ctx, a.db.NewSelect(), domain.ChannelTypeWechatAuthorization, identity.Workspace.ID, channelID)
	if err != nil {
		return nil, err
	}
	if channel.ProviderAccountID == nil {
		return nil, ErrNotAuthorized
	}
	_, err = AuthorizationAccessToken(ctx, a.db, a.client, *channel.ProviderAccountID, true)
	if err != nil && !errors.Is(err, ErrTokenFailed) && !errors.Is(err, ErrTokenSuperseded) {
		return nil, err
	}
	realtime.Publish(realtime.ServiceInboxChannelChanged(identity.Workspace.ID, channelID))
	return loadAuthorizationChannel(ctx, a.db, identity.Workspace.ID, channelID)
}

// AuthorizationAccessToken 返回授权接入公众号 appID 的有效接口调用凭据，剩余有效期不足时获取新凭据；force 为真时直接获取新凭据。公众号没有有效授权时返回 ErrNotAuthorized。
func AuthorizationAccessToken(ctx context.Context, db *bun.DB, client *wechat.Client, appID string, force bool) (string, error) {
	return accessToken(ctx, db, authorizationTokenKey(appID), tokenUseMargin, force, &authorizationTokenSource{db: db, appID: appID, client: client})
}

// authorizationTokenKey 返回授权接入公众号 appID 的接口调用凭据标识。
func authorizationTokenKey(appID string) tokenKey {
	return tokenKey{Credential: domain.WechatCredentialAuthorization, AppID: appID}
}

// authorizationTokenSource 用授权方刷新令牌经第三方平台获取授权公众号的接口调用凭据。
type authorizationTokenSource struct {
	db     *bun.DB
	appID  string
	client *wechat.Client
	// refreshToken 是认领刷新时读取的授权方刷新令牌。
	refreshToken string
	// rotatedToken 是本次获取时微信返回的刷新令牌，未返回时为空。
	rotatedToken string
}

// prepare 以共享锁读取公众号的有效授权，返回经平台凭据获取授权方凭据的请求。
func (s *authorizationTokenSource) prepare(ctx context.Context, tx bun.Tx) (func(context.Context) (wechat.AccessToken, error), error) {
	authorization, platform, err := loadActiveAuthorization(ctx, tx, "SHARE", s.appID)
	if err != nil {
		return nil, err
	}
	s.refreshToken = authorization.RefreshToken
	componentAppID, refreshToken := platform.ComponentAppID, authorization.RefreshToken
	return func(ctx context.Context) (wechat.AccessToken, error) {
		componentToken, err := PlatformAccessToken(ctx, s.db, s.client, false)
		if err != nil {
			return wechat.AccessToken{}, err
		}
		token, err := s.client.AuthorizerAccessToken(ctx, componentToken, componentAppID, s.appID, refreshToken)
		if err != nil {
			return wechat.AccessToken{}, err
		}
		s.rotatedToken = token.RefreshToken
		return token.AccessToken, nil
	}, nil
}

// commit 锁定公众号的有效授权，确认刷新令牌与认领刷新时一致，微信返回了新的刷新令牌时写回。
func (s *authorizationTokenSource) commit(ctx context.Context, tx bun.Tx) (bool, error) {
	authorization, _, err := loadActiveAuthorization(ctx, tx, "UPDATE", s.appID)
	if errors.Is(err, ErrNotAuthorized) || errors.Is(err, ErrPlatformNotConfigured) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if authorization.RefreshToken != s.refreshToken {
		return false, nil
	}
	if s.rotatedToken == "" || s.rotatedToken == s.refreshToken {
		return true, nil
	}
	if _, err := tx.NewUpdate().Model((*servermodels.WechatAuthorization)(nil)).
		Where("channel_id = ?", authorization.ChannelID).
		Set("refresh_token = ?", s.rotatedToken).
		Exec(ctx); err != nil {
		return false, fmt.Errorf("save wechat authorizer refresh token: %w", err)
	}
	return true, nil
}

// loadActiveAuthorization 在事务内以 lock 指定的锁读取公众号 appID 所属授权接入渠道的有效授权与当前平台配置；授权不存在、已取消或属于其他第三方平台时返回 ErrNotAuthorized。
func loadActiveAuthorization(ctx context.Context, tx bun.Tx, lock, appID string) (*servermodels.WechatAuthorization, *servermodels.WechatPlatform, error) {
	platform, err := loadPlatform(ctx, tx.NewSelect().For("SHARE"))
	if err != nil {
		return nil, nil, err
	}
	authorization := &servermodels.WechatAuthorization{}
	err = tx.NewSelect().Model(authorization).
		Join("JOIN channels AS c ON c.id = wa.channel_id").
		Where("c.type = ?", domain.ChannelTypeWechatAuthorization).
		Where("c.provider_account_id = ?", appID).
		Where("wa.revoked_at IS NULL").
		Where("wa.component_app_id = ?", platform.ComponentAppID).
		For(lock + " OF wa").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotAuthorized
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read wechat authorization: %w", err)
	}
	return authorization, platform, nil
}
