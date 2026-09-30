//go:build server

package servicecategory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Active 按名称顺序返回企业全部未归档的咨询分类，供 AI 转人工时选择。
func Active(ctx context.Context, db bun.IDB, organizationID string) ([]servermodels.ServiceCategory, error) {
	categories := make([]servermodels.ServiceCategory, 0)
	if err := db.NewSelect().Model(&categories).Column("sc.id", "sc.name", "sc.description", "sc.team_id").
		Where("sc.organization_id = ? AND sc.archived_at IS NULL", organizationID).
		OrderExpr("lower(sc.name) ASC, sc.id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load active service categories: %w", err)
	}
	return categories, nil
}

// FindActive 按编号读取企业未归档的咨询分类；不存在或已归档时返回 nil。
func FindActive(ctx context.Context, db bun.IDB, organizationID, categoryID string) (*servermodels.ServiceCategory, error) {
	category := &servermodels.ServiceCategory{}
	err := db.NewSelect().Model(category).Column("sc.id", "sc.name", "sc.team_id").
		Where("sc.organization_id = ? AND sc.id = ? AND sc.archived_at IS NULL", organizationID, categoryID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load service category: %w", err)
	}
	return category, nil
}

// ClearTeam 在团队删除事务中清空指向该团队的咨询分类路由。
func ClearTeam(ctx context.Context, db bun.IDB, organizationID, teamID string) error {
	if _, err := db.NewUpdate().Model((*servermodels.ServiceCategory)(nil)).
		Set("team_id = NULL").
		Set("updated_at = now()").
		Where("organization_id = ? AND team_id = ?", organizationID, teamID).
		Exec(ctx); err != nil {
		return fmt.Errorf("clear service category team: %w", err)
	}
	return nil
}
