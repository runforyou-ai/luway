//go:build server

package models

// AccountIdentity 表示已通过会话认证的账号及本次请求使用的登录会话。
type AccountIdentity struct {
	Account Account
	Session AccountSession
}

// Identity 表示当前账号在某个工作区中的成员身份、所属工作区及本次请求使用的登录会话。
type Identity struct {
	Organization         Organization
	OrganizationIdentity OrganizationIdentity
	User                 User
	Account              Account
	Session              AccountSession
}
