//go:build server

package computer

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListComputersQuery 查询当前成员的个人电脑或工作区电脑。
type ListComputersQuery struct {
	db *bun.DB
}

// NewListComputersQuery 创建电脑列表查询。
func NewListComputersQuery(db *bun.DB) *ListComputersQuery {
	return &ListComputersQuery{db: db}
}

// Execute 返回未撤销的指定类型电脑及使用它的 AI 员工数，按添加时间排列；个人电脑只返回当前成员名下的电脑。
func (q *ListComputersQuery) Execute(ctx context.Context, identity *servermodels.Identity, kind domain.ComputerKind) ([]Record, error) {
	rows := make([]struct {
		servermodels.Computer `bun:",extend"`
		AgentCount            int  `bun:"agent_count"`
		Online                bool `bun:"online"`
	}, 0)
	query := q.db.NewSelect().
		Model(&rows).
		ColumnExpr("cmp.*").
		ColumnExpr("(SELECT count(*) FROM agents AS a WHERE a.workspace_id = cmp.workspace_id AND a.computer_id = cmp.id) AS agent_count").
		ColumnExpr(servermodels.ComputerOnlineExpr("cmp")+" AS online").
		Where("cmp.workspace_id = ?", identity.Workspace.ID).
		Where("cmp.kind = ?", kind).
		Where("cmp.revoked_at IS NULL").
		Order("cmp.created_at ASC")
	if kind == domain.ComputerKindPersonal {
		query = query.Where("cmp.owner_user_id = ?", identity.User.ID)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("list computers: %w", err)
	}
	output := make([]Record, 0, len(rows))
	for _, row := range rows {
		output = append(output, recordFromModel(row.Computer, row.AgentCount, row.Online))
	}
	return output, nil
}
