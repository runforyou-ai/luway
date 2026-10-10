//go:build server

package wechat

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/set"
	"github.com/uptrace/bun"
)

var (
	// ErrNotAuthorized 表示授权接入渠道没有有效的公众号授权。
	ErrNotAuthorized = errors.New("wechat account not authorized")
	// ErrAuthorizationIntentNotFound 表示授权标识不存在、已过期或已被重新发起的授权取代。
	ErrAuthorizationIntentNotFound = errors.New("wechat authorization intent not found")
	// ErrNotVerifiedServiceAccount 表示授权的公众号不是已认证服务号。
	ErrNotVerifiedServiceAccount = errors.New("wechat account is not a verified service account")
	// ErrAuthorizedAccountMismatch 表示授权的公众号与渠道已连接的公众号不一致。
	ErrAuthorizedAccountMismatch = errors.New("wechat authorized account mismatch")
	// ErrAuthorizationRevoked 表示本次授权早于已记录的取消授权，授权未生效。
	ErrAuthorizationRevoked = errors.New("wechat authorization revoked")
	// ErrPlatformUnavailable 表示第三方平台尚未就绪或微信拒绝了平台请求。
	ErrPlatformUnavailable = errors.New("wechat platform unavailable")
)

const (
	// AuthorizationPagePath 是授权发起页在部署地址下的路径前缀，后接授权标识。
	AuthorizationPagePath = "/api/public/wechat/authorizations/"
	// ApplyAuthorizationEventActionName 是处理授权成功与授权更新通知的任务 Action 名称。
	ApplyAuthorizationEventActionName = "wechat.apply_authorization_event"
)

// ApplyAuthorizationEventEnqueueOptions 是投递授权通知处理任务的选项。
var ApplyAuthorizationEventEnqueueOptions = servertask.EnqueueOptions{Queue: servertask.QueueMaintenance, MaxAttempts: 5}

// requiredPermissions 是授权接入公众号须授予的权限集及其微信编号。
var requiredPermissions = []struct {
	Permission domain.WechatPermission
	ID         int
}{
	{domain.WechatPermissionMessage, 1},
	{domain.WechatPermissionUser, 2},
	{domain.WechatPermissionMaterial, 11},
}

// AuthorizationConnection 定义授权接入渠道的授权状态、公众号资料、缺少的权限与接口调用凭据状态；尚未授权时 Status 为空。
type AuthorizationConnection struct {
	PlatformConfigured bool
	AppID              string
	Status             *domain.WechatAuthorizationStatus
	NickName           string
	HeadImageURL       string
	PrincipalName      string
	UserName           string
	MissingPermissions []domain.WechatPermission
	AuthorizedAt       *time.Time
	RevokedAt          *time.Time
	TokenState
}

// AuthorizationChannel 定义授权接入公众号渠道详情与授权。
type AuthorizationChannel struct {
	channelaction.MessageChannelRecord
	Connection AuthorizationConnection
}

// AuthorizationResult 定义授权完成后展示的公众号名称。
type AuthorizationResult struct {
	NickName string
}

// AuthorizationEventInput 是处理授权成功与授权更新通知任务的输入。
type AuthorizationEventInput struct {
	InfoType          string `json:"infoType"`
	AppID             string `json:"appId"`
	AuthorizationCode string `json:"authorizationCode"`
	PreAuthCode       string `json:"preAuthCode"`
	CreateTime        int64  `json:"createTime"`
}

// AuthorizationChannelQuery 读取当前企业的单个授权接入公众号渠道。
type AuthorizationChannelQuery struct {
	db *bun.DB
}

// NewAuthorizationChannelQuery 创建授权接入公众号渠道查询。
func NewAuthorizationChannelQuery(db *bun.DB) *AuthorizationChannelQuery {
	return &AuthorizationChannelQuery{db: db}
}

// Execute 返回授权接入公众号渠道详情、授权状态与接口调用凭据状态。
func (q *AuthorizationChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*AuthorizationChannel, error) {
	return loadAuthorizationChannel(ctx, q.db, identity.Workspace.ID, channelID)
}

// StartAuthorizationAction 为授权接入渠道发起公众号授权。
type StartAuthorizationAction struct {
	db        *bun.DB
	client    *wechat.Client
	publicURL func() string
}

// NewStartAuthorizationAction 创建发起授权操作，publicURL 返回生成授权发起页地址的部署地址。
func NewStartAuthorizationAction(db *bun.DB, client *wechat.Client, publicURL func() string) *StartAuthorizationAction {
	return &StartAuthorizationAction{db: db, client: client, publicURL: publicURL}
}

