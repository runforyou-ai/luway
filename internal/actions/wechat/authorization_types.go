//go:build server

package wechat

import (
	"errors"
	"fmt"
	"time"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/wechat"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
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

// grant 定义用授权码换取的授权信息与公众号资料。
type grant struct {
	authorization wechat.Authorization
	profile       wechat.AuthorizerProfile
}

// errGrantStale 表示授权时间早于已记录的授权或不晚于取消授权时间，授权未写入。
var errGrantStale = errors.New("wechat grant stale")

// revokedAuthorization 表示一条被取消的公众号授权所属的渠道与工作区。
type revokedAuthorization struct {
	ChannelID   string `bun:"channel_id"`
	WorkspaceID string `bun:"workspace_id"`
}

// platformError 把获取平台凭据或调用开放平台接口的失败归为 ErrPlatformUnavailable，部署尚未配置平台与无法连接微信的错误原样返回。
func platformError(err error) error {
	var apiError *wechat.APIError
	if errors.Is(err, ErrVerifyTicketMissing) || errors.Is(err, ErrTokenFailed) || errors.Is(err, ErrTokenSuperseded) || errors.As(err, &apiError) {
		return fmt.Errorf("%w: %w", ErrPlatformUnavailable, err)
	}
	return err
}
