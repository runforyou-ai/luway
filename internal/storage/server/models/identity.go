//go:build server

package models

import "github.com/runforyou-ai/luway/internal/domain"

// AccountIdentity 表示已通过会话认证的账号及本次请求使用的登录会话。
type AccountIdentity struct {
	Account Account
	Session AccountSession
}

// Identity 表示当前账号在某个工作区中的成员身份、所属角色、所属工作区及本次请求使用的登录会话。
type Identity struct {
	Workspace         Workspace
	WorkspaceIdentity WorkspaceIdentity
	User              User
	Role              MemberRole
	Account           Account
	Session           AccountSession
}

// MemberRole 表示成员所属角色的类型与已分配的权限。
type MemberRole struct {
	Kind        domain.RoleKind
	Permissions []domain.PermissionCode
}

// HasPermission 判断成员所属角色是否授予指定权限。
func (i *Identity) HasPermission(code domain.PermissionCode) bool {
	return domain.RoleGrants(i.Role.Kind, i.Role.Permissions, code)
}
