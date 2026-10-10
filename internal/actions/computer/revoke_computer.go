//go:build server

package computer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/localagent"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// RevokeComputerAction 撤销当前成员的个人电脑或工作区电脑。
type RevokeComputerAction struct {
	db      *bun.DB
	settler ToolCallSettler
}

// NewRevokeComputerAction 创建电脑撤销操作，settler 在结算派发给它的调用后唤醒等待结果的一方。
func NewRevokeComputerAction(db *bun.DB, settler ToolCallSettler) *RevokeComputerAction {
	return &RevokeComputerAction{db: db, settler: settler}
}

// Execute 撤销当前成员名下的个人电脑或工作区电脑并结束其事件流，工作区电脑须成员角色授予工作区管理权限，随后结算派发给它且未结束的调用并释放其上的本机 Agent 会话；电脑记录保留；个人电脑在该安装再次注册前凭据失效；工作区电脑在同一事务中解除 AI 员工的绑定。
func (a *RevokeComputerAction) Execute(ctx context.Context, identity *servermodels.Identity, computerID string) error {
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var kind domain.ComputerKind
		err := tx.NewUpdate().
			Model((*servermodels.Computer)(nil)).
			Set("revoked_at = now()").
			Where("id = ? AND workspace_id = ?", computerID, identity.Workspace.ID).
			Where("(kind = ? AND ?) OR owner_user_id = ?", domain.ComputerKindWorkspace, identity.HasPermission(domain.PermissionWorkspaceManage), identity.User.ID).
			Where("revoked_at IS NULL").
			Returning("kind").
			Scan(ctx, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if kind == domain.ComputerKindWorkspace {
			if _, err := tx.NewUpdate().Model((*servermodels.Agent)(nil)).
				Set("computer_id = NULL").
				Set("computer_grant = NULL").
				Set("local_agents = '[]'::jsonb").
				Where("workspace_id = ? AND computer_id = ?", identity.Workspace.ID, computerID).
				Exec(ctx); err != nil {
				return err
			}
		}
		realtime.Notify(ctx, realtime.ComputerCredentialRevoked(identity.Workspace.ID, computerID))
		return nil
	})
	if err != nil {
		return fmt.Errorf("revoke computer: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "电脑已撤销", "user_id", identity.User.ID, "computer_id", computerID)
	if err := settleMatchingCalls(ctx, a.db, a.settler, false, "cmp.id = ? AND cmp.revoked_at IS NOT NULL", computerID); err != nil {
		return err
	}
	// 结算派发给它的调用后释放这台电脑上的本机 Agent 会话。
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		return localagent.ReleaseWhere(ctx, tx, identity.Workspace.ID, "las.computer_id = ?", computerID)
	})
}
