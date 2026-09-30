package domain

// LoginAttemptPurpose 定义官方账号登录尝试的用途。
type LoginAttemptPurpose string

const (
	LoginAttemptPurposeLogin LoginAttemptPurpose = "login"
)

// OfficialLoginClientType 定义发起官方账号登录的客户端类型。
type OfficialLoginClientType string

const (
	OfficialLoginClientWeb OfficialLoginClientType = "web"
)
