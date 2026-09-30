//go:build server

package customerservice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"github.com/uptrace/bun"
)

// ModelCredential 是设置引用的模型及其供应商凭据。
type ModelCredential struct {
	Brand           string `bun:"brand"`
	APIKey          string `bun:"api_key"`
	APIURL          string `bun:"api_url"`
	Identifier      string `bun:"identifier"`
	MaxOutputTokens int64  `bun:"max_output_tokens"`
	ContextWindow   int64  `bun:"context_window"`
}

// LoadModel 读取设置引用的指定用途模型；未设置或模型已不存在时返回 nil。
func LoadModel(ctx context.Context, db bun.IDB, organizationID string, reference *domain.AIModelReference, modelType domain.AIModelType) (*ModelCredential, error) {
	if reference == nil {
		return nil, nil
	}
	credential := &ModelCredential{}
	err := db.NewSelect().
		TableExpr("ai_provider_models AS aipm").
		ColumnExpr("aip.brand, aip.api_key, aip.api_url, aipm.identifier, aipm.max_output_tokens, aipm.context_window").
		Join("JOIN ai_providers AS aip ON aip.id = aipm.provider_id AND aip.organization_id = aipm.organization_id").
		Where("aipm.organization_id = ? AND aipm.provider_id = ? AND aipm.identifier = ? AND aipm.model_type = ?",
			organizationID, reference.ProviderID, reference.ModelIdentifier, modelType).
		Scan(ctx, credential)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load referenced model: %w", err)
	}
	return credential, nil
}

// ModelConfig 把模型凭据转换为单次模型调用配置。
func (c *ModelCredential) ModelConfig() agentruntime.ModelConfig {
	return agentruntime.ModelConfig{
		Brand: c.Brand, APIKey: c.APIKey, BaseURL: c.APIURL, Identifier: c.Identifier,
		MaxOutputTokens: int(c.MaxOutputTokens), ContextWindow: int(c.ContextWindow),
	}
}
