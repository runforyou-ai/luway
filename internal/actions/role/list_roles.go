//go:build server

package role

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
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
		Where("r.workspace_id = ?", identity.Workspace.ID).
		// 内置角色按固定顺序在前，自定义角色按创建时间在后。
		OrderExpr("array_position(?::text[], r.kind) ASC NULLS LAST", pgdialect.Array(domain.BuiltInRoleKinds())).
		Order("r.created_at ASC").
		Scan(ctx); err != nil {
		return ListOutput{}, fmt.Errorf("list roles: %w", err)
	}
	roleIDs := arr.Map(roles, func(role servermodels.Role) string { return role.ID })
	permissions, err := loadPermissions(ctx, q.db, identity.Workspace.ID, roleIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("list role permissions: %w", err)
	}
	memberCounts, err := loadMemberCounts(ctx, q.db, identity.Workspace.ID, roleIDs)
	if err != nil {
		return ListOutput{}, fmt.Errorf("count role members: %w", err)
	}
	records := arr.Map(roles, func(role servermodels.Role) Record {
		return recordFromModel(role, permissions[role.ID], memberCounts[role.ID])
	})
	return ListOutput{Roles: records, Permissions: domain.PermissionDefinitions()}, nil
}

// ListOptionsQuery 查询当前企业的角色选项。
type ListOptionsQuery struct {
	db *bun.DB
}

// NewListOptionsQuery 创建角色选项查询。
func NewListOptionsQuery(db *bun.DB) *ListOptionsQuery {
	return &ListOptionsQuery{db: db}
}

// Execute 按角色列表顺序返回全部角色选项，并标出操作者可以分配的角色：管理员角色只有管理员可以分配。
func (q *ListOptionsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Option, error) {
	roles := make([]servermodels.Role, 0)
	if err := q.db.NewSelect().
		Model(&roles).
		Column("id", "kind", "name").
		Where("r.workspace_id = ?", identity.Workspace.ID).
		OrderExpr("array_position(?::text[], r.kind) ASC NULLS LAST", pgdialect.Array(domain.BuiltInRoleKinds())).
		Order("r.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list role options: %w", err)
	}
	return arr.Map(roles, func(role servermodels.Role) Option {
		kind := domain.RoleKind(role.Kind)
		return Option{ID: role.ID, Kind: kind, Name: role.Name, Assignable: kind != domain.RoleKindAdmin || identity.Role.Kind == domain.RoleKindAdmin}
	}), nil
}
