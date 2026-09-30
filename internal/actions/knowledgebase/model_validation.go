//go:build server

package knowledgebase

import (
	"context"
	"slices"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// validateModels 锁定同企业供应商并校验知识库所选模型用途。
func validateModels(ctx context.Context, tx bun.Tx, organizationID string, input Input) error {
	providerIDs := []string{input.EmbeddingProviderID, input.RerankProviderID}
	slices.Sort(providerIDs)
	providers := make([]servermodels.AIProvider, 0)
	if err := tx.NewSelect().Model(&providers).Where("organization_id = ?", organizationID).
		Where("id IN (?)", bun.In(providerIDs)).Order("id ASC").For("SHARE").Scan(ctx); err != nil {
		return err
	}
	fields := make(map[string]common.FieldCode)
	for _, selected := range []struct {
		providerID, identifier, field string
		modelType                     domain.AIModelType
		code                          common.FieldCode
	}{
		{input.EmbeddingProviderID, input.EmbeddingModelIdentifier, "embeddingModelIdentifier", domain.AIModelTypeEmbedding, ValidationEmbeddingModelInvalid},
		{input.RerankProviderID, input.RerankModelIdentifier, "rerankModelIdentifier", domain.AIModelTypeRerank, ValidationRerankModelInvalid},
	} {
		found := slices.ContainsFunc(providers, func(provider servermodels.AIProvider) bool { return provider.ID == selected.providerID })
		if found {
			var err error
			found, err = tx.NewSelect().Model((*servermodels.AIProviderModel)(nil)).
				Where("organization_id = ?", organizationID).Where("provider_id = ?", selected.providerID).
				Where("identifier = ?", selected.identifier).Where("model_type = ?", selected.modelType).Exists(ctx)
			if err != nil {
				return err
			}
		}
		if !found {
			fields[selected.field] = selected.code
		}
	}
	if len(fields) > 0 {
		return &common.FieldError{Fields: fields}
	}
	return nil
}
