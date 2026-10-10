//go:build server

package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrAccountNotConnected 表示密钥接入渠道尚未连接公众号。
var ErrAccountNotConnected = errors.New("wechat account not connected")

// KeyConnectionInput 定义密钥接入渠道提交的公众号凭据与消息加密配置。
type KeyConnectionInput struct {
	AppID          string
	AppSecret      string
	Token          string
	EncryptionMode domain.WechatEncryptionMode
	// EncodingAESKey 只在安全模式下使用。
	EncodingAESKey string
}

// KeyConnection 定义密钥接入渠道的公众号凭据、服务器地址与验证状态、服务器出口与接口调用凭据状态；尚未连接时只有服务器地址与服务器出口有值。
type KeyConnection struct {
	AppID          string
	AppSecret      string
	Token          string
	EncryptionMode *domain.WechatEncryptionMode
	EncodingAESKey string
	// ServerURL 是填写在公众号服务器配置中的消息接收地址。
	ServerURL string
	// ServerVerifiedAt 是微信按当前 Token 验证服务器地址成功的时间，尚未验证时为空。
	ServerVerifiedAt *time.Time
	// Servers 是部署中各服务器的出口。
	Servers []ServerEgress
	TokenState
}

// KeyChannel 定义密钥接入公众号渠道详情与连接。
type KeyChannel struct {
	channelaction.MessageChannelRecord
	Connection KeyConnection
}

// KeyChannelQuery 读取当前企业的单个密钥接入公众号渠道。
type KeyChannelQuery struct {
	db        *bun.DB
	publicURL func() string
}

// NewKeyChannelQuery 创建密钥接入公众号渠道查询，publicURL 返回生成服务器地址的部署地址。
func NewKeyChannelQuery(db *bun.DB, publicURL func() string) *KeyChannelQuery {
	return &KeyChannelQuery{db: db, publicURL: publicURL}
}

// Execute 返回密钥接入公众号渠道详情、连接与接口调用凭据状态。
func (q *KeyChannelQuery) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*KeyChannel, error) {
	return loadKeyChannel(ctx, q.db, q.publicURL(), identity.Workspace.ID, channelID)
}

// SaveKeyConnectionAction 连接公众号或更新密钥接入渠道的凭据。
type SaveKeyConnectionAction struct {
	db        *bun.DB
	client    *wechat.Client
	publicURL func() string
}

// NewSaveKeyConnectionAction 创建密钥接入连接保存操作，publicURL 返回生成服务器地址的部署地址。
func NewSaveKeyConnectionAction(db *bun.DB, client *wechat.Client, publicURL func() string) *SaveKeyConnectionAction {
	return &SaveKeyConnectionAction{db: db, client: client, publicURL: publicURL}
}

