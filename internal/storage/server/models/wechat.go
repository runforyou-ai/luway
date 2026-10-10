//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WechatPlatform 表示 PostgreSQL 中部署使用的唯一微信第三方平台配置与最近一次验证票据。
type WechatPlatform struct {
	bun.BaseModel `bun:"table:wechat_platforms,alias:wp"`

	ComponentAppID         string     `bun:"component_app_id,pk"`
	CreatedAt              time.Time  `bun:"created_at,nullzero,default:now()"`
	UpdatedAt              time.Time  `bun:"updated_at,nullzero,default:now()"`
	ComponentAppSecret     string     `bun:"component_app_secret"`
	Token                  string     `bun:"token"`
	EncodingAESKey         string     `bun:"encoding_aes_key"`
	VerifyTicket           *string    `bun:"verify_ticket"`
	VerifyTicketReceivedAt *time.Time `bun:"verify_ticket_received_at"`
}

// WechatAccessToken 表示 PostgreSQL 中按凭据类别与 AppID 保存的微信接口调用凭据与最近一次获取失败。
type WechatAccessToken struct {
	bun.BaseModel `bun:"table:wechat_access_tokens,alias:wat"`

	Credential    string     `bun:"credential,pk"`
	AppID         string     `bun:"app_id,pk"`
	CreatedAt     time.Time  `bun:"created_at,nullzero,default:now()"`
	UpdatedAt     time.Time  `bun:"updated_at,nullzero,default:now()"`
	AccessToken   *string    `bun:"access_token"`
	ExpiresAt     *time.Time `bun:"expires_at"`
	FailedAt      *time.Time `bun:"failed_at"`
	Failure       string     `bun:"failure"`
	FailureDetail string     `bun:"failure_detail"`
	// RefreshStartedAt 是当前刷新租约的开始时间，没有进行中的刷新时为空。
	RefreshStartedAt *time.Time `bun:"refresh_started_at"`
}

// WechatChannelKey 表示 PostgreSQL 中密钥接入公众号渠道的凭据。
type WechatChannelKey struct {
	bun.BaseModel `bun:"table:wechat_channel_keys,alias:wck"`

	ChannelID      string    `bun:"channel_id,pk"`
	WorkspaceID    string    `bun:"workspace_id"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	AppSecret      string    `bun:"app_secret"`
	Token          string    `bun:"token"`
	EncryptionMode string    `bun:"encryption_mode"`
	// EncodingAESKey 在明文模式下为空。
	EncodingAESKey string `bun:"encoding_aes_key"`
	// ServerVerifiedAt 是微信按当前 Token 验证服务器地址成功的时间，尚未验证时为空。
	ServerVerifiedAt *time.Time `bun:"server_verified_at"`
}

// WechatAuthorization 表示 PostgreSQL 中授权接入公众号渠道获得的公众号授权与公众号资料。
type WechatAuthorization struct {
	bun.BaseModel `bun:"table:wechat_authorizations,alias:wa"`

	ChannelID      string    `bun:"channel_id,pk"`
	WorkspaceID    string    `bun:"workspace_id"`
	CreatedAt      time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero,default:now()"`
	ComponentAppID string    `bun:"component_app_id"`
	RefreshToken   string    `bun:"refresh_token"`
	PermissionIDs  []int     `bun:"permission_ids,array"`
	NickName       string    `bun:"nick_name"`
	HeadImageURL   string    `bun:"head_image_url"`
	PrincipalName  string    `bun:"principal_name"`
	UserName       string    `bun:"user_name"`
	AuthorizedAt   time.Time `bun:"authorized_at"`
	// RevokedAt 是公众号取消授权的时间，授权有效时为空。
	RevokedAt *time.Time `bun:"revoked_at"`
}

// WechatAuthorizationIntent 表示 PostgreSQL 中授权接入公众号渠道最近一次发起的授权。
type WechatAuthorizationIntent struct {
	bun.BaseModel `bun:"table:wechat_authorization_intents,alias:wai"`

	ChannelID   string    `bun:"channel_id,pk"`
	WorkspaceID string    `bun:"workspace_id"`
	CreatedAt   time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt   time.Time `bun:"updated_at,nullzero,default:now()"`
	State       string    `bun:"state"`
	PreAuthCode string    `bun:"pre_auth_code"`
	ExpiresAt   time.Time `bun:"expires_at"`
	// CompletedAt 是授权完成时间，尚未完成时为空。
	CompletedAt *time.Time `bun:"completed_at"`
}
