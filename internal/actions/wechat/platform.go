//go:build server

// Package wechat 维护部署级微信第三方平台配置、公众号渠道连接、验证票据与接口调用凭据。
package wechat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	serverinstanceaction "github.com/runforyou-ai/luway/internal/actions/serverinstance"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

var (
	// ErrPlatformNotConfigured 表示部署尚未配置微信第三方平台。
	ErrPlatformNotConfigured = errors.New("wechat platform not configured")
	// ErrVerifyTicketMissing 表示尚未收到微信推送的验证票据。
	ErrVerifyTicketMissing = errors.New("wechat verify ticket missing")
)

// 公众号与第三方平台凭据的字段校验码。
const (
	ValidationAppIDInvalid          common.FieldCode = "WECHAT_APP_ID_INVALID"
	ValidationAppIDImmutable        common.FieldCode = "WECHAT_APP_ID_IMMUTABLE"
	ValidationEncodingAESKeyInvalid common.FieldCode = "WECHAT_ENCODING_AES_KEY_INVALID"
)

const (
	// EventPath 是授权事件接收地址在部署地址下的路径。
	EventPath = "/api/public/wechat/events"
)

// PlatformInput 定义平台管理员提交的第三方平台凭据。
type PlatformInput struct {
	ComponentAppID     string
	ComponentAppSecret string
	Token              string
	EncodingAESKey     string
}

// Platform 定义第三方平台的配置、接入地址、服务器出口与运行状态；Configured 为假时只有接入地址与服务器出口有值。
type Platform struct {
	Configured bool
	// Status 是平台可用状态，Configured 为假时为空。
	Status              domain.WechatPlatformStatus
	ComponentAppID      string
	ComponentAppSecret  string
	Token               string
	EncodingAESKey      string
	AuthorizationDomain string
	EventURL            string
	// MessageURL 是授权公众号消息与事件接收地址，其中的 $APPID$ 由微信替换为公众号 AppID。
	MessageURL             string
	Servers                []ServerEgress
	VerifyTicketReceivedAt *time.Time
	// AccessTokenExpiresAt 是平台接口调用凭据的到期时间，尚未成功获取时为空。
	AccessTokenExpiresAt *time.Time
	// TokenFailedAt 与 TokenFailure 是最近一次获取凭据失败的时间与原因，之后成功获取时为空。
	TokenFailedAt      *time.Time
	TokenFailure       *domain.WechatTokenFailure
	TokenFailureDetail string
}

// ServerEgress 定义一台服务端进程的主机名、配置的出口 IP 与是否在线。
type ServerEgress struct {
	Hostname string
	EgressIP string
	Online   bool
}

// listServerEgress 列出已登记的服务端进程及其配置的出口 IP。
func listServerEgress(ctx context.Context, db bun.IDB) ([]ServerEgress, error) {
	instances, err := serverinstanceaction.ListInstances(ctx, db)
	if err != nil {
		return nil, err
	}
	return arr.Map(instances, func(instance serverinstanceaction.ServerInstance) ServerEgress {
		return ServerEgress{Hostname: instance.Hostname, EgressIP: instance.Config.EgressIP, Online: instance.Online}
	}), nil
}

// PlatformQuery 读取第三方平台配置与状态。
type PlatformQuery struct {
	db        *bun.DB
	publicURL func() string
}

// NewPlatformQuery 创建第三方平台查询，publicURL 返回生成接入地址的部署地址。
func NewPlatformQuery(db *bun.DB, publicURL func() string) *PlatformQuery {
	return &PlatformQuery{db: db, publicURL: publicURL}
}

