//go:build server

package team

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// Summary 定义企业身份所属团队的精简字段。
type Summary struct {
	ID   string `bun:"id"`
	Name string `bun:"name"`
}

// LoadIdentityTeams 读取一个企业身份所属的全部团队。
func LoadIdentityTeams(ctx context.Context, db bun.IDB, workspaceID, identityID string) ([]Summary, error) {
	grouped, err := LoadTeamsByIdentity(ctx, db, workspaceID, []string{identityID})
	if err != nil {
		return nil, err
	}
	return grouped[identityID], nil
}

// LoadTeamsByIdentity 一次查询多个企业身份所属团队，按身份编号分组返回。
func LoadTeamsByIdentity(ctx context.Context, db bun.IDB, workspaceID string, identityIDs []string) (map[string][]Summary, error) {
	grouped := make(map[string][]Summary, len(identityIDs))
	for _, identityID := range identityIDs {
		grouped[identityID] = make([]Summary, 0)
	}
	if len(identityIDs) == 0 {
		return grouped, nil
	}
	rows := make([]struct {
		IdentityID string `bun:"identity_id"`
		ID         string `bun:"id"`
		Name       string `bun:"name"`
	}, 0)
	err := db.NewSelect().TableExpr("team_members AS tm").
		ColumnExpr("tm.identity_id::text AS identity_id, t.id::text AS id, t.name").
		Join("JOIN teams AS t ON t.id = tm.team_id AND t.workspace_id = tm.workspace_id").
		Where("tm.workspace_id = ?", workspaceID).
		Where("tm.identity_id IN (?)", bun.List(identityIDs)).
		OrderExpr("lower(t.name) ASC, t.id ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		grouped[row.IdentityID] = append(grouped[row.IdentityID], Summary{ID: row.ID, Name: row.Name})
	}
	return grouped, nil
}

// ReplaceIdentityTeams 按差集替换企业身份的团队关系，关系实际增减时通知企业全部网站访客重新读取接待状态；调用方须持有 teamIDs 对应团队的共享锁，并处于 realtime.RunInTx 内。
func ReplaceIdentityTeams(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, workspaceIdentityID string, teamIDs []string) error {
	remove := tx.NewDelete().Model((*servermodels.TeamMember)(nil)).
		Where("workspace_id = ?", identity.Workspace.ID).
		Where("identity_id = ?", workspaceIdentityID)
	if len(teamIDs) > 0 {
		remove = remove.Where("team_id NOT IN (?)", bun.List(teamIDs))
	}
	removed, err := remove.Exec(ctx)
	if err != nil {
		return err
	}
	changed, err := removed.RowsAffected()
	if err != nil {
		return err
	}
	if len(teamIDs) > 0 {
		relations := arr.Map(teamIDs, func(teamID string) servermodels.TeamMember {
			return servermodels.TeamMember{WorkspaceID: identity.Workspace.ID, TeamID: teamID, IdentityID: workspaceIdentityID, CreatedByUserID: identity.User.ID}
		})
		inserted, err := tx.NewInsert().Model(&relations).
			Column("workspace_id", "team_id", "identity_id", "created_by_user_id").
			On("CONFLICT (workspace_id, team_id, identity_id) DO NOTHING").
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("insert identity teams: %w", err)
		}
		added, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		changed += added
	}
	// 团队成员变化会改变队列在线情况。
	if changed > 0 {
		realtime.Notify(ctx, realtime.WebsiteReceptionChanged(identity.Workspace.ID))
	}
	return nil
}
