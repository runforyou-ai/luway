package domain

import (
	"github.com/runforyou-ai/support/arr"
)

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

// DefaultRolePermissions 返回内置角色的默认权限；管理员固定拥有全部权限。
func DefaultRolePermissions(kind RoleKind) []PermissionCode {
	switch kind {
	case RoleKindAdmin:
		return arr.Map(PermissionDefinitions(), func(definition PermissionDefinition) PermissionCode { return definition.Code })
	case RoleKindCustomerService:
		return []PermissionCode{PermissionExternalContactsManage}
	default:
		return nil
	}
}

// EffectiveRolePermissions 按权限目录顺序返回角色实际拥有的权限：管理员为全部权限，其余角色为已分配的权限。
func EffectiveRolePermissions(kind RoleKind, granted []PermissionCode) []PermissionCode {
	return arr.OrEmpty(arr.FilterMap(permissionDefinitions, func(definition PermissionDefinition) (PermissionCode, bool) {
		return definition.Code, RoleGrants(kind, granted, definition.Code)
	}))
}

// RoleGrants 判断角色是否授予指定权限：管理员拥有全部权限，其余角色按已分配的权限判断。
func RoleGrants(kind RoleKind, granted []PermissionCode, code PermissionCode) bool {
	if kind == RoleKindAdmin {
		return true
	}
	for _, permission := range granted {
		if permission == code {
			return true
		}
	}
	return false
}
