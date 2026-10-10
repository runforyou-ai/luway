package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// WechatTokenFailure 表示最近一次获取微信接口调用凭据失败的原因。
type WechatTokenFailure = domain.WechatTokenFailure

// WechatPlatformStatus 表示第三方平台的可用状态。
type WechatPlatformStatus = domain.WechatPlatformStatus

// WechatPlatform 定义部署的微信第三方平台配置、需填写到微信开放平台的接入信息、各服务器出口 IP 与平台凭据状态；Configured 为假时凭据字段为空。
type WechatPlatform struct {
	Configured bool `json:"configured"`
	// Status 是平台可用状态，Configured 为假时为空。
	Status             *WechatPlatformStatus `json:"status"`
	ComponentAppID     string                `json:"componentAppId"`
	ComponentAppSecret string                `json:"componentAppSecret"`
	Token              string                `json:"token"`
	EncodingAESKey     string                `json:"encodingAesKey"`
	// AuthorizationDomain 是授权发起页域名。
	AuthorizationDomain string `json:"authorizationDomain"`
	// EventURL 是授权事件接收地址。
	EventURL string `json:"eventUrl"`
	// MessageURL 是授权公众号消息与事件接收地址，其中的 $APPID$ 由微信替换为公众号 AppID。
	MessageURL             string         `json:"messageUrl"`
	Servers                []WechatServer `json:"servers"`
	VerifyTicketReceivedAt *time.Time     `json:"verifyTicketReceivedAt"`
	// AccessTokenExpiresAt 是平台接口调用凭据的到期时间，尚未成功获取时为空。
	AccessTokenExpiresAt *time.Time `json:"accessTokenExpiresAt"`
	// TokenFailedAt 与 TokenFailure 是最近一次获取凭据失败的时间与原因，之后成功获取时为空。
	TokenFailedAt *time.Time          `json:"tokenFailedAt"`
	TokenFailure  *WechatTokenFailure `json:"tokenFailure"`
	// TokenFailureDetail 在白名单失败时是微信识别的调用方 IP，其他失败时是错误码与说明。
	TokenFailureDetail string `json:"tokenFailureDetail"`
}

// WechatServer 定义一台服务端进程的主机名、配置的出口 IP 与是否在线；EgressIP 为空表示未配置。
type WechatServer struct {
	Hostname string `json:"hostname"`
	EgressIP string `json:"egressIp"`
	Online   bool   `json:"online"`
}

// WechatPlatformInput 定义平台管理员提交的第三方平台凭据。
type WechatPlatformInput struct {
	ComponentAppID     string `json:"componentAppId"`
	ComponentAppSecret string `json:"componentAppSecret" validate:"notblank" msg:"field.wechat_app_secret_required"`
	Token              string `json:"token" validate:"alphanum,min=3,max=32" msg:"field.wechat_token_invalid"`
	EncodingAESKey     string `json:"encodingAesKey" validate:"alphanum,len=43" msg:"field.wechat_encoding_aes_key_invalid"`
}

// WechatEncryptionMode 表示密钥接入公众号的消息加密方式。
type WechatEncryptionMode = domain.WechatEncryptionMode

// WechatTokenState 定义公众号接口调用凭据的到期时间与最近一次获取失败。
type WechatTokenState struct {
	// AccessTokenExpiresAt 是公众号接口调用凭据的到期时间，尚未成功获取时为空。
	AccessTokenExpiresAt *time.Time `json:"accessTokenExpiresAt"`
	// TokenFailedAt 与 TokenFailure 是最近一次获取凭据失败的时间与原因，之后成功获取时为空。
	TokenFailedAt *time.Time          `json:"tokenFailedAt"`
	TokenFailure  *WechatTokenFailure `json:"tokenFailure"`
	// TokenFailureDetail 在白名单失败时是微信识别的调用方 IP，其他失败时是错误码与说明。
	TokenFailureDetail string `json:"tokenFailureDetail"`
}

// WechatKeyChannel 定义密钥接入公众号渠道详情与连接。
type WechatKeyChannel struct {
	MessageChannelSummary
	Connection WechatKeyConnection `json:"connection"`
}

// WechatKeyConnection 定义密钥接入渠道的公众号凭据、服务器地址与验证状态、各服务器出口 IP 与接口调用凭据状态；尚未连接时 AppID 为空。
type WechatKeyConnection struct {
	AppID          string                `json:"appId"`
	AppSecret      string                `json:"appSecret"`
	Token          string                `json:"token"`
	EncryptionMode *WechatEncryptionMode `json:"encryptionMode"`
	EncodingAESKey string                `json:"encodingAesKey"`
	// ServerURL 是填写在公众号服务器配置中的消息接收地址。
	ServerURL string `json:"serverUrl"`
	// ServerVerifiedAt 是微信按当前 Token 验证服务器地址成功的时间，尚未验证时为空。
	ServerVerifiedAt *time.Time     `json:"serverVerifiedAt"`
	Servers          []WechatServer `json:"servers"`
	WechatTokenState
}

// WechatKeyConnectionInput 定义密钥接入渠道提交的公众号凭据与消息加密配置；明文模式忽略 EncodingAESKey。
type WechatKeyConnectionInput struct {
	AppID          string               `json:"appId"`
	AppSecret      string               `json:"appSecret" validate:"notblank" msg:"field.wechat_app_secret_required"`
	Token          string               `json:"token" validate:"alphanum,min=3,max=32" msg:"field.wechat_token_invalid"`
	EncryptionMode WechatEncryptionMode `json:"encryptionMode" validate:"oneof=plain safe" msg:"field.wechat_encryption_mode_invalid"`
	EncodingAESKey string               `json:"encodingAesKey"`
}

// WechatAuthorizationStatus 表示授权接入公众号渠道的授权状态。
type WechatAuthorizationStatus = domain.WechatAuthorizationStatus

// WechatPermission 表示授权接入公众号须授予的权限集。
type WechatPermission = domain.WechatPermission

// WechatAuthorizationChannel 定义授权接入公众号渠道详情与授权。
type WechatAuthorizationChannel struct {
	MessageChannelSummary
	Connection WechatAuthorizationConnection `json:"connection"`
}

// WechatAuthorizationConnection 定义授权接入渠道的授权状态、公众号资料、缺少的权限与接口调用凭据状态；尚未授权时 Status 为空，授权无效时凭据状态为空。
type WechatAuthorizationConnection struct {
	// PlatformConfigured 表示部署已配置微信第三方平台，可以发起授权。
	PlatformConfigured bool                       `json:"platformConfigured"`
	AppID              string                     `json:"appId"`
	Status             *WechatAuthorizationStatus `json:"status"`
	NickName           string                     `json:"nickName"`
	HeadImageURL       string                     `json:"headImageUrl"`
	PrincipalName      string                     `json:"principalName"`
	// UserName 是公众号原始 ID。
	UserName           string             `json:"userName"`
	MissingPermissions []WechatPermission `json:"missingPermissions"`
	AuthorizedAt       *time.Time         `json:"authorizedAt"`
	RevokedAt          *time.Time         `json:"revokedAt"`
	WechatTokenState
}

// WechatAuthorizationStart 定义发起授权后在浏览器中打开的授权发起页地址。
type WechatAuthorizationStart struct {
	URL string `json:"url"`
}
