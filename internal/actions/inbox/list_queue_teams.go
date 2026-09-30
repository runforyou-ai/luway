//go:build server

package inbox

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ServiceQueueTeam 定义可作为客服队列的团队。
type ServiceQueueTeam struct {
	ID        string `bun:"id"`
	Name      string `bun:"name"`
	Mine      bool   `bun:"mine"`
	Available bool   `bun:"available"`
}

// ListServiceQueueTeamsQuery 读取可作为客服队列的团队。
type ListServiceQueueTeamsQuery struct{ db *bun.DB }

// NewListServiceQueueTeamsQuery 创建客服队列团队查询。
func NewListServiceQueueTeamsQuery(db *bun.DB) *ListServiceQueueTeamsQuery {
	return &ListServiceQueueTeamsQuery{db: db}
}

// Execute 返回企业内的全部团队，本人所在团队排在前面。
func (q *ListServiceQueueTeamsQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]ServiceQueueTeam, error) {
	mine := q.db.NewSelect().TableExpr("team_members AS mine_tm").ColumnExpr("1").
		Where("mine_tm.organization_id = t.organization_id AND mine_tm.team_id = t.id").
		Where("mine_tm.identity_id = ?", identity.OrganizationIdentity.ID)
	available := identityaction.TeamServiceHandlerQuery(q.db).
		Where("oi.organization_id = t.organization_id AND tm.team_id = t.id")
	teams := make([]ServiceQueueTeam, 0)
	err := q.db.NewSelect().TableExpr("teams AS t").
		ColumnExpr("t.id::text AS id, t.name").
		ColumnExpr("EXISTS (?) AS mine", mine).
		ColumnExpr("EXISTS (?) AS available", available).
		Where("t.organization_id = ?", identity.Organization.ID).
		OrderExpr("mine DESC, lower(t.name) ASC, t.id ASC").
		Scan(ctx, &teams)
	if err != nil {
		return nil, fmt.Errorf("list service queue teams: %w", err)
	}
	return teams, nil
}
