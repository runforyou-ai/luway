package domain

// SessionKind 定义登录会话种类。
type SessionKind string

const (
	// SessionKindUser 是用户端 Bearer 会话。
	SessionKindUser SessionKind = "user"
	// SessionKindAdmin 是平台后台 Cookie 会话。
	SessionKindAdmin SessionKind = "admin"
	// SessionKindSupport 是平台管理员支援进入工作区的短期 Bearer 会话。
	SessionKindSupport SessionKind = "support"
)

// IsSessionKind 判断取值是否为已知会话种类。
func IsSessionKind(value string) bool {
	switch SessionKind(value) {
	case SessionKindUser, SessionKindAdmin, SessionKindSupport:
		return true
	default:
		return false
	}
}
