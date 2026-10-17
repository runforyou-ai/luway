//go:build server

// Package aimodel 按模型引用解析、锁定、列出和检查业务引用的 AI 模型及其调用来源；平台侧为 Auto 能力池，工作区侧为自有模型。
package aimodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrUnavailable 表示模型不存在、当前工作区不可用、不满足用途要求或没有可用来源。
var ErrUnavailable = errors.New("AI model unavailable")

// Model 定义解析后的模型、解析时的用途与按优先级排列的可用来源。
type Model struct {
	ID              string                        `bun:"id"`
	Scope           domain.AIModelScope           `bun:"scope"`
	Name            string                        `bun:"name"`
	Type            domain.AIModelType            `bun:"model_type"`
	InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	ContextWindow   int64                         `bun:"context_window"`
	MaxOutputTokens int64                         `bun:"max_output_tokens"`
	Usage           domain.AIModelUsage           `bun:"-"`
	Routes          []Route                       `bun:"-"`
}

// Route 定义模型的一个可用来源：供应商连接参数、该来源的上游模型标识与被选为先尝试来源的权重，权重为 0 只作备用。
// Auto 池成员的 ID 为池成员编号；工作区模型的 ID 为 ai_model_routes 编号。
type Route struct {
	ID           string                 `bun:"id"`
	Identifier   string                 `bun:"identifier"`
	Weight       int                    `bun:"weight"`
	ProviderID   string                 `bun:"provider_id"`
	ProviderName string                 `bun:"provider_name"`
	Brand        domain.AIProviderBrand `bun:"brand"`
	APIKey       string                 `bun:"api_key"`
	APIURL       string                 `bun:"api_url"`
}

// Option 定义模型选择器展示的模型；工作区模型带首选来源的供应商，Auto 不展示来源。
type Option struct {
	ID              string                        `bun:"id"`
	Scope           domain.AIModelScope           `bun:"scope"`
	Name            string                        `bun:"name"`
	Type            domain.AIModelType            `bun:"model_type"`
	InputModalities []domain.AIModelInputModality `bun:"input_modalities,type:jsonb"`
	ProviderID      string                        `bun:"provider_id"`
	ProviderName    string                        `bun:"provider_name"`
	Brand           domain.AIProviderBrand        `bun:"brand"`
}

// Join 为查询关联可用模型 AS aim：modelIDExpr 为 auto 或工作区模型编号；不满足用途或不可用时 aim 列为空。
func Join(query *bun.SelectQuery, modelIDExpr, workspaceIDExpr string, usage domain.AIModelUsage) *bun.SelectQuery {
	requirement := mustRequirement(usage)
	allowed := serverstorage.AutoPoolMemberAllowedFunction + "(" + workspaceIDExpr + ", apm.id)"
	poolFilter := "apm.enabled AND apm.model_type = ? AND " + allowed + " AND (? OR apm.input_modalities @> ?::jsonb)"
	args := []any{requirement.Type, !requirement.RequiresText, `["text"]`}
	for _, modality := range requirement.RequiredModalities {
		poolFilter += " AND apm.input_modalities @> ?::jsonb"
		args = append(args, modalityJSON(modality))
	}
	sql := `LEFT JOIN LATERAL (
	SELECT * FROM (
		SELECT 'auto'::text AS id, 'Auto'::text AS name, ?::text AS model_type,
			COALESCE((SELECT apm.input_modalities FROM auto_pool_members AS apm WHERE ` + poolFilter + ` ORDER BY apm.weight DESC, apm.priority ASC, apm.id ASC LIMIT 1), '[]'::jsonb) AS input_modalities,
			COALESCE((SELECT MIN(apm.context_window) FROM auto_pool_members AS apm WHERE ` + poolFilter + `), 0) AS context_window,
			COALESCE((SELECT MIN(apm.max_output_tokens) FROM auto_pool_members AS apm WHERE ` + poolFilter + `), 0) AS max_output_tokens,
			'platform'::text AS scope
		WHERE (` + modelIDExpr + `) = 'auto'
			AND EXISTS (SELECT 1 FROM auto_pool_members AS apm WHERE ` + poolFilter + `)
		UNION ALL
		SELECT aim.id::text, aim.name, aim.model_type::text, aim.input_modalities, aim.context_window, aim.max_output_tokens, 'workspace'::text
		FROM ai_models AS aim
		WHERE aim.id::text = (` + modelIDExpr + `)
			AND aim.workspace_id = ` + workspaceIDExpr + `
			AND aim.model_type = ?
			AND (? OR aim.input_modalities @> ?::jsonb)`
	// Auto 分支：model_type 一次 + poolFilter 四次（modalities / context / max_output / exists）
	joinArgs := []any{requirement.Type}
	joinArgs = append(joinArgs, args...)
	joinArgs = append(joinArgs, args...)
	joinArgs = append(joinArgs, args...)
	joinArgs = append(joinArgs, args...)
	joinArgs = append(joinArgs, requirement.Type, !requirement.RequiresText, `["text"]`)
	for _, modality := range requirement.RequiredModalities {
		sql += ` AND aim.input_modalities @> ?::jsonb`
		joinArgs = append(joinArgs, modalityJSON(modality))
	}
	sql += ` AND EXISTS (SELECT 1 FROM ai_model_routes AS amr WHERE amr.model_id = aim.id AND amr.enabled)
	) AS resolved
) AS aim ON true`
	return query.Join(sql, joinArgs...)
}