// Execute 用 AppID 与 AppSecret 获取稳定版接口调用凭据，成功后保存凭据与该接口调用凭据；渠道首次连接时登记公众号 AppID，已连接的渠道不可更换 AppID。
// 凭据未通过微信验证时返回 CredentialError，公众号已连接到其他密钥接入渠道时返回 ErrAppIDTaken。
func (a *SaveKeyConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string, input KeyConnectionInput) (*KeyChannel, error) {
	input = KeyConnectionInput{
		AppID: strings.TrimSpace(input.AppID), AppSecret: strings.TrimSpace(input.AppSecret),
		Token: strings.TrimSpace(input.Token), EncryptionMode: input.EncryptionMode, EncodingAESKey: strings.TrimSpace(input.EncodingAESKey),
	}
	// 校验 AppID 与安全模式的 EncodingAESKey，明文模式不保存 EncodingAESKey。
	fields := map[string]common.FieldCode{}
	if !wechat.ValidAppID(input.AppID) {
		fields["appId"] = ValidationAppIDInvalid
	}
	switch input.EncryptionMode {
	case domain.WechatEncryptionPlain:
		input.EncodingAESKey = ""
	case domain.WechatEncryptionSafe:
		if !wechat.ValidEncodingAESKey(input.EncodingAESKey) {
			fields["encodingAesKey"] = ValidationEncodingAESKeyInvalid
		}
	}
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	appIDImmutable := &common.FieldError{Fields: map[string]common.FieldCode{"appId": ValidationAppIDImmutable}}
	current, err := loadChannelModel(ctx, a.db.NewSelect(), domain.ChannelTypeWechatKey, identity.Workspace.ID, channelID)
	if err != nil {
		return nil, err
	}
	if current.ProviderAccountID != nil && *current.ProviderAccountID != input.AppID {
		return nil, appIDImmutable
	}

	fetched, err := a.client.StableToken(ctx, input.AppID, input.AppSecret)
	if err != nil {
		return nil, newCredentialError(err)
	}
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		channel, err := loadChannelModel(ctx, tx.NewSelect().For("UPDATE OF c"), domain.ChannelTypeWechatKey, identity.Workspace.ID, channelID)
		if err != nil {
			return err
		}
		err = claimAppID(ctx, tx, channel, input.AppID)
		if errors.Is(err, errAppIDChanged) {
			return appIDImmutable
		}
		if err != nil {
			return err
		}
		// 保存密钥接入凭据，Token 变更时清空服务器地址验证时间。
		if _, err := tx.NewInsert().Model(&servermodels.WechatChannelKey{
			ChannelID: channelID, WorkspaceID: identity.Workspace.ID,
			AppSecret: input.AppSecret, Token: input.Token, EncryptionMode: string(input.EncryptionMode), EncodingAESKey: input.EncodingAESKey,
		}).
			On("CONFLICT (channel_id) DO UPDATE").
			Set("app_secret = EXCLUDED.app_secret").
			Set("token = EXCLUDED.token").
			Set("encryption_mode = EXCLUDED.encryption_mode").
			Set("encoding_aes_key = EXCLUDED.encoding_aes_key").
			Set("server_verified_at = CASE WHEN wck.token = EXCLUDED.token THEN wck.server_verified_at END").
			Exec(ctx); err != nil {
			return fmt.Errorf("save wechat channel key: %w", err)
		}
		if err := saveAccessToken(ctx, tx, keyTokenKey(input.AppID), fetched); err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.ServiceInboxChannelChanged(identity.Workspace.ID, channelID))
		return nil
	})
	if err != nil {
		return nil, err
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "已保存公众号密钥接入连接", "channel_id", channelID, "app_id", input.AppID)
	return loadKeyChannel(ctx, a.db, a.publicURL(), identity.Workspace.ID, channelID)
}

// CheckKeyConnectionAction 立即重新获取密钥接入公众号的接口调用凭据。
type CheckKeyConnectionAction struct {
	db        *bun.DB
	client    *wechat.Client
	publicURL func() string
}

// NewCheckKeyConnectionAction 创建密钥接入凭据检测操作，publicURL 返回生成服务器地址的部署地址。
func NewCheckKeyConnectionAction(db *bun.DB, client *wechat.Client, publicURL func() string) *CheckKeyConnectionAction {
	return &CheckKeyConnectionAction{db: db, client: client, publicURL: publicURL}
}

// Execute 重新获取接口调用凭据，结果写入凭据状态并通知成员后返回渠道详情；渠道尚未连接公众号时返回 ErrAccountNotConnected。
func (a *CheckKeyConnectionAction) Execute(ctx context.Context, identity *servermodels.Identity, channelID string) (*KeyChannel, error) {
	channel, err := loadChannelModel(ctx, a.db.NewSelect(), domain.ChannelTypeWechatKey, identity.Workspace.ID, channelID)
	if err != nil {
		return nil, err
	}
	if channel.ProviderAccountID == nil {
		return nil, ErrAccountNotConnected
	}
	_, err = KeyAccessToken(ctx, a.db, a.client, *channel.ProviderAccountID, true)
	if err != nil && !errors.Is(err, ErrTokenFailed) && !errors.Is(err, ErrTokenSuperseded) {
		return nil, err
	}
	realtime.Publish(realtime.ServiceInboxChannelChanged(identity.Workspace.ID, channelID))
	return loadKeyChannel(ctx, a.db, a.publicURL(), identity.Workspace.ID, channelID)
}

