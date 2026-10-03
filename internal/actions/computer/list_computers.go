//go:build server

package computer

import (
	"context"
	"fmt"
	"time"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListComputersQuery 查询当前成员的电脑。
type ListComputersQuery struct {
	db *bun.DB
}

// NewListComputersQuery 创建电脑列表查询。
func NewListComputersQuery(db *bun.DB) *ListComputersQuery {
	return &ListComputersQuery{db: db}
}

// Execute 返回当前成员名下未撤销的电脑，按注册时间排列。
func (q *ListComputersQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Record, error) {
	computers := make([]servermodels.Computer, 0)
	if err := q.db.NewSelect().
		Model(&computers).
		Where("cmp.organization_id = ?", identity.Organization.ID).
		Where("cmp.owner_user_id = ?", identity.User.ID).
		Where("cmp.revoked_at IS NULL").
		Order("cmp.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list computers: %w", err)
	}
	now := time.Now()
	output := make([]Record, 0, len(computers))
	for _, computer := range computers {
		output = append(output, recordFromModel(computer, now))
	}
	return output, nil
}
