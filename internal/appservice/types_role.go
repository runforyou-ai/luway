package appservice

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// RoleKind 表示角色类型。
type RoleKind string

const (
	RoleKindAdmin           RoleKind = RoleKind(domain.RoleKindAdmin)
	RoleKindCustomerService RoleKind = RoleKind(domain.RoleKindCustomerService)
	RoleKindMember          RoleKind = RoleKind(domain.RoleKindMember)
	RoleKindCustom          RoleKind = RoleKind(domain.RoleKindCustom)
)

// PermissionCode 表示一项预定义权限。
type PermissionCode string

const (
	PermissionExternalContactsView   PermissionCode = PermissionCode(domain.PermissionExternalContactsView)
	PermissionExternalContactsManage PermissionCode = PermissionCode(domain.PermissionExternalContactsManage)
	PermissionTeamMembersView        PermissionCode = PermissionCode(domain.PermissionTeamMembersView)
	PermissionTeamMembersManage      PermissionCode = PermissionCode(domain.PermissionTeamMembersManage)
	PermissionChannelsView           PermissionCode = PermissionCode(domain.PermissionChannelsView)
	PermissionChannelsManage         PermissionCode = PermissionCode(domain.PermissionChannelsManage)
	PermissionRolesView              PermissionCode = PermissionCode(domain.PermissionRolesView)
	PermissionRolesManage            PermissionCode = PermissionCode(domain.PermissionRolesManage)
	PermissionOrganizationView       PermissionCode = PermissionCode(domain.PermissionOrganizationView)
	PermissionOrganizationManage     PermissionCode = PermissionCode(domain.PermissionOrganizationManage)
)

// PermissionResource 表示权限所属功能。
type PermissionResource string

const (
	PermissionResourceExternalContacts PermissionResource = PermissionResource(domain.PermissionResourceExternalContacts)
	PermissionResourceTeamMembers      PermissionResource = PermissionResource(domain.PermissionResourceTeamMembers)
	PermissionResourceChannels         PermissionResource = PermissionResource(domain.PermissionResourceChannels)
	PermissionResourceRoles            PermissionResource = PermissionResource(domain.PermissionResourceRoles)
	PermissionResourceOrganization     PermissionResource = PermissionResource(domain.PermissionResourceOrganization)
)

// PermissionLevel 表示权限操作层级。
type PermissionLevel string

const (
	PermissionLevelView   PermissionLevel = PermissionLevel(domain.PermissionLevelView)
	PermissionLevelManage PermissionLevel = PermissionLevel(domain.PermissionLevelManage)
)

// PermissionDefinition 定义权限目录中的一项权限。
type PermissionDefinition struct {
	Code     PermissionCode     `json:"code"`
	Resource PermissionResource `json:"resource"`
	Level    PermissionLevel    `json:"level"`
}

// Role 定义企业角色及其权限。
type Role struct {
	ID          string           `json:"id"`
	Kind        RoleKind         `json:"kind"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Permissions []PermissionCode `json:"permissions"`
	MemberCount int              `json:"memberCount"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

// RoleSummary 定义成员关联角色的精简字段。
type RoleSummary struct {
	ID   string   `json:"id"`
	Kind RoleKind `json:"kind"`
	Name string   `json:"name"`
}

// RoleList 定义角色、数量上限和权限目录。
type RoleList struct {
	Roles       []Role                 `json:"roles"`
	Permissions []PermissionDefinition `json:"permissions"`
	Maximum     int                    `json:"maximum"`
}

// RoleInput 定义角色可编辑字段。
type RoleInput struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Permissions []PermissionCode `json:"permissions"`
}

// RoleAssignmentInput 定义一个成员的目标角色。
type RoleAssignmentInput struct {
	IdentityID string `json:"identityId"`
	RoleID     string `json:"roleId"`
}

// RoleAssignmentsInput 定义一次批量角色调整。
type RoleAssignmentsInput struct {
	Assignments []RoleAssignmentInput `json:"assignments"`
}
