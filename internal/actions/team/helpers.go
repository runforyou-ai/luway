//go:build server

package team

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/uptrace/bun"
)

// withActiveMemberCount 补充团队中账号正常的企业成员和 AI 员工数量。
func withActiveMemberCount(query *bun.SelectQuery) *bun.SelectQuery {
	members := query.DB().NewSelect().TableExpr("team_members AS tm").ColumnExpr("count(*)").
		Join("JOIN workspace_identities AS oi ON oi.id = tm.identity_id AND oi.workspace_id = tm.workspace_id").
		Where("tm.workspace_id = t.workspace_id AND tm.team_id = t.id").
		Apply(identityaction.ApplyActiveMemberConditions)
	return query.ColumnExpr("(?) AS member_count", members)
}

// lockTeam 对当前企业中的团队行取 FOR UPDATE。
func lockTeam(ctx context.Context, db bun.IDB, workspaceID, teamID string) error {
	var lockedID string
	err := db.NewSelect().TableExpr("teams AS t").Column("id").
		Where("t.workspace_id = ? AND t.id = ?", workspaceID, teamID).
		For("UPDATE").
		Scan(ctx, &lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// LockTeams 按编号锁定同企业的全部指定团队，并按名称返回摘要；teamIDs 须已规范化并去重。
func LockTeams(ctx context.Context, db bun.IDB, workspaceID string, teamIDs []string) ([]Summary, error) {
	teams := make([]Summary, 0, len(teamIDs))
	if len(teamIDs) == 0 {
		return teams, nil
	}
	if err := db.NewSelect().TableExpr("teams AS t").
		ColumnExpr("t.id::text, t.name").
		Where("t.workspace_id = ? AND t.id IN (?)", workspaceID, bun.List(teamIDs)).
		OrderExpr("t.id ASC").For("KEY SHARE").Scan(ctx, &teams); err != nil {
		return nil, err
	}
	if len(teams) != len(teamIDs) {
		return nil, ErrNotFound
	}
	// 展示顺序按名称排列，同名团队保持编号顺序。
	slices.SortStableFunc(teams, func(left, right Summary) int {
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	})
	return teams, nil
}

// requireTeam 校验团队属于当前企业。
func requireTeam(ctx context.Context, db bun.IDB, workspaceID, teamID string) error {
	exists, err := db.NewSelect().TableExpr("teams AS t").
		Where("t.workspace_id = ? AND t.id = ?", workspaceID, teamID).Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// loadTeam 读取当前企业中的团队。
func loadTeam(ctx context.Context, db bun.IDB, workspaceID, teamID string) (*TeamRecord, error) {
	record := &TeamRecord{}
	query := db.NewSelect().
		TableExpr("teams AS t").
		ColumnExpr("t.id::text AS id").
		Column("name", "description", "created_at", "updated_at")
	err := withActiveMemberCount(query).
		Where("t.workspace_id = ?", workspaceID).
		Where("t.id = ?", teamID).
		Scan(ctx, record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return record, err
}
