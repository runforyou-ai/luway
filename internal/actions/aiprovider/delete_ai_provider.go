//go:build server

package aiprovider

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
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
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		provider, err := loadProvider(ctx, tx, identity.Workspace.ID, providerID, true)
		if err != nil {
			return err
		}
		stored, err := loadModels(ctx, tx, provider.ID)
		if err != nil {
			return err
		}
		references, err := storedReferences(ctx, tx, identity.Workspace.ID, stored)
		if err != nil {
			return err
		}
		if len(references) > 0 {
			return ErrInUse
		}
		if err := deleteModels(ctx, tx, identity.Workspace.ID, arr.Map(stored, func(model Model) string { return model.ID })); err != nil {
			return err
		}
		_, err = tx.NewDelete().
			Model(provider).
			Where("workspace_id = ?", identity.Workspace.ID).
			WherePK().
			Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete AI provider: %w", err)
	}
	return nil
}