// Execute 返回第三方平台配置、接入地址、各服务器出口 IP 与平台凭据状态。
func (q *PlatformQuery) Execute(ctx context.Context) (Platform, error) {
	publicURL := q.publicURL()
	output := Platform{EventURL: publicURL + EventPath, MessageURL: publicURL + AuthorizationMessagePath}
	if parsed, err := url.Parse(publicURL); err == nil {
		output.AuthorizationDomain = parsed.Host
	}
	servers, err := listServerEgress(ctx, q.db)
	if err != nil {
		return Platform{}, err
	}
	output.Servers = servers
	platform, err := loadPlatform(ctx, q.db.NewSelect())
	if errors.Is(err, ErrPlatformNotConfigured) {
		return output, nil
	}
	if err != nil {
		return Platform{}, err
	}
	output.Configured = true
	output.ComponentAppID, output.ComponentAppSecret = platform.ComponentAppID, platform.ComponentAppSecret
	output.Token, output.EncodingAESKey = platform.Token, platform.EncodingAESKey
	output.VerifyTicketReceivedAt = platform.VerifyTicketReceivedAt
	var row struct {
		servermodels.WechatAccessToken `bun:",extend"`
		Valid                          bool `bun:"valid"`
	}
	err = q.db.NewSelect().Model(&row).ColumnExpr("wat.*").
		ColumnExpr("coalesce(wat.access_token IS NOT NULL AND wat.expires_at > now(), false) AS valid").
		Where("wat.credential = ? AND wat.app_id = ?", domain.WechatCredentialPlatform, platform.ComponentAppID).Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Platform{}, fmt.Errorf("read wechat platform token: %w", err)
	}
	token := &row.WechatAccessToken
	output.AccessTokenExpiresAt = token.ExpiresAt
	if token.FailedAt != nil {
		output.TokenFailedAt, output.TokenFailure, output.TokenFailureDetail = token.FailedAt, new(domain.WechatTokenFailure(token.Failure)), token.FailureDetail
	}
	// 按最近一次失败、凭据有效期与验证票据判断平台状态。
	switch {
	case token.FailedAt != nil:
		output.Status = domain.WechatPlatformStatusFailed
	case row.Valid:
		output.Status = domain.WechatPlatformStatusReady
	case platform.VerifyTicket == nil:
		output.Status = domain.WechatPlatformStatusWaitingTicket
	default:
		output.Status = domain.WechatPlatformStatusPending
	}
	return output, nil
}

// SavePlatformAction 保存第三方平台凭据并立即验证。
type SavePlatformAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewSavePlatformAction 创建保存第三方平台操作。
func NewSavePlatformAction(db *bun.DB, client *wechat.Client) *SavePlatformAction {
	return &SavePlatformAction{db: db, client: client}
}

// Execute 校验并保存第三方平台凭据；更换 Component AppID 时清空验证票据与原平台凭据，AppSecret 变化时清空平台凭据。保存后已有验证票据时立即获取平台凭据，结果写入凭据状态。
func (a *SavePlatformAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input PlatformInput) error {
	input = PlatformInput{
		ComponentAppID: strings.TrimSpace(input.ComponentAppID), ComponentAppSecret: strings.TrimSpace(input.ComponentAppSecret),
		Token: strings.TrimSpace(input.Token), EncodingAESKey: strings.TrimSpace(input.EncodingAESKey),
	}
	// 校验 Component AppID 格式。
	if !wechat.ValidAppID(input.ComponentAppID) {
		return &common.FieldError{Fields: map[string]common.FieldCode{"componentAppId": ValidationAppIDInvalid}}
	}
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := platformaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		if _, err := platformaction.Lock(ctx, tx); err != nil {
			return err
		}
		current, err := loadPlatform(ctx, tx.NewSelect().For("UPDATE"))
		if errors.Is(err, ErrPlatformNotConfigured) {
			_, err = tx.NewInsert().Model(&servermodels.WechatPlatform{
				ComponentAppID: input.ComponentAppID, ComponentAppSecret: input.ComponentAppSecret,
				Token: input.Token, EncodingAESKey: input.EncodingAESKey,
			}).Exec(ctx)
			return err
		}
		if err != nil {
			return err
		}
		update := tx.NewUpdate().Model((*servermodels.WechatPlatform)(nil)).
			Where("component_app_id = ?", current.ComponentAppID).
			Set("component_app_id = ?", input.ComponentAppID).
			Set("component_app_secret = ?", input.ComponentAppSecret).
			Set("token = ?", input.Token).
			Set("encoding_aes_key = ?", input.EncodingAESKey)
		if current.ComponentAppID != input.ComponentAppID {
			update = update.Set("verify_ticket = NULL").Set("verify_ticket_received_at = NULL")
		}
		if _, err := update.Exec(ctx); err != nil {
			return fmt.Errorf("save wechat platform: %w", err)
		}
		if current.ComponentAppID != input.ComponentAppID || current.ComponentAppSecret != input.ComponentAppSecret {
			return deleteAccessToken(ctx, tx, platformTokenKey(current.ComponentAppID))
		}
		return nil
	})
	if err != nil {
		return err
	}
	slog.InfoContext(logscope.WithAccount(ctx, operator.Account.ID), "已保存微信第三方平台配置", "component_app_id", input.ComponentAppID)
	_, err = PlatformAccessToken(ctx, a.db, a.client, true)
	if errors.Is(err, ErrVerifyTicketMissing) || errors.Is(err, ErrTokenFailed) || errors.Is(err, ErrTokenSuperseded) {
		return nil
	}
	return err
}