// Execute 获取预授权码并覆盖渠道的授权意图，返回授权发起页地址。部署尚未配置第三方平台时返回 ErrPlatformNotConfigured，平台凭据不可用时返回 ErrPlatformUnavailable。
func (a *StartAuthorizationAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (string, error) {
	if _, err := loadChannelModel(ctx, a.db.NewSelect(), domain.ChannelTypeWechatAuthorization, identity.Workspace.ID, channelID); err != nil {
		return "", err
	}
	platform, err := loadPlatform(ctx, a.db.NewSelect())
	if err != nil {
		return "", err
	}
	componentToken, err := PlatformAccessToken(ctx, a.db, a.client, false)
	if err != nil {
		return "", platformError(err)
	}
	code, err := a.client.CreatePreAuthCode(ctx, componentToken, platform.ComponentAppID)
	if err != nil {
		return "", platformError(err)
	}
	state := rand.Text()
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if _, err := loadChannelModel(ctx, tx.NewSelect().For("UPDATE OF c"), domain.ChannelTypeWechatAuthorization, identity.Workspace.ID, channelID); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&servermodels.WechatAuthorizationIntent{
			ChannelID: channelID, WorkspaceID: identity.Workspace.ID, State: state, PreAuthCode: code.Value,
		}).
			Value("expires_at", "now() + make_interval(secs => ?)", code.ExpiresIn.Seconds()).
			On("CONFLICT (channel_id) DO UPDATE").
			Set("created_at = now()").
			Set("state = EXCLUDED.state").
			Set("pre_auth_code = EXCLUDED.pre_auth_code").
			Set("expires_at = EXCLUDED.expires_at").
			Set("completed_at = NULL").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("save wechat authorization intent: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return a.publicURL() + AuthorizationPagePath + state, nil
}

// AuthorizationPageQuery 解析授权发起页跳转的微信授权页地址。
type AuthorizationPageQuery struct {
	db        *bun.DB
	publicURL func() string
}

// NewAuthorizationPageQuery 创建授权发起页查询，publicURL 返回生成授权回跳地址的部署地址。
func NewAuthorizationPageQuery(db *bun.DB, publicURL func() string) *AuthorizationPageQuery {
	return &AuthorizationPageQuery{db: db, publicURL: publicURL}
}

// Execute 返回授权标识对应的微信授权页地址；渠道已连接公众号时只允许该公众号授权。授权标识不存在或已过期时返回 ErrAuthorizationIntentNotFound。
func (q *AuthorizationPageQuery) Execute(ctx context.Context, state string) (string, error) {
	var row struct {
		PreAuthCode       string  `bun:"pre_auth_code"`
		ProviderAccountID *string `bun:"provider_account_id"`
	}
	err := q.db.NewSelect().TableExpr("wechat_authorization_intents AS wai").
		Join("JOIN channels AS c ON c.id = wai.channel_id").
		ColumnExpr("wai.pre_auth_code, c.provider_account_id").
		Where("wai.state = ?", state).
		Where("wai.expires_at > now()").
		Where("wai.completed_at IS NULL").
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAuthorizationIntentNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read wechat authorization intent: %w", err)
	}
	platform, err := loadPlatform(ctx, q.db.NewSelect())
	if err != nil {
		return "", err
	}
	redirectURI := q.publicURL() + AuthorizationPagePath + state + "/callback"
	return wechat.AuthorizationURL(platform.ComponentAppID, row.PreAuthCode, redirectURI, support.Deref(row.ProviderAccountID)), nil
}

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

// grant 定义用授权码换取的授权信息与公众号资料。
type grant struct {
	authorization wechat.Authorization
	profile       wechat.AuthorizerProfile
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

// errGrantStale 表示授权时间早于已记录的授权或不晚于取消授权时间，授权未写入。
var errGrantStale = errors.New("wechat grant stale")

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

// revokedAuthorization 表示一条被取消的公众号授权所属的渠道与工作区。
type revokedAuthorization struct {
	ChannelID   string `bun:"channel_id"`
	WorkspaceID string `bun:"workspace_id"`
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

// platformError 把获取平台凭据或调用开放平台接口的失败归为 ErrPlatformUnavailable，部署尚未配置平台与无法连接微信的错误原样返回。
func platformError(err error) error {
	var apiError *wechat.APIError
	if errors.Is(err, ErrVerifyTicketMissing) || errors.Is(err, ErrTokenFailed) || errors.Is(err, ErrTokenSuperseded) || errors.As(err, &apiError) {
		return fmt.Errorf("%w: %w", ErrPlatformUnavailable, err)
	}
	return err
}

// loadAuthorizationChannel 读取授权接入公众号渠道详情、授权状态、公众号资料与接口调用凭据状态。
func loadAuthorizationChannel(ctx context.Context, db *bun.DB, workspaceID, channelID string) (*AuthorizationChannel, error) {
	channel, err := loadChannelModel(ctx, db.NewSelect(), domain.ChannelTypeWechatAuthorization, workspaceID, channelID)
	if err != nil {
		return nil, err
	}
	platform, err := loadPlatform(ctx, db.NewSelect())
	if err != nil && !errors.Is(err, ErrPlatformNotConfigured) {
		return nil, err
	}
	output := &AuthorizationChannel{MessageChannelRecord: *channelaction.NewMessageChannelRecord(channel)}
	connection := &output.Connection
	connection.PlatformConfigured = platform != nil
	connection.MissingPermissions = []domain.WechatPermission{}
	if channel.ProviderAccountID == nil {
		return output, nil
	}
	connection.AppID = *channel.ProviderAccountID
	authorization := &servermodels.WechatAuthorization{}
	err = db.NewSelect().Model(authorization).Where("channel_id = ?", channelID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return output, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read wechat authorization: %w", err)
	}
	// 按取消授权与授权所属平台判断授权状态。
	status := domain.WechatAuthorizationActive
	switch {
	case authorization.RevokedAt != nil:
		status = domain.WechatAuthorizationRevoked
	case platform == nil || platform.ComponentAppID != authorization.ComponentAppID:
		status = domain.WechatAuthorizationPlatformChanged
	}
	connection.Status = &status
	connection.NickName, connection.HeadImageURL = authorization.NickName, authorization.HeadImageURL
	connection.PrincipalName, connection.UserName = authorization.PrincipalName, authorization.UserName
	connection.AuthorizedAt, connection.RevokedAt = &authorization.AuthorizedAt, authorization.RevokedAt
	granted := set.Collect(authorization.PermissionIDs)
	for _, required := range requiredPermissions {
		if !granted.Has(required.ID) {
			connection.MissingPermissions = append(connection.MissingPermissions, required.Permission)
		}
	}
	if status == domain.WechatAuthorizationActive {
		connection.TokenState, err = loadTokenState(ctx, db, authorizationTokenKey(connection.AppID))
		if err != nil {
			return nil, err
		}
	}
	return output, nil
}
