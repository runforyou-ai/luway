//go:build server

package aiprovider

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateAIProviderAction 修改模型服务供应商。
type UpdateAIProviderAction struct {
	db *bun.DB
}

// NewUpdateAIProviderAction 创建模型服务供应商修改操作。
func NewUpdateAIProviderAction(db *bun.DB) *UpdateAIProviderAction {
	return &UpdateAIProviderAction{db: db}
}

// Execute 修改模型服务供应商和模型目录。
func (a *UpdateAIProviderAction) Execute(ctx context.Context, identity *servermodels.Identity, providerID string, update UpdateInput) (*Record, error) {
	var provider *servermodels.AIProvider
	var models []Model
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		current, err := loadProvider(ctx, tx, identity.Organization.ID, providerID, true)
		if err != nil {
			return err
		}
		input, fields := normalizeInput(Input{
			Brand: domain.AIProviderBrand(current.Brand), Name: update.Name, CredentialType: update.CredentialType,
			APIKey: update.APIKey, APIURL: update.APIURL, Models: update.Models,
		})
		if len(fields) > 0 {
			return &ValidationError{Fields: fields}
		}
		if err := validateReferencedModels(ctx, tx, identity.Organization.ID, current.ID, input.Models); err != nil {
			return err
		}
		current.Name = input.Name
		current.CredentialType = string(input.CredentialType)
		current.APIKey = input.APIKey
		current.APIURL = input.APIURL
		if _, err := tx.NewUpdate().
			Model(current).
			Column("name", "credential_type", "api_key", "api_url").
			Set("updated_at = now()").
			Where("organization_id = ?", identity.Organization.ID).
			WherePK().
			Returning("*").
			Exec(ctx); err != nil {
			return err
		}
		if err := replaceModels(ctx, tx, identity.Organization.ID, current.ID, input.Models); err != nil {
			return err
		}
		provider = current
		models = input.Models
		return nil
	})
	if isNameConflict(err) {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("update AI provider: %w", err)
	}
	output := recordFromModel(*provider, models)
	return &output, nil
}
