//go:build server

package aiprovider

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
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
		Where("aip.workspace_id = ?", identity.Workspace.ID).
		Order("aip.created_at ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("list AI providers: %w", err)
	}
	modelRecords := make([]struct {
		ID         string `bun:"id"`
		ProviderID string `bun:"provider_id"`
		Identifier string `bun:"identifier"`
		Name       string `bun:"name"`
		Type       string `bun:"model_type"`
	}, 0)
	if err := q.db.NewSelect().TableExpr("ai_model_routes AS amr").
		ColumnExpr("aim.id::text AS id, amr.provider_id::text AS provider_id, amr.identifier, aim.name, aim.model_type").
		Join("JOIN ai_models AS aim ON aim.id = amr.model_id").
		Join("JOIN ai_providers AS aip ON aip.id = amr.provider_id").
		Where("aip.workspace_id = ?", identity.Workspace.ID).
		OrderExpr("amr.provider_id ASC, aim.id ASC").
		Scan(ctx, &modelRecords); err != nil {
		return nil, fmt.Errorf("list AI provider models: %w", err)
	}
	modelsByProvider := make(map[string][]ModelSummary)
	for _, record := range modelRecords {
		modelsByProvider[record.ProviderID] = append(
			modelsByProvider[record.ProviderID],
			ModelSummary{
				ID:         record.ID,
				Identifier: record.Identifier,
				Name:       record.Name,
				Type:       domain.AIModelType(record.Type),
			},
		)
	}
	return arr.Map(providers, func(provider servermodels.AIProvider) Summary {
		return Summary{
			ID: provider.ID, Brand: domain.AIProviderBrand(provider.Brand), Name: provider.Name, APIURL: provider.APIURL,
			Models: modelsByProvider[provider.ID],
		}
	}), nil
}
