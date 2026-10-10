//go:build server

package computer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// RegisterComputerAction 把执行器所在电脑注册为当前成员的个人电脑。
type RegisterComputerAction struct {
	db *bun.DB
}

// NewRegisterComputerAction 创建电脑注册操作。
func NewRegisterComputerAction(db *bun.DB) *RegisterComputerAction {
	return &RegisterComputerAction{db: db}
}

// Execute 按安装标识注册或更新当前成员的个人电脑并签发新的电脑凭据；同一安装重新注册时旧凭据失效、撤销标记清除。
func (a *RegisterComputerAction) Execute(ctx context.Context, identity *servermodels.Identity, input RegisterInput) (*Registration, error) {
	input.InstallID, input.Name = strings.TrimSpace(input.InstallID), strings.TrimSpace(input.Name)
	credential, credentialHash := random.Token(32)
	computer := servermodels.Computer{
		WorkspaceID:    identity.Workspace.ID,
		Kind:           domain.ComputerKindPersonal,
		OwnerUserID:    &identity.User.ID,
		InstallID:      &input.InstallID,
		Name:           input.Name,
		CredentialHash: credentialHash,
	}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		// 成员行锁串行化同一成员的注册，已有安装在凭据更新后撤销旧连接。
		existing, err := tx.NewSelect().Model((*servermodels.Computer)(nil)).
			Where("workspace_id = ? AND owner_user_id = ? AND install_id = ? AND kind = ?", identity.Workspace.ID, identity.User.ID, input.InstallID, domain.ComputerKindPersonal).
			Exists(ctx)
		if err != nil {
			return err
		}
		_, err = tx.NewInsert().
			Model(&computer).
			Column("workspace_id", "kind", "owner_user_id", "install_id", "name", "credential_hash").
			On("CONFLICT (workspace_id, owner_user_id, install_id) WHERE kind = 'personal' DO UPDATE").
			Set("name = EXCLUDED.name").
			Set("credential_hash = EXCLUDED.credential_hash").
			Set("revoked_at = NULL").
			Returning("*").
			Exec(ctx)
		if err != nil {
			return err
		}
		if existing {
			realtime.Notify(ctx, realtime.ComputerCredentialRevoked(identity.Workspace.ID, computer.ID))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("register computer: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "电脑注册成功", "user_id", identity.User.ID, "computer_id", computer.ID)
	// 新签发的凭据尚未报告 HTTP 心跳，注册结果标为离线。
	return &Registration{Record: recordFromModel(computer, 0, false), Credential: credential}, nil
}
