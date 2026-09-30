//go:build server

package role

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetRoleQuery 查询当前企业的指定角色。
type GetRoleQuery struct {
	db *bun.DB
}

// NewGetRoleQuery 创建角色详情查询。
func NewGetRoleQuery(db *bun.DB) *GetRoleQuery {
	return &GetRoleQuery{db: db}
}

// Execute 返回指定角色及其权限。
func (q *GetRoleQuery) Execute(ctx context.Context, identity *servermodels.Identity, roleID string) (*Record, error) {
	role, err := loadRole(ctx, q.db, identity.Organization.ID, roleID, false)
	if err != nil {
		return nil, err
	}
	permissions, err := loadPermissions(ctx, q.db, identity.Organization.ID, []string{role.ID})
	if err != nil {
		return nil, fmt.Errorf("get role permissions: %w", err)
	}
	memberCounts, err := loadMemberCounts(ctx, q.db, identity.Organization.ID, []string{role.ID})
	if err != nil {
		return nil, fmt.Errorf("count role members: %w", err)
	}
	output := recordFromModel(*role, permissions[role.ID], memberCounts[role.ID])
	return &output, nil
}
