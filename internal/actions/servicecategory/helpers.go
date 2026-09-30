//go:build server

package servicecategory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// normalizeInput 规范化并校验咨询分类字段。
func normalizeInput(input Input) (Input, map[string]common.FieldCode) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	fields := make(map[string]common.FieldCode)
	if input.Name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(input.Name) > domain.ServiceCategoryNameMaxLength {
		fields["name"] = ValidationNameTooLong
	}
	if utf8.RuneCountInString(input.Description) > domain.ServiceCategoryDescriptionMaxLength {
		fields["description"] = ValidationDescriptionTooLong
	}
	if input.TeamID != nil && !common.ValidUUID(*input.TeamID) {
		fields["teamId"] = ValidationTeamInvalid
	}
	return input, fields
}

// lockTeam 对分类关联的团队取 FOR KEY SHARE，与团队删除互斥；团队不存在时返回字段错误。
func lockTeam(ctx context.Context, db bun.IDB, organizationID string, teamID *string) error {
	if teamID == nil {
		return nil
	}
	var id string
	err := db.NewSelect().Model((*servermodels.Team)(nil)).Column("t.id").
		Where("t.organization_id = ? AND t.id = ?", organizationID, *teamID).
		For("KEY SHARE").
		Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return &common.FieldError{Fields: map[string]common.FieldCode{"teamId": ValidationTeamInvalid}}
	}
	return err
}

// selectRecords 构造未归档咨询分类及其团队名称的查询。
func selectRecords(db bun.IDB, organizationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("service_categories AS sc").
		ColumnExpr("sc.id::text AS id").
		ColumnExpr("sc.name, sc.description, sc.team_id::text AS team_id, t.name AS team_name, sc.created_at, sc.updated_at").
		Join("LEFT JOIN teams AS t ON t.id = sc.team_id AND t.organization_id = sc.organization_id").
		Where("sc.organization_id = ?", organizationID).
		Where("sc.archived_at IS NULL")
}

// loadRecord 读取当前企业中未归档的咨询分类。
func loadRecord(ctx context.Context, db bun.IDB, organizationID, categoryID string) (*Record, error) {
	record := &Record{}
	err := selectRecords(db, organizationID).Where("sc.id = ?", categoryID).Scan(ctx, record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return record, err
}
