package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// RoleKind 表示角色类型。
type RoleKind = domain.RoleKind

// PermissionCode 表示一项预定义权限。
type PermissionCode = domain.PermissionCode

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

// RoleOption 定义成员表单与筛选使用的角色选项，Assignable 表示当前成员可以把成员调整到该角色。
type RoleOption struct {
	ID         string   `json:"id"`
	Kind       RoleKind `json:"kind"`
	Name       string   `json:"name"`
	Assignable bool     `json:"assignable"`
}

// RoleOptionList 定义当前企业的角色选项。
type RoleOptionList struct {
	Roles []RoleOption `json:"roles"`
}

// RoleList 定义角色、数量上限和权限目录。
type RoleList struct {
	Roles       []Role           `json:"roles"`
	Permissions []PermissionCode `json:"permissions"`
	Maximum     int              `json:"maximum"`
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
