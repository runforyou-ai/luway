//go:build server

// Package role 实现企业角色和权限配置的查询与操作。
package role

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// Input 定义角色可编辑字段。
type Input struct {
	Name        string
	Description string
	Permissions []domain.PermissionCode
}

// AssignmentInput 定义一个成员的目标角色。
type AssignmentInput struct {
	IdentityID string
	RoleID     string
}

// Record 定义角色及其权限。
type Record struct {
	ID             string
	OrganizationID string
	Kind           domain.RoleKind
	Name           string
	Description    string
	Permissions    []domain.PermissionCode
	MemberCount    int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ListOutput 定义角色列表和预定义权限目录。
type ListOutput struct {
	Roles       []Record
	Permissions []domain.PermissionDefinition
}
