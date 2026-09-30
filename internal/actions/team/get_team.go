//go:build server

package team

import (
	"context"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetTeamQuery 读取当前企业的单个团队。
type GetTeamQuery struct{ db *bun.DB }

// NewGetTeamQuery 创建团队详情查询。
func NewGetTeamQuery(db *bun.DB) *GetTeamQuery { return &GetTeamQuery{db: db} }

// Execute 返回团队及其活跃成员数，团队不属于当前企业时返回 ErrNotFound。
func (q *GetTeamQuery) Execute(ctx context.Context, identity *servermodels.Identity, teamID string) (TeamRecord, error) {
	team, err := loadTeam(ctx, q.db, identity.Organization.ID, teamID)
	if err != nil {
		return TeamRecord{}, err
	}
	return *team, nil
}
