//go:build server

package role

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// ListRolesQuery 查询当前企业的角色。
type ListRolesQuery struct {
	db *bun.DB
}

// NewListRolesQuery 创建角色列表查询。
func NewListRolesQuery(db *bun.DB) *ListRolesQuery {
	return &ListRolesQuery{db: db}
}

// Execute 返回角色和预定义权限目录。
func (q *ListRolesQuery) Execute(ctx context.Context, identity *servermodels.Identity) (ListOutput, error) {
	roles := make([]servermodels.Role, 0)
	if err := q.db.NewSelect().
		Model(&roles).
		Where("r.organization_id = ?", identity.Organization.ID).
		// 内置角色按固定顺序在前，自定义角色按创建时间在后。
		OrderExpr("array_position(?::text[], r.kind) ASC NULLS LAST", pgdialect.Array(domain.BuiltInRoleKinds())).
		Order("r.created_at ASC").
		Scan(ctx); err != nil {
		return ListOutput{}, fmt.Errorf("list roles: %w", err)
	}
	roleIDs := make([]string, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}
	permissions, err := loadPermissions(ctx, q.db, identity.Organization.ID, roleIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("list role permissions: %w", err)
	}
	memberCounts, err := loadMemberCounts(ctx, q.db, identity.Organization.ID, roleIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("count role members: %w", err)
	}
	output := ListOutput{Roles: make([]Record, 0, len(roles)), Permissions: domain.PermissionDefinitions()}
	for _, role := range roles {
		output.Roles = append(output.Roles, recordFromModel(role, permissions[role.ID], memberCounts[role.ID]))
	}
	return output, nil
}
