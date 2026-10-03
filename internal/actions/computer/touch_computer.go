//go:build server

package computer

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TouchComputerAction 记录执行器事件流在线。
type TouchComputerAction struct {
	db *bun.DB
}

// NewTouchComputerAction 创建在线记录操作。
func NewTouchComputerAction(db *bun.DB) *TouchComputerAction {
	return &TouchComputerAction{db: db}
}

// Execute 把电脑最近在线时间记为当前时间，电脑已撤销时返回 ErrCredentialInvalid。
func (a *TouchComputerAction) Execute(ctx context.Context, computer Identity) error {
	result, err := a.db.NewUpdate().Model((*servermodels.Computer)(nil)).
		Set("last_seen_at = now()").
		Where("id = ? AND organization_id = ? AND revoked_at IS NULL", computer.ComputerID, computer.OrganizationID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("touch computer: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("touch computer: %w", err)
	} else if affected == 0 {
		return ErrCredentialInvalid
	}
	return nil
}
