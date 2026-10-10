//go:build server

package servicecategory

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// normalizeInput 去除咨询分类名称与说明的首尾空白，团队编号统一为小写。
func normalizeInput(input Input) Input {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.TeamID != nil {
		teamID, _ := str.NormalizeUUID(*input.TeamID)
		input.TeamID = &teamID
	}
	return input
}

// lockTeam 对分类关联的团队取 FOR KEY SHARE，与团队删除互斥；团队不存在时返回字段错误。
func lockTeam(ctx context.Context, db bun.IDB, workspaceID string, teamID *string) error {
	if teamID == nil {
		return nil
	}
	var id string
	err := db.NewSelect().Model((*servermodels.Team)(nil)).Column("t.id").
		Where("t.workspace_id = ? AND t.id = ?", workspaceID, *teamID).
		For("KEY SHARE").
		Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return &common.FieldError{Fields: map[string]common.FieldCode{"teamId": ValidationTeamInvalid}}
	}
	return err
}

// selectRecords 构造未归档咨询分类及其团队名称的查询。
func selectRecords(db bun.IDB, workspaceID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("service_categories AS sc").
		ColumnExpr("sc.id::text AS id").
		ColumnExpr("sc.name, sc.description, sc.team_id::text AS team_id, t.name AS team_name, sc.created_at, sc.updated_at").
		Join("LEFT JOIN teams AS t ON t.id = sc.team_id AND t.workspace_id = sc.workspace_id").
		Where("sc.workspace_id = ?", workspaceID).
		Where("sc.archived_at IS NULL")
}

// loadRecord 读取当前企业中未归档的咨询分类。
func loadRecord(ctx context.Context, db bun.IDB, workspaceID, categoryID string) (*Record, error) {
	record := &Record{}
	err := selectRecords(db, workspaceID).Where("sc.id = ?", categoryID).Scan(ctx, record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return record, err
}
