//go:build server

package direct

import "time"

// WorkspaceMember 是账号在一个工作区中的有效成员身份。
type WorkspaceMember struct {
	WorkspaceID string
	UserID      string
}

// AccountMembersSession 是工作区动态事件流使用的账号会话及其全部有效成员身份，Members 按工作区编号排序。
type AccountMembersSession struct {
	AccountID string
	SessionID string
	Remaining time.Duration
	Members   []WorkspaceMember
}
