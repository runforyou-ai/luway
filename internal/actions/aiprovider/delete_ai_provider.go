//go:build server

package aiprovider

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// DeleteAIProviderAction 删除模型服务供应商。
type DeleteAIProviderAction struct {
	db *bun.DB
}

// NewDeleteAIProviderAction 创建模型服务供应商删除操作。
func NewDeleteAIProviderAction(db *bun.DB) *DeleteAIProviderAction {
	return &DeleteAIProviderAction{db: db}
}

// Execute 删除模型服务供应商及其模型目录。
func (a *DeleteAIProviderAction) Execute(ctx context.Context, identity *servermodels.Identity, providerID string) error {
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		provider, err := loadProvider(ctx, tx, identity.Organization.ID, providerID, true)
		if err != nil {
			return err
		}
		references, err := providerReferences(ctx, tx, identity.Organization.ID, provider.ID)
		if err != nil {
			return err
		}
		if len(references) > 0 {
			return ErrInUse
		}
		if _, err := tx.NewDelete().
			Model((*servermodels.AIProviderModel)(nil)).
			Where("organization_id = ?", identity.Organization.ID).
			Where("provider_id = ?", provider.ID).
			Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewDelete().
			Model(provider).
			Where("organization_id = ?", identity.Organization.ID).
			WherePK().
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete AI provider: %w", err)
	}
	return nil
}
