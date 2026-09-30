//go:build server

package direct

import (
	"time"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// MemberSession 定义实时网关持有的已认证成员会话：公开受众与存活时限所需的工作区、用户、登录会话编号与到期时间，身份详情只供后端内部使用。
type MemberSession struct {
	OrganizationID string
	UserID         string
	SessionID      string
	ExpiresAt      time.Time
	identity       *servermodels.Identity
}

// NewMemberSession 由已解析的成员身份创建成员会话。
func NewMemberSession(identity *servermodels.Identity) MemberSession {
	return MemberSession{
		OrganizationID: identity.Organization.ID, UserID: identity.User.ID,
		SessionID: identity.Session.ID, ExpiresAt: identity.Session.ExpiresAt, identity: identity,
	}
}

// WorkspaceMember 是账号在一个工作区中的有效成员身份。
type WorkspaceMember struct {
	OrganizationID string
	UserID         string
}

// AccountMembersSession 是工作区动态事件流使用的账号会话及其全部有效成员身份，Members 按工作区编号排序。
type AccountMembersSession struct {
	AccountID string
	SessionID string
	ExpiresAt time.Time
	Members   []WorkspaceMember
}
