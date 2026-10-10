//go:build server

package computer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// CreateWorkspaceComputerAction 在当前工作区添加工作区电脑。
type CreateWorkspaceComputerAction struct {
	db *bun.DB
}

// NewCreateWorkspaceComputerAction 创建工作区电脑添加操作。
func NewCreateWorkspaceComputerAction(db *bun.DB) *CreateWorkspaceComputerAction {
	return &CreateWorkspaceComputerAction{db: db}
}

// Execute 按名称添加工作区电脑并签发电脑凭据，执行器以该凭据连接后上报平台与执行能力。
func (a *CreateWorkspaceComputerAction) Execute(ctx context.Context, identity *servermodels.Identity, name string) (*Registration, error) {
	name = strings.TrimSpace(name)
	credential, credentialHash := random.Token(32)
	computer := servermodels.Computer{
		WorkspaceID:    identity.Workspace.ID,
		Kind:           domain.ComputerKindWorkspace,
		Name:           name,
		CredentialHash: credentialHash,
	}
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		_, err := tx.NewInsert().
			Model(&computer).
			Column("workspace_id", "kind", "name", "credential_hash").
			Returning("*").
			Exec(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("create workspace computer: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "工作区电脑已添加", "user_id", identity.User.ID, "computer_id", computer.ID)
	// 新签发的凭据尚未报告 HTTP 心跳，电脑记为离线。
	return &Registration{Record: recordFromModel(computer, 0, false), Credential: credential}, nil
}

// ResetComputerCredentialAction 重置工作区电脑的凭据。
type ResetComputerCredentialAction struct {
	db      *bun.DB
	settler ToolCallSettler
}

// NewResetComputerCredentialAction 创建工作区电脑凭据重置操作，settler 在结算派发给它的调用后唤醒等待结果的一方。
func NewResetComputerCredentialAction(db *bun.DB, settler ToolCallSettler) *ResetComputerCredentialAction {
	return &ResetComputerCredentialAction{db: db, settler: settler}
}

// Execute 为未撤销的工作区电脑签发新凭据、记为离线并强制关闭旧凭据的实时连接，旧凭据立即失效；派发给它且未结束的调用随后立即结算。
func (a *ResetComputerCredentialAction) Execute(ctx context.Context, identity *servermodels.Identity, computerID string) (*Registration, error) {
	credential, credentialHash := random.Token(32)
	computer := servermodels.Computer{}
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		err := tx.NewUpdate().
			Model(&computer).
			Set("credential_hash = ?", credentialHash).
			Set("last_seen_at = NULL").
			Where("id = ? AND workspace_id = ? AND kind = ?", computerID, identity.Workspace.ID, domain.ComputerKindWorkspace).
			Where("revoked_at IS NULL").
			Returning("*").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		realtime.Notify(ctx, realtime.ComputerCredentialRevoked(identity.Workspace.ID, computerID))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reset computer credential: %w", err)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, identity.Workspace.ID), "工作区电脑凭据已重置", "user_id", identity.User.ID, "computer_id", computerID)
	if err := settleMatchingCalls(ctx, a.db, a.settler, false, "cmp.id = ?", computerID); err != nil {
		return nil, err
	}
	agentCount, err := a.db.NewSelect().Model((*servermodels.Agent)(nil)).
		Where("workspace_id = ? AND computer_id = ?", identity.Workspace.ID, computerID).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count computer agents: %w", err)
	}
	// 新签发的凭据尚未报告 HTTP 心跳，电脑记为离线。
	return &Registration{Record: recordFromModel(computer, int(agentCount), false), Credential: credential}, nil
}
