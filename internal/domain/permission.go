package domain

// PermissionCode 定义可分配给角色的权限。
type PermissionCode string

const (
	PermissionExternalContactsView   PermissionCode = "external_contacts.view"
	PermissionExternalContactsManage PermissionCode = "external_contacts.manage"
	PermissionTeamMembersView        PermissionCode = "team_members.view"
	PermissionTeamMembersManage      PermissionCode = "team_members.manage"
	PermissionChannelsView           PermissionCode = "channels.view"
	PermissionChannelsManage         PermissionCode = "channels.manage"
	PermissionRolesView              PermissionCode = "roles.view"
	PermissionRolesManage            PermissionCode = "roles.manage"
	PermissionOrganizationView       PermissionCode = "organization.view"
	PermissionOrganizationManage     PermissionCode = "organization.manage"
)

// PermissionResource 定义权限所属功能。
type PermissionResource string

const (
	PermissionResourceExternalContacts PermissionResource = "external_contacts"
	PermissionResourceTeamMembers      PermissionResource = "team_members"
	PermissionResourceChannels         PermissionResource = "channels"
	PermissionResourceRoles            PermissionResource = "roles"
	PermissionResourceOrganization     PermissionResource = "organization"
)

// PermissionLevel 定义权限操作层级。
type PermissionLevel string

const (
	PermissionLevelView   PermissionLevel = "view"
	PermissionLevelManage PermissionLevel = "manage"
)

// PermissionDefinition 描述一项预定义权限。
type PermissionDefinition struct {
	Code     PermissionCode
	Resource PermissionResource
	Level    PermissionLevel
}

var permissionDefinitions = []PermissionDefinition{
	{Code: PermissionExternalContactsView, Resource: PermissionResourceExternalContacts, Level: PermissionLevelView},
	{Code: PermissionExternalContactsManage, Resource: PermissionResourceExternalContacts, Level: PermissionLevelManage},
	{Code: PermissionTeamMembersView, Resource: PermissionResourceTeamMembers, Level: PermissionLevelView},
	{Code: PermissionTeamMembersManage, Resource: PermissionResourceTeamMembers, Level: PermissionLevelManage},
	{Code: PermissionChannelsView, Resource: PermissionResourceChannels, Level: PermissionLevelView},
	{Code: PermissionChannelsManage, Resource: PermissionResourceChannels, Level: PermissionLevelManage},
	{Code: PermissionRolesView, Resource: PermissionResourceRoles, Level: PermissionLevelView},
	{Code: PermissionRolesManage, Resource: PermissionResourceRoles, Level: PermissionLevelManage},
	{Code: PermissionOrganizationView, Resource: PermissionResourceOrganization, Level: PermissionLevelView},
	{Code: PermissionOrganizationManage, Resource: PermissionResourceOrganization, Level: PermissionLevelManage},
}

// PermissionDefinitions 返回按界面顺序排列的权限目录。
func PermissionDefinitions() []PermissionDefinition {
	return append([]PermissionDefinition(nil), permissionDefinitions...)
}

// IsPermissionCode 判断权限代码是否受支持。
func IsPermissionCode(code PermissionCode) bool {
	for _, definition := range permissionDefinitions {
		if definition.Code == code {
			return true
		}
	}
	return false
}

// PermissionViewDependency 返回管理权限依赖的查看权限。
func PermissionViewDependency(code PermissionCode) (PermissionCode, bool) {
	for _, definition := range permissionDefinitions {
		if definition.Code != code || definition.Level != PermissionLevelManage {
			continue
		}
		for _, candidate := range permissionDefinitions {
			if candidate.Resource == definition.Resource && candidate.Level == PermissionLevelView {
				return candidate.Code, true
			}
		}
	}
	return "", false
}
