package domain

// WechatTokenFailure 定义最近一次获取微信接口调用凭据失败的原因。
type WechatTokenFailure string

const (
	WechatTokenFailureIPNotWhitelisted WechatTokenFailure = "ip_not_whitelisted"
	WechatTokenFailureRejected         WechatTokenFailure = "rejected"
	WechatTokenFailureUnavailable      WechatTokenFailure = "unavailable"
)

// WechatPlatformStatus 定义第三方平台的可用状态。
type WechatPlatformStatus string

const (
	// WechatPlatformStatusWaitingTicket 表示尚未收到微信推送的验证票据。
	WechatPlatformStatusWaitingTicket WechatPlatformStatus = "waiting_ticket"
	// WechatPlatformStatusPending 表示已收到验证票据，平台凭据尚未获取或已过期。
	WechatPlatformStatusPending WechatPlatformStatus = "pending"
	// WechatPlatformStatusReady 表示平台凭据在有效期内。
	WechatPlatformStatusReady WechatPlatformStatus = "ready"
	// WechatPlatformStatusFailed 表示最近一次获取平台凭据失败。
	WechatPlatformStatusFailed WechatPlatformStatus = "failed"
)

// WechatCredential 定义微信接口调用凭据的类别。
type WechatCredential string

const (
	// WechatCredentialPlatform 表示第三方平台的平台凭据。
	WechatCredentialPlatform WechatCredential = "platform"
	// WechatCredentialKey 表示密钥接入公众号用 AppSecret 获取的凭据。
	WechatCredentialKey WechatCredential = "key"
	// WechatCredentialAuthorization 表示授权接入公众号经第三方平台获取的授权方凭据。
	WechatCredentialAuthorization WechatCredential = "authorization"
)

// WechatAuthorizationStatus 定义授权接入公众号渠道的授权状态。
type WechatAuthorizationStatus string

const (
	// WechatAuthorizationActive 表示公众号授权有效。
	WechatAuthorizationActive WechatAuthorizationStatus = "active"
	// WechatAuthorizationRevoked 表示公众号已取消授权。
	WechatAuthorizationRevoked WechatAuthorizationStatus = "revoked"
	// WechatAuthorizationPlatformChanged 表示部署已更换第三方平台，须重新授权。
	WechatAuthorizationPlatformChanged WechatAuthorizationStatus = "platform_changed"
)

// WechatPermission 定义授权接入公众号须授予的权限集。
type WechatPermission string

const (
	// WechatPermissionMessage 表示消息管理权限。
	WechatPermissionMessage WechatPermission = "message"
	// WechatPermissionUser 表示用户管理权限。
	WechatPermissionUser WechatPermission = "user"
	// WechatPermissionMaterial 表示素材管理权限。
	WechatPermissionMaterial WechatPermission = "material"
)

// WechatEncryptionMode 定义密钥接入公众号的消息加密方式。
type WechatEncryptionMode string

const (
	// WechatEncryptionPlain 表示明文模式。
	WechatEncryptionPlain WechatEncryptionMode = "plain"
	// WechatEncryptionSafe 表示安全模式，消息使用 EncodingAESKey 加密。
	WechatEncryptionSafe WechatEncryptionMode = "safe"
)
