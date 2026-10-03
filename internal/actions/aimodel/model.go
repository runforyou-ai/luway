//go:build server

// Package aimodel 按模型编号解析、锁定、列出和检查业务引用的 AI 模型及其调用来源。
package aimodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ErrUnavailable 表示模型不存在、不属于当前工作区、不满足用途要求或没有可用来源。
var ErrUnavailable = errors.New("AI model unavailable")

// Model 定义解析后的模型、解析时的用途与按尝试顺序排列的可用来源。
type Model struct {
	ID              string                        `bun:"id"`
	Name            string                        `bun:"name"`
	Type            domain.AIModelType            `bun:"model_type"`
	InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	ContextWindow   int64                         `bun:"context_window"`
	MaxOutputTokens int64                         `bun:"max_output_tokens"`
	Usage           domain.AIModelUsage           `bun:"-"`
	Routes          []Route                       `bun:"-"`
}

// Route 定义模型的一个可用来源：供应商连接参数与该来源的上游模型标识。
type Route struct {
	ID           string                 `bun:"id"`
	Identifier   string                 `bun:"identifier"`
	ProviderID   string                 `bun:"provider_id"`
	ProviderName string                 `bun:"provider_name"`
	Brand        domain.AIProviderBrand `bun:"brand"`
	APIKey       string                 `bun:"api_key"`
	APIURL       string                 `bun:"api_url"`
}

// Option 定义模型选择器展示的模型及其首选来源的供应商，不含凭据。
type Option struct {
	ID              string                        `bun:"id"`
	Name            string                        `bun:"name"`
	Type            domain.AIModelType            `bun:"model_type"`
	InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	ProviderID      string                        `bun:"provider_id"`
	ProviderName    string                        `bun:"provider_name"`
	Brand           domain.AIProviderBrand        `bun:"brand"`
}

// Join 为查询关联 ai_models AS aim：模型编号取 modelIDExpr，模型须属于 organizationIDExpr 指定的工作区、满足用途要求且至少有一个已启用来源，不满足时 aim 列为空。
func Join(query *bun.SelectQuery, modelIDExpr, organizationIDExpr string, usage domain.AIModelUsage) *bun.SelectQuery {
	requirement := mustRequirement(usage)
	return query.Join("LEFT JOIN ai_models AS aim ON aim.id = "+modelIDExpr+" AND aim.organization_id = "+organizationIDExpr+
		" AND aim.model_type = ? AND (? OR aim.input_modalities @> ?::jsonb)"+
		" AND EXISTS (SELECT 1 FROM ai_model_routes AS amr WHERE amr.model_id = aim.id AND amr.enabled)",
		requirement.Type, !requirement.RequiresText, `["text"]`)
}

// Resolve 读取工作区中满足用途要求的模型及其可用来源，不可用时返回 ErrUnavailable。
func Resolve(ctx context.Context, db bun.IDB, organizationID, modelID string, usage domain.AIModelUsage) (*Model, error) {
	return load(ctx, db, organizationID, modelID, usage, false)
}

// Lock 在事务中校验模型满足用途要求，并以 KEY SHARE 锁定模型、可用来源与来源供应商直至事务结束，不可用时返回 ErrUnavailable。
func Lock(ctx context.Context, tx bun.Tx, organizationID, modelID string, usage domain.AIModelUsage) (*Model, error) {
	return load(ctx, tx, organizationID, modelID, usage, true)
}

// load 读取并按需锁定单个可用模型及其来源。
func load(ctx context.Context, db bun.IDB, organizationID, modelID string, usage domain.AIModelUsage, lock bool) (*Model, error) {
	if !common.ValidUUID(modelID) {
		return nil, ErrUnavailable
	}
	requirement := mustRequirement(usage)
	model := &Model{Usage: usage}
	query := db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.name, aim.model_type, aim.input_modalities, aim.context_window, aim.max_output_tokens").
		Where("aim.id = ?", modelID).
		Where("aim.organization_id = ?", organizationID).
		Where("aim.model_type = ?", requirement.Type).
		Where("? OR aim.input_modalities @> ?::jsonb", !requirement.RequiresText, `["text"]`)
	if lock {
		query = query.For("KEY SHARE")
	}
	if err := query.Scan(ctx, model); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	} else if err != nil {
		return nil, fmt.Errorf("load AI model %q: %w", modelID, err)
	}
	routes := db.NewSelect().TableExpr("ai_model_routes AS amr").
		ColumnExpr("amr.id::text AS id, amr.identifier").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand, aip.api_key, aip.api_url").
		Join("JOIN ai_providers AS aip ON aip.id = amr.provider_id").
		Where("amr.model_id = ?", modelID).
		Where("amr.enabled").
		OrderExpr("amr.priority ASC, amr.id ASC")
	if lock {
		routes = routes.For("KEY SHARE OF amr, aip")
	}
	if err := routes.Scan(ctx, &model.Routes); err != nil {
		return nil, fmt.Errorf("load AI model %q routes: %w", modelID, err)
	}
	if len(model.Routes) == 0 {
		return nil, ErrUnavailable
	}
	return model, nil
}

// LoadOptions 批量读取工作区中满足用途要求的模型展示信息，结果按模型编号索引，缺失项表示不可用。
func LoadOptions(ctx context.Context, db bun.IDB, organizationID string, modelIDs []string, usage domain.AIModelUsage) (map[string]Option, error) {
	options := make([]Option, 0, len(modelIDs))
	if len(modelIDs) > 0 {
		if err := optionQuery(db, organizationID, usage).Where("aim.id IN (?)", bun.In(modelIDs)).Scan(ctx, &options); err != nil {
			return nil, fmt.Errorf("load AI model options: %w", err)
		}
	}
	result := make(map[string]Option, len(options))
	for _, option := range options {
		result[option.ID] = option
	}
	return result, nil
}

// optionQuery 构造工作区中满足用途要求且有可用来源的模型展示查询，供应商取首选来源 route AS aip。
func optionQuery(db bun.IDB, organizationID string, usage domain.AIModelUsage) *bun.SelectQuery {
	requirement := mustRequirement(usage)
	return db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.name, aim.model_type, aim.input_modalities").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand").
		Join(`JOIN LATERAL (
	SELECT provider.id, provider.name, provider.brand FROM ai_model_routes AS amr
	JOIN ai_providers AS provider ON provider.id = amr.provider_id
	WHERE amr.model_id = aim.id AND amr.enabled
	ORDER BY amr.priority ASC, amr.id ASC LIMIT 1
) AS aip ON true`).
		Where("aim.organization_id = ?", organizationID).
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

// Option 返回模型的展示信息，供应商取首选来源。
func (m *Model) Option() Option {
	route := m.Routes[0]
	return Option{
		ID: m.ID, Name: m.Name, Type: m.Type, InputModalities: m.InputModalities,
		ProviderID: route.ProviderID, ProviderName: route.ProviderName, Brand: route.Brand,
	}
}
