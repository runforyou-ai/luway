package domain

// RoleKind 定义内置角色和自定义角色。
type RoleKind string

const (
	RoleKindAdmin           RoleKind = "admin"
	RoleKindCustomerService RoleKind = "customer_service"
	RoleKindMember          RoleKind = "member"
	RoleKindCustom          RoleKind = "custom"
)

// BuiltInRoleKinds 返回内置角色的固定顺序。
func BuiltInRoleKinds() []RoleKind {
	return []RoleKind{RoleKindAdmin, RoleKindCustomerService, RoleKindMember}
}

// DefaultRolePermissions 返回内置角色的默认权限。
func DefaultRolePermissions(kind RoleKind) []PermissionCode {
	switch kind {
	case RoleKindAdmin:
		definitions := PermissionDefinitions()
		permissions := make([]PermissionCode, 0, len(definitions))
		for _, definition := range definitions {
			permissions = append(permissions, definition.Code)
		}
		return permissions
	case RoleKindCustomerService:
		return []PermissionCode{
			PermissionExternalContactsView,
			PermissionExternalContactsManage,
			PermissionTeamMembersView,
			PermissionChannelsView,
		}
	case RoleKindMember:
		return []PermissionCode{PermissionTeamMembersView}
	default:
		return nil
	}
}
