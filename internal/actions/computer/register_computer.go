//go:build server

package computer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/token"
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
	input, fields := normalizeRegisterInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	credential, err := token.Issue(0)
	if err != nil {
		return nil, fmt.Errorf("issue computer credential: %w", err)
	}
	computer := servermodels.Computer{
		OrganizationID: identity.Organization.ID,
		Kind:           domain.ComputerKindPersonal,
		OwnerUserID:    &identity.User.ID,
		InstallID:      input.InstallID,
		Name:           input.Name,
		Platform:       input.Platform,
		CredentialHash: credential.TokenHash,
	}
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		_, err := tx.NewInsert().
			Model(&computer).
			Column("organization_id", "kind", "owner_user_id", "install_id", "name", "platform", "credential_hash").
			On("CONFLICT (organization_id, owner_user_id, install_id) DO UPDATE").
			Set("name = EXCLUDED.name").
			Set("platform = EXCLUDED.platform").
			Set("credential_hash = EXCLUDED.credential_hash").
			Set("revoked_at = NULL").
			Set("updated_at = now()").
			Returning("*").
			Exec(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("register computer: %w", err)
	}
	slog.Info("电脑注册成功", "organization_id", identity.Organization.ID, "user_id", identity.User.ID, "computer_id", computer.ID, "platform", computer.Platform)
	return &Registration{Record: recordFromModel(computer, time.Now()), Credential: credential.Token}, nil
}