// KeyAccessToken 返回密钥接入公众号 appID 的有效接口调用凭据，剩余有效期不足时获取新凭据；force 为真时直接获取新凭据。公众号尚未连接到密钥接入渠道时返回 ErrAccountNotConnected。
func KeyAccessToken(ctx context.Context, db *bun.DB, client *wechat.Client, appID string, force bool) (string, error) {
	return accessToken(ctx, db, keyTokenKey(appID), tokenUseMargin, force, &keyTokenSource{appID: appID, client: client})
}

// keyTokenKey 返回密钥接入公众号 appID 的接口调用凭据标识。
func keyTokenKey(appID string) tokenKey {
	return tokenKey{Credential: domain.WechatCredentialKey, AppID: appID}
}

// keyTokenSource 用密钥接入公众号的 AppSecret 获取稳定版接口调用凭据。
type keyTokenSource struct {
	appID  string
	client *wechat.Client
	// appSecret 是认领刷新时读取的 AppSecret。
	appSecret string
}

// prepare 以共享锁读取公众号的密钥接入凭据，返回获取稳定版凭据的请求。
func (s *keyTokenSource) prepare(ctx context.Context, tx bun.Tx) (func(context.Context) (wechat.AccessToken, error), error) {
	key, err := loadAccountKey(ctx, tx.NewSelect().For("SHARE OF wck"), s.appID)
	if err != nil {
		return nil, err
	}
	s.appSecret = key.AppSecret
	appID, appSecret, client := s.appID, key.AppSecret, s.client
	return func(ctx context.Context) (wechat.AccessToken, error) {
		return client.StableToken(ctx, appID, appSecret)
	}, nil
}

// commit 以共享锁读取公众号的密钥接入凭据，确认 AppSecret 与认领刷新时一致。
func (s *keyTokenSource) commit(ctx context.Context, tx bun.Tx) (bool, error) {
	key, err := loadAccountKey(ctx, tx.NewSelect().For("SHARE OF wck"), s.appID)
	if errors.Is(err, ErrAccountNotConnected) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return key.AppSecret == s.appSecret, nil
}

// loadAccountKey 用 query 读取公众号 appID 所属密钥接入渠道的凭据，公众号未连接到密钥接入渠道时返回 ErrAccountNotConnected。
func loadAccountKey(ctx context.Context, query *bun.SelectQuery, appID string) (*servermodels.WechatChannelKey, error) {
	key := &servermodels.WechatChannelKey{}
	err := query.Model(key).
		Join("JOIN channels AS c ON c.id = wck.channel_id").
		Where("c.type = ?", domain.ChannelTypeWechatKey).
		Where("c.provider_account_id = ?", appID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAccountNotConnected
	}
	if err != nil {
		return nil, fmt.Errorf("read wechat channel key: %w", err)
	}
	return key, nil
}

// loadKeyChannel 读取密钥接入公众号渠道详情、凭据、服务器地址与验证状态、服务器出口与接口调用凭据状态。
func loadKeyChannel(ctx context.Context, db *bun.DB, publicURL, workspaceID, channelID string) (*KeyChannel, error) {
	channel, err := loadChannelModel(ctx, db.NewSelect(), domain.ChannelTypeWechatKey, workspaceID, channelID)
	if err != nil {
		return nil, err
	}
	servers, err := listServerEgress(ctx, db)
	if err != nil {
		return nil, err
	}
	output := &KeyChannel{MessageChannelRecord: *channelaction.NewMessageChannelRecord(channel), Connection: KeyConnection{ServerURL: publicURL + KeyMessagePath(channelID), Servers: servers}}
	if channel.ProviderAccountID == nil {
		return output, nil
	}
	connection := &output.Connection
	connection.AppID = *channel.ProviderAccountID
	key := &servermodels.WechatChannelKey{}
	if err := db.NewSelect().Model(key).Where("channel_id = ?", channelID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("read wechat channel key: %w", err)
	}
	connection.EncryptionMode = new(domain.WechatEncryptionMode(key.EncryptionMode))
	connection.AppSecret, connection.Token, connection.EncodingAESKey = key.AppSecret, key.Token, key.EncodingAESKey
	connection.ServerVerifiedAt = key.ServerVerifiedAt
	connection.TokenState, err = loadTokenState(ctx, db, keyTokenKey(connection.AppID))
	if err != nil {
		return nil, err
	}
	return output, nil
}
