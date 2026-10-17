//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/set"
	"github.com/uptrace/bun"
)

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
