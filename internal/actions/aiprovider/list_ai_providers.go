//go:build server

package aiprovider

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ListAIProvidersQuery 查询当前企业的模型服务供应商。
type ListAIProvidersQuery struct {
	db *bun.DB
}

// NewListAIProvidersQuery 创建模型服务供应商列表查询。
func NewListAIProvidersQuery(db *bun.DB) *ListAIProvidersQuery {
	return &ListAIProvidersQuery{db: db}
}

// Execute 返回当前企业的模型服务供应商列表。
func (q *ListAIProvidersQuery) Execute(ctx context.Context, identity *servermodels.Identity) ([]Summary, error) {
	providers := make([]servermodels.AIProvider, 0)
	if err := q.db.NewSelect().
		Model(&providers).
		Column("id", "brand", "name", "api_url").
		Where("aip.organization_id = ?", identity.Organization.ID).
		Order("aip.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list AI providers: %w", err)
	}
	modelRecords := make([]servermodels.AIProviderModel, 0)
	if err := q.db.NewSelect().
		Model(&modelRecords).
		Column("provider_id", "identifier", "name", "model_type").
		Where("aipm.organization_id = ?", identity.Organization.ID).
		Order("aipm.provider_id ASC").
		Order("aipm.created_at ASC").
		Order("aipm.identifier ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list AI provider models: %w", err)
	}
	modelsByProvider := make(map[string][]ModelSummary)
	for _, record := range modelRecords {
		modelsByProvider[record.ProviderID] = append(
			modelsByProvider[record.ProviderID],
			ModelSummary{
				Identifier: record.Identifier,
				Name:       record.Name,
				Type:       domain.AIModelType(record.Type),
			},
		)
	}
	output := make([]Summary, 0, len(providers))
	for _, provider := range providers {
		output = append(output, Summary{
			ID: provider.ID, Brand: domain.AIProviderBrand(provider.Brand), Name: provider.Name, APIURL: provider.APIURL,
			Models: modelsByProvider[provider.ID],
		})
	}
	return output, nil
}