// Resolve 读取对工作区可用且满足用途要求的模型及其可用来源，不可用时返回 ErrUnavailable。
func Resolve(ctx context.Context, db bun.IDB, workspaceID, modelID string, usage domain.AIModelUsage) (*Model, error) {
	return load(ctx, db, workspaceID, modelID, usage, "", false)
}

// ResolveAutoSticky 解析模型引用：已钉住的成员必须仍可用，否则返回 ErrUnavailable（不静默换成员，以免破坏向量空间）；
// 首次选型时从池中取权重最高者（相同则取靠前），并把 Routes 收窄为该成员，保证本次调用与写入的 sticky 一致。
func ResolveAutoSticky(ctx context.Context, db bun.IDB, workspaceID, modelID, stickyMemberID string, usage domain.AIModelUsage) (*Model, string, error) {
	if !IsAuto(modelID) {
		model, err := load(ctx, db, workspaceID, modelID, usage, "", false)
		return model, "", err
	}
	if stickyMemberID != "" {
		model, err := loadAuto(ctx, db, workspaceID, usage, stickyMemberID, false)
		if err != nil {
			return nil, "", err
		}
		return model, stickyMemberID, nil
	}
	model, err := loadAuto(ctx, db, workspaceID, usage, "", false)
	if err != nil {
		return nil, "", err
	}
	primary := pickStickyMember(model.Routes)
	model.Routes = []Route{primary}
	return model, primary.ID, nil
}

// pickStickyMember 选出首次钉住的池成员：权重大于 0 时取权重最高者，否则取当前顺序的第一个（已按 priority、id 排好）。
func pickStickyMember(routes []Route) Route {
	primary := routes[0]
	for _, route := range routes[1:] {
		if route.Weight > primary.Weight {
			primary = route
		}
	}
	return primary
}

// Lock 在事务中校验模型满足用途要求，并以 KEY SHARE 锁定模型、可用来源与来源供应商直至事务结束，不可用时返回 ErrUnavailable。
func Lock(ctx context.Context, tx bun.Tx, workspaceID, modelID string, usage domain.AIModelUsage) (*Model, error) {
	return load(ctx, tx, workspaceID, modelID, usage, "", true)
}

// load 读取并按需锁定单个可用模型及其来源。
func load(ctx context.Context, db bun.IDB, workspaceID, modelID string, usage domain.AIModelUsage, stickyMemberID string, lock bool) (*Model, error) {
	if IsAuto(modelID) {
		return loadAuto(ctx, db, workspaceID, usage, stickyMemberID, lock)
	}
	if !str.IsUUID(modelID) {
		return nil, ErrUnavailable
	}
	requirement := mustRequirement(usage)
	model := &Model{Usage: usage}
	query := db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.name, aim.model_type, aim.input_modalities, aim.context_window, aim.max_output_tokens").
		ColumnExpr("'workspace'::text AS scope").
		Where("aim.id = ?", modelID).
		Where("aim.workspace_id = ?", workspaceID)
	query = whereRequirement(query, requirement)
	if lock {
		query = query.For("KEY SHARE")
	}
	if err := query.Scan(ctx, model); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnavailable
	} else if err != nil {
		return nil, fmt.Errorf("load AI model %q: %w", modelID, err)
	}
	routes := db.NewSelect().TableExpr("ai_model_routes AS amr").
		ColumnExpr("amr.id::text AS id, amr.identifier, amr.weight").
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

