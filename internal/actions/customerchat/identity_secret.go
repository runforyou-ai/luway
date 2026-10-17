//go:build server

package customerchat

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// LoadCustomerIdentitySecret 读取企业客户身份密钥，未生成时返回空串。
func LoadCustomerIdentitySecret(ctx context.Context, db bun.IDB, workspaceID string) (string, error) {
	setting := &servermodels.CustomerServiceSetting{}
	if err := db.NewSelect().Model(setting).
		Column("customer_identity_secret").
		Where("css.workspace_id = ?", workspaceID).
		Scan(ctx); err != nil {
		return "", fmt.Errorf("load customer identity secret: %w", err)
	}
	if setting.CustomerIdentitySecret == nil {
		return "", nil
	}
	return *setting.CustomerIdentitySecret, nil
}

// GetCustomerIdentitySecret 返回当前企业的客户身份密钥，未生成时返回空串。
func GetCustomerIdentitySecret(ctx context.Context, db *bun.DB, identity *servermodels.Identity) (string, error) {
	return LoadCustomerIdentitySecret(ctx, db, identity.Workspace.ID)
}

// RegenerateCustomerIdentitySecret 生成或重新生成当前企业的客户身份密钥，并在提交后结束该企业全部以签名身份建立的访客事件流。
func RegenerateCustomerIdentitySecret(ctx context.Context, db *bun.DB, identity *servermodels.Identity) (string, error) {
	// 生成 32 字节随机密钥并以 base64url 字符串保存。
	secret := random.Base64URL(32)
	err := realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		setting := &servermodels.CustomerServiceSetting{WorkspaceID: identity.Workspace.ID, CustomerIdentitySecret: &secret}
		result, err := tx.NewUpdate().Model(setting).Column("customer_identity_secret").WherePK().Exec(ctx)
		if err != nil {
			return fmt.Errorf("save customer identity secret: %w", err)
		}
		if count, err := result.RowsAffected(); err != nil {
			return err
		} else if count != 1 {
			return fmt.Errorf("update customer identity secret affected %d rows", count)
		}
		realtime.Notify(ctx, realtime.CustomerIdentityRevoked(identity.Workspace.ID))
		return nil
	})
	if err != nil {
		return "", err
	}
	return secret, nil
}
