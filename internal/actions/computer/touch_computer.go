//go:build server

package computer

import (
	"context"
	"fmt"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/random"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// TouchComputerAction 记录以当前凭据完成的执行器 HTTP 心跳。
type TouchComputerAction struct {
	db *bun.DB
}

// NewTouchComputerAction 创建在线记录操作。
func NewTouchComputerAction(db *bun.DB) *TouchComputerAction {
	return &TouchComputerAction{db: db}
}

// Execute 把电脑最近在线时间记为当前时间，电脑已撤销时返回 ErrCredentialInvalid。
func (a *TouchComputerAction) Execute(ctx context.Context, computer Identity, credential string) error {
	result, err := a.db.NewUpdate().Model((*servermodels.Computer)(nil)).
		Set("last_seen_at = now()").
		Where("id = ? AND workspace_id = ? AND revoked_at IS NULL", computer.ComputerID, computer.WorkspaceID).
		Where("credential_hash = ?", random.HashToken(credential)).
		Where("EXISTS (SELECT 1 FROM workspaces w WHERE w.id = cmp.workspace_id AND w.lifecycle_status = ?)", domain.WorkspaceLifecycleActive).
		Where("cmp.kind = ? OR EXISTS (SELECT 1 FROM users u JOIN accounts a ON a.id = u.account_id WHERE u.id = cmp.owner_user_id AND u.status = ? AND a.status = ?)", domain.ComputerKindWorkspace, domain.IdentityStatusActive, domain.AccountStatusActive).
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
