//go:build server

package device

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListDevicesQuery 查询当前用户的设备。
type ListDevicesQuery struct {
	db *bun.DB
}

// NewListDevicesQuery 创建设备列表查询。
func NewListDevicesQuery(db *bun.DB) *ListDevicesQuery {
	return &ListDevicesQuery{db: db}
}

// Execute 返回当前用户名下未撤销的设备，按注册时间排列。
func (q *ListDevicesQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Record, error) {
	records := make([]servermodels.Device, 0)
	if err := q.db.NewSelect().
		Model(&records).
		Where("d.organization_id = ?", identity.Organization.ID).
		Where("d.user_id = ?", identity.User.ID).
		Where("d.revoked_at IS NULL").
		Order("d.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	output := make([]Record, 0, len(records))
	for _, record := range records {
		output = append(output, recordFromModel(record))
	}
	return output, nil
}
