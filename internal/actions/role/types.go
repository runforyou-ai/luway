//go:build server

// Package role 实现企业角色和权限配置的查询与操作。
package role

import (
	"time"

	"github.com/runforyou-ai/luway/internal/apptypes"
	"github.com/runforyou-ai/luway/internal/domain"
)

// Input 是角色可编辑字段，与契约共用形状。
type Input = apptypes.RoleInput

// AssignmentInput 是一个成员的目标角色，与契约共用形状。
type AssignmentInput = apptypes.RoleAssignmentInput

// Option 是成员表单与筛选使用的角色选项，与契约共用形状。
type Option = apptypes.RoleOption

// Record 是 Action 内部角色视图；含 WorkspaceID，不得直接作为 API 输出。
type Record struct {
	ID          string
	WorkspaceID string
	Kind        domain.RoleKind
	Name        string
	Description string
	Permissions []domain.PermissionCode
	MemberCount int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ListOutput 定义角色列表和预定义权限目录。
type ListOutput struct {
	Roles       []Record
	Permissions []domain.PermissionDefinition
}

// ToRole 转为对外 API 形状，去掉内部字段。
func (r Record) ToRole() apptypes.Role {
	permissions := append(make([]domain.PermissionCode, 0, len(r.Permissions)), r.Permissions...)
	return apptypes.Role{
		ID: r.ID, Kind: r.Kind, Name: r.Name, Description: r.Description,
		Permissions: permissions, MemberCount: r.MemberCount,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