// CheckPlatformAction 立即重新获取平台接口调用凭据。
type CheckPlatformAction struct {
	db     *bun.DB
	client *wechat.Client
}

// NewCheckPlatformAction 创建重新检测平台凭据操作。
func NewCheckPlatformAction(db *bun.DB, client *wechat.Client) *CheckPlatformAction {
	return &CheckPlatformAction{db: db, client: client}
}

// Execute 用当前凭据与验证票据获取平台接口调用凭据，结果写入凭据状态；尚未配置平台时返回 ErrPlatformNotConfigured。
func (a *CheckPlatformAction) Execute(ctx context.Context) error {
	_, err := PlatformAccessToken(ctx, a.db, a.client, true)
	if errors.Is(err, ErrVerifyTicketMissing) || errors.Is(err, ErrTokenFailed) || errors.Is(err, ErrTokenSuperseded) {
		return nil
	}
	return err
}

// PlatformAccessToken 返回有效的平台接口调用凭据，剩余有效期不足时获取新凭据；force 为真时直接获取新凭据。
func PlatformAccessToken(ctx context.Context, db *bun.DB, client *wechat.Client, force bool) (string, error) {
	return platformAccessToken(ctx, db, client, tokenUseMargin, force)
}

// platformAccessToken 返回剩余有效期不少于 margin 的平台接口调用凭据。
func platformAccessToken(ctx context.Context, db *bun.DB, client *wechat.Client, margin time.Duration, force bool) (string, error) {
	platform, err := loadPlatform(ctx, db.NewSelect())
	if err != nil {
		return "", err
	}
	return accessToken(ctx, db, platformTokenKey(platform.ComponentAppID), margin, force, platformTokenSource{appID: platform.ComponentAppID, client: client})
}

// platformTokenKey 返回第三方平台 componentAppID 的平台凭据标识。
func platformTokenKey(componentAppID string) tokenKey {
	return tokenKey{Credential: domain.WechatCredentialPlatform, AppID: componentAppID}
}

// platformTokenSource 用第三方平台凭据与最新验证票据获取平台接口调用凭据。
type platformTokenSource struct {
	appID  string
	client *wechat.Client
}

// prepare 以共享锁读取平台配置，确认仍是同一 Component AppID 且已收到验证票据，返回获取平台凭据的请求。
func (s platformTokenSource) prepare(ctx context.Context, tx bun.Tx) (func(context.Context) (wechat.AccessToken, error), error) {
	platform, err := loadPlatform(ctx, tx.NewSelect().For("SHARE"))
	if err != nil {
		return nil, err
	}
	if platform.ComponentAppID != s.appID {
		return nil, ErrPlatformNotConfigured
	}
	if platform.VerifyTicket == nil {
		return nil, ErrVerifyTicketMissing
	}
	secret, ticket := platform.ComponentAppSecret, *platform.VerifyTicket
	return func(ctx context.Context) (wechat.AccessToken, error) {
		return s.client.ComponentAccessToken(ctx, s.appID, secret, ticket)
	}, nil
}

// commit 以共享锁读取平台配置，判断凭据是否仍属于当前 Component AppID。
func (s platformTokenSource) commit(ctx context.Context, tx bun.Tx) (bool, error) {
	platform, err := loadPlatform(ctx, tx.NewSelect().For("SHARE"))
	if errors.Is(err, ErrPlatformNotConfigured) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return platform.ComponentAppID == s.appID, nil
}

// loadPlatform 按给定查询读取第三方平台配置，尚未配置时返回 ErrPlatformNotConfigured。
func loadPlatform(ctx context.Context, query *bun.SelectQuery) (*servermodels.WechatPlatform, error) {
	platform := &servermodels.WechatPlatform{}
	err := query.Model(platform).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPlatformNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("read wechat platform: %w", err)
	}
	return platform, nil
}
