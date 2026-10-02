//go:build server

// Package aimodel 按模型编号解析、锁定、列出和检查业务引用的 AI 模型。
package aimodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/uptrace/bun"
)

// ErrUnavailable 表示模型不存在、不属于当前工作区或不满足用途要求。
var ErrUnavailable = errors.New("AI model unavailable")

// Model 定义解析后的模型及其供应商连接参数。
type Model struct {
	ID              string                        `bun:"id"`
	Identifier      string                        `bun:"identifier"`
	Name            string                        `bun:"name"`
	Type            domain.AIModelType            `bun:"model_type"`
	InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	ContextWindow   int64                         `bun:"context_window"`
	MaxOutputTokens int64                         `bun:"max_output_tokens"`
	ProviderID      string                        `bun:"provider_id"`
	ProviderName    string                        `bun:"provider_name"`
	Brand           domain.AIProviderBrand        `bun:"brand"`
	APIKey          string                        `bun:"api_key"`
	APIURL          string                        `bun:"api_url"`
}

// Option 定义模型选择器展示的模型，不含供应商凭据。
type Option struct {
	ID              string                        `bun:"id"`
	Identifier      string                        `bun:"identifier"`
	Name            string                        `bun:"name"`
	Type            domain.AIModelType            `bun:"model_type"`
	InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	ProviderID      string                        `bun:"provider_id"`
	ProviderName    string                        `bun:"provider_name"`
	Brand           domain.AIProviderBrand        `bun:"brand"`
}

// Join 为查询关联 ai_models AS aim 与 ai_providers AS aip：模型编号取 modelIDExpr，供应商须属于 organizationIDExpr 指定的工作区且模型满足用途要求，不满足时两表列为空。
func Join(query *bun.SelectQuery, modelIDExpr, organizationIDExpr string, usage domain.AIModelUsage) *bun.SelectQuery {
	requirement := mustRequirement(usage)
	return query.
		Join("LEFT JOIN ai_models AS aim ON aim.id = "+modelIDExpr+" AND aim.model_type = ? AND (? OR aim.input_modalities @> ?::jsonb)",
			requirement.Type, !requirement.RequiresText, `["text"]`).
		Join("LEFT JOIN ai_providers AS aip ON aip.id = aim.provider_id AND aip.organization_id = " + organizationIDExpr)
}

// Resolve 读取工作区中满足用途要求的模型及其连接参数，不可用时返回 ErrUnavailable。
func Resolve(ctx context.Context, db bun.IDB, organizationID, modelID string, usage domain.AIModelUsage) (*Model, error) {
	return load(ctx, db, organizationID, modelID, usage, false)
}

// Lock 在事务中校验模型满足用途要求，并以 KEY SHARE 锁定模型与供应商直至事务结束，不可用时返回 ErrUnavailable。
func Lock(ctx context.Context, tx bun.Tx, organizationID, modelID string, usage domain.AIModelUsage) (*Model, error) {
	return load(ctx, tx, organizationID, modelID, usage, true)
}

// load 读取并按需锁定单个可用模型。
func load(ctx context.Context, db bun.IDB, organizationID, modelID string, usage domain.AIModelUsage, lock bool) (*Model, error) {
	if !common.ValidUUID(modelID) {
		return nil, ErrUnavailable
	}
	model := &Model{}
	query := modelQuery(db, organizationID, usage).
		ColumnExpr("aim.context_window, aim.max_output_tokens, aip.api_key, aip.api_url").
		Where("aim.id = ?", modelID)
	if lock {
		query = query.For("KEY SHARE OF aim, aip")
	}
	if err := query.Scan(ctx, model); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	} else if err != nil {
		return nil, fmt.Errorf("load AI model %q: %w", modelID, err)
	}
	return model, nil
}

// LoadOptions 批量读取工作区中满足用途要求的模型展示信息，结果按模型编号索引，缺失项表示不可用。
func LoadOptions(ctx context.Context, db bun.IDB, organizationID string, modelIDs []string, usage domain.AIModelUsage) (map[string]Option, error) {
	options := make([]Option, 0, len(modelIDs))
	if len(modelIDs) > 0 {
		if err := modelQuery(db, organizationID, usage).Where("aim.id IN (?)", bun.In(modelIDs)).Scan(ctx, &options); err != nil {
			return nil, fmt.Errorf("load AI model options: %w", err)
		}
	}
	result := make(map[string]Option, len(options))
	for _, option := range options {
		result[option.ID] = option
	}
	return result, nil
}

// modelQuery 构造工作区中满足用途要求的模型查询，包含展示列。
func modelQuery(db bun.IDB, organizationID string, usage domain.AIModelUsage) *bun.SelectQuery {
	requirement := mustRequirement(usage)
	return db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.identifier, aim.name, aim.model_type, aim.input_modalities").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand").
		Join("JOIN ai_providers AS aip ON aip.id = aim.provider_id").
		Where("aip.organization_id = ?", organizationID).
		Where("aim.model_type = ?", requirement.Type).
		Where("? OR aim.input_modalities @> ?::jsonb", !requirement.RequiresText, `["text"]`)
}

// mustRequirement 返回用途的模型要求，未知用途属于调用方编程错误。
func mustRequirement(usage domain.AIModelUsage) domain.AIModelRequirement {
	requirement, ok := usage.Requirement()
	if !ok {
		panic(fmt.Sprintf("unknown AI model usage %q", usage))
	}
	return requirement
}

// ModelConfig 把模型转换为单次模型调用配置。
func (m *Model) ModelConfig() agentruntime.ModelConfig {
	return agentruntime.ModelConfig{
		Brand: string(m.Brand), APIKey: m.APIKey, BaseURL: m.APIURL, Identifier: m.Identifier,
		MaxOutputTokens: int(m.MaxOutputTokens), ContextWindow: int(m.ContextWindow),
	}
}

// Option 返回模型的展示信息。
func (m *Model) Option() Option {
	return Option{
		ID: m.ID, Identifier: m.Identifier, Name: m.Name, Type: m.Type, InputModalities: m.InputModalities,
		ProviderID: m.ProviderID, ProviderName: m.ProviderName, Brand: m.Brand,
	}
}
