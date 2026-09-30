//go:build server

package aiprovider

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetAIProviderQuery 查询当前企业中的模型服务供应商。
type GetAIProviderQuery struct {
	db *bun.DB
}

// NewGetAIProviderQuery 创建模型服务供应商详情查询。
func NewGetAIProviderQuery(db *bun.DB) *GetAIProviderQuery {
	return &GetAIProviderQuery{db: db}
}

// Execute 返回模型服务供应商及其模型目录。
func (q *GetAIProviderQuery) Execute(ctx context.Context, identity *servermodels.Identity, providerID string) (*Record, error) {
	provider, err := loadProvider(ctx, q.db, identity.Organization.ID, providerID, false)
	if err != nil {
		return nil, fmt.Errorf("get AI provider: %w", err)
	}
	models, err := loadModels(ctx, q.db, identity.Organization.ID, provider.ID)
	if err != nil {
		return nil, fmt.Errorf("get AI provider models: %w", err)
	}
	output := recordFromModel(*provider, models)
	return &output, nil
}