// LoadOptions 批量读取已保存配置引用的模型展示信息；结果按模型引用索引，缺失项表示不可用。
func LoadOptions(ctx context.Context, db bun.IDB, workspaceID string, modelIDs []string, usage domain.AIModelUsage) (map[string]Option, error) {
	result := make(map[string]Option, len(modelIDs))
	workspaceIDs := make([]string, 0, len(modelIDs))
	wantAuto := false
	for _, id := range modelIDs {
		if IsAuto(id) {
			wantAuto = true
			continue
		}
		workspaceIDs = append(workspaceIDs, id)
	}
	if wantAuto {
		requirement := mustRequirement(usage)
		// 已保存的 Auto 引用始终可展示，与当前池是否可调用无关；选项列表另行用 autoOption 过滤。
		result[domain.AIModelRefAuto] = Option{
			ID: domain.AIModelRefAuto, Scope: domain.AIModelScopePlatform, Name: AutoName, Type: requirement.Type,
		}
	}
	if len(workspaceIDs) > 0 {
		options := make([]Option, 0, len(workspaceIDs))
		if err := optionQuery(db, workspaceID, usage).Where("aim.id IN (?)", bun.List(workspaceIDs)).Scan(ctx, &options); err != nil {
			return nil, fmt.Errorf("load AI model options: %w", err)
		}
		for _, option := range options {
			result[option.ID] = option
		}
	}
	return result, nil
}

// optionQuery 构造对工作区可用且满足用途要求的工作区模型展示查询。
func optionQuery(db bun.IDB, workspaceID string, usage domain.AIModelUsage) *bun.SelectQuery {
	requirement := mustRequirement(usage)
	return whereRequirement(db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.name, aim.model_type, aim.input_modalities").
		ColumnExpr("'workspace'::text AS scope").
		ColumnExpr("COALESCE(aip.id::text, '') AS provider_id, COALESCE(aip.name, '') AS provider_name, COALESCE(aip.brand, '') AS brand").
		Join(`LEFT JOIN LATERAL (
	SELECT provider.id, provider.name, provider.brand FROM ai_model_routes AS amr
	JOIN ai_providers AS provider ON provider.id = amr.provider_id
	WHERE amr.model_id = aim.id
	ORDER BY amr.enabled DESC, amr.priority ASC, amr.id ASC LIMIT 1
) AS aip ON true`).
		Where("aim.workspace_id = ?", workspaceID), requirement)
}

// whereRequirement 追加用途对模型类型与输入模态的过滤条件。
func whereRequirement(query *bun.SelectQuery, requirement domain.AIModelRequirement) *bun.SelectQuery {
	query = query.Where("aim.model_type = ?", requirement.Type).
		Where("? OR aim.input_modalities @> ?::jsonb", !requirement.RequiresText, `["text"]`)
	for _, modality := range requirement.RequiredModalities {
		query = query.Where("aim.input_modalities @> ?::jsonb", modalityJSON(modality))
	}
	return query
}

// modalityJSON 返回用于 jsonb 包含判断的单模态数组字面量。
func modalityJSON(modality domain.AIModelInputModality) string {
	return `["` + string(modality) + `"]`
}

// mustRequirement 返回用途的模型要求，未知用途属于调用方编程错误。
func mustRequirement(usage domain.AIModelUsage) domain.AIModelRequirement {
	requirement, ok := usage.Requirement()
	if !ok {
		panic(fmt.Sprintf("unknown AI model usage %q", usage))
	}
	return requirement
}

// Option 返回模型的展示信息，工作区模型的供应商取首选来源。
func (m *Model) Option() Option {
	option := Option{ID: m.ID, Scope: m.Scope, Name: m.Name, Type: m.Type, InputModalities: m.InputModalities}
	if m.Scope == domain.AIModelScopeWorkspace {
		route := m.Routes[0]
		option.ProviderID, option.ProviderName, option.Brand = route.ProviderID, route.ProviderName, route.Brand
	}
	return option
}
