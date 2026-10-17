package appservice

import (
	"github.com/runforyou-ai/luway/internal/apptypes"
	"github.com/runforyou-ai/luway/internal/domain"
)

// RoleKind 表示角色类型。
type RoleKind = domain.RoleKind

// PermissionCode 表示一项预定义权限。
type PermissionCode = domain.PermissionCode

// Role 定义企业角色及其权限。
type Role = apptypes.Role

// RoleSummary 定义成员关联角色的精简字段。
type RoleSummary = apptypes.RoleSummary

// RoleOption 定义成员表单与筛选使用的角色选项，Assignable 表示当前成员可以把成员调整到该角色。
type RoleOption = apptypes.RoleOption

// RoleOptionList 定义当前企业的角色选项。
type RoleOptionList = apptypes.RoleOptionList

// RoleList 定义角色、数量上限和权限目录。
type RoleList = apptypes.RoleList

// RoleInput 定义角色可编辑字段。
type RoleInput = apptypes.RoleInput

// RoleAssignmentInput 定义一个成员的目标角色。
type RoleAssignmentInput = apptypes.RoleAssignmentInput

// RoleAssignmentsInput 定义一次批量角色调整。
type RoleAssignmentsInput = apptypes.RoleAssignmentsInput
