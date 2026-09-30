//go:build server

package aiprovider

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateAIProviderAction 创建模型服务供应商。
type CreateAIProviderAction struct {
	db *bun.DB
}

// NewCreateAIProviderAction 创建模型服务供应商操作。
func NewCreateAIProviderAction(db *bun.DB) *CreateAIProviderAction {
	return &CreateAIProviderAction{db: db}
}

// Execute 创建模型服务供应商并保存模型目录。
func (a *CreateAIProviderAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var provider servermodels.AIProvider
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		provider = servermodels.AIProvider{
			OrganizationID: identity.Organization.ID, Brand: string(input.Brand), Name: input.Name,
			CredentialType: string(input.CredentialType), APIKey: input.APIKey, APIURL: input.APIURL,
		}
		if _, err := tx.NewInsert().
			Model(&provider).
			Column("organization_id", "brand", "name", "credential_type", "api_key", "api_url").
			Returning("*").
			Exec(ctx); err != nil {
			return err
		}
		return replaceModels(ctx, tx, identity.Organization.ID, provider.ID, input.Models)
	})
	if isNameConflict(err) {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("create AI provider: %w", err)
	}
	output := recordFromModel(provider, input.Models)
	return &output, nil
}
