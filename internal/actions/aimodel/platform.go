//go:build server

package aimodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

var (
	// ErrPlatformNotFound 表示平台模型不存在。
	ErrPlatformNotFound = errors.New("platform AI model not found")
	// ErrPlatformInUse 表示平台模型仍被工作区业务配置引用。
	ErrPlatformInUse = errors.New("platform AI model is in use")
)

// 平台模型的字段校验码。
const (
	ValidationPlatformNameInvalid            common.FieldCode = "PLATFORM_AI_MODEL_NAME_INVALID"
	ValidationPlatformNameDuplicate          common.FieldCode = "PLATFORM_AI_MODEL_NAME_DUPLICATE"
	ValidationPlatformTypeInvalid            common.FieldCode = "PLATFORM_AI_MODEL_TYPE_INVALID"
	ValidationPlatformInputModalitiesInvalid common.FieldCode = "PLATFORM_AI_MODEL_INPUT_MODALITIES_INVALID"
	ValidationPlatformContextWindowInvalid   common.FieldCode = "PLATFORM_AI_MODEL_CONTEXT_WINDOW_INVALID"
	ValidationPlatformMaxOutputTokensInvalid common.FieldCode = "PLATFORM_AI_MODEL_MAX_OUTPUT_TOKENS_INVALID"
	ValidationPlatformRoutesInvalid          common.FieldCode = "PLATFORM_AI_MODEL_ROUTES_INVALID"
	ValidationPlatformRouteDuplicate         common.FieldCode = "PLATFORM_AI_MODEL_ROUTE_DUPLICATE"
	ValidationPlatformRouteWeightInvalid     common.FieldCode = "PLATFORM_AI_MODEL_ROUTE_WEIGHT_INVALID"
	ValidationPlatformUsageConflict          common.FieldCode = "PLATFORM_AI_MODEL_USAGE_CONFLICT"
)

// MaxRouteWeight 是来源权重的上限。
const MaxRouteWeight = 100

// platformModelColumns 是平台模型创建与修改写入的列。
var platformModelColumns = []string{
	"name", "model_type", "input_modalities", "context_window", "max_output_tokens",
}

// platformSpecFieldCodes 是模型属性字段对应的校验结果。
var platformSpecFieldCodes = map[string]common.FieldCode{
	"name":            ValidationPlatformNameInvalid,
	"type":            ValidationPlatformTypeInvalid,
	"inputModalities": ValidationPlatformInputModalitiesInvalid,
	"contextWindow":   ValidationPlatformContextWindowInvalid,
	"maxOutputTokens": ValidationPlatformMaxOutputTokensInvalid,
}

// PlatformRouteInput 定义平台模型的一个来源：平台供应商、该供应商的上游模型标识与权重，编号为空表示新增来源，权重为 0 只作备用。
type PlatformRouteInput struct {
	ID         string
	ProviderID string
	Identifier string
	Weight     int
	Enabled    bool
}

// PlatformInput 定义平台模型的属性与按优先级排列的来源。
type PlatformInput struct {
	Spec
	Routes []PlatformRouteInput
}

// PlatformRoute 定义平台模型的一个来源及其供应商。
type PlatformRoute struct {
	ID            string                 `bun:"id"`
	ModelID       string                 `bun:"model_id"`
	ProviderID    string                 `bun:"provider_id"`
	ProviderName  string                 `bun:"provider_name"`
	ProviderBrand domain.AIProviderBrand `bun:"provider_brand"`
	Identifier    string                 `bun:"identifier"`
	Weight        int                    `bun:"weight"`
	Enabled       bool                   `bun:"enabled"`
}

// PlatformRecord 定义平台模型的属性与按优先级排列的来源。
type PlatformRecord struct {
	ID string
	Spec
	Routes []PlatformRoute
}

// NormalizePlatformInput 规范化并校验平台模型属性与来源：来源至少一个，权重在 0 到 MaxRouteWeight 之间，已有来源编号不重复，同一供应商的上游模型标识不重复。
func NormalizePlatformInput(input PlatformInput) (PlatformInput, map[string]common.FieldCode) {
	fields := make(map[string]common.FieldCode)
	if field := NormalizeSpec(&input.Spec); field != "" {
		fields[field] = platformSpecFieldCodes[field]
	}
	routes := make([]PlatformRouteInput, 0, len(input.Routes))
	var seenIDs, seenTargets set.Set[string]
	for _, route := range input.Routes {
		route.Identifier = strings.TrimSpace(route.Identifier)
		providerID, validProvider := str.NormalizeUUID(route.ProviderID)
		if !validProvider || !ValidIdentifier(route.Identifier) {
			fields["routes"] = ValidationPlatformRoutesInvalid
			continue
		}
		if route.Weight < 0 || route.Weight > MaxRouteWeight {
			fields["routes"] = ValidationPlatformRouteWeightInvalid
			continue
		}
		route.ProviderID = providerID
		// 已有来源的编号须为不重复的 UUID。
		if route.ID != "" {
			id, valid := str.NormalizeUUID(route.ID)
			if !valid || !seenIDs.Add(id) {
				fields["routes"] = ValidationPlatformRoutesInvalid
				continue
			}
			route.ID = id
		}
		target := route.ProviderID + "\n" + route.Identifier
		if !seenTargets.Add(target) {
			fields["routes"] = ValidationPlatformRouteDuplicate
			continue
		}
		routes = append(routes, route)
	}
	if len(input.Routes) == 0 {
		fields["routes"] = ValidationPlatformRoutesInvalid
	}
	input.Routes = routes
	return input, fields
}

// CreatePlatform 在事务内创建对全部工作区可用的平台模型及其来源，返回模型编号；input 须已经 NormalizePlatformInput 规范化，调用方以 PlatformConflictError 转换事务结果中的唯一冲突。
func CreatePlatform(ctx context.Context, tx bun.Tx, input PlatformInput) (string, error) {
	record, err := platformModelRecord(input.Spec)
	if err != nil {
		return "", err
	}
	if _, err := tx.NewInsert().Model(&record).
		Column(platformModelColumns...).
		Returning("id").
		Exec(ctx); err != nil {
		return "", err
	}
	return record.ID, savePlatformRoutes(ctx, tx, record.ID, input.Routes)
}

// UpdatePlatform 在事务内锁定并修改平台模型的属性与来源；工作区业务配置引用的模型须继续满足引用用途，input 须已经 NormalizePlatformInput 规范化，调用方以 PlatformConflictError 转换事务结果中的唯一冲突。
func UpdatePlatform(ctx context.Context, tx bun.Tx, modelID string, input PlatformInput) error {
	if err := LockPlatform(ctx, tx, modelID); err != nil {
		return err
	}
	references, err := PlatformReferences(ctx, tx, []string{modelID})
	if err != nil {
		return err
	}
	for _, reference := range references {
		if !Satisfies(reference.Usage, input.Type, input.InputModalities) {
			return &common.FieldError{Fields: map[string]common.FieldCode{"type": ValidationPlatformUsageConflict}}
		}
	}
	record, err := platformModelRecord(input.Spec)
	if err != nil {
		return err
	}
	record.ID = modelID
	if _, err := tx.NewUpdate().Model(&record).
		Column(platformModelColumns...).
		Where("workspace_id IS NULL").
		WherePK().
		Exec(ctx); err != nil {
		return err
	}
	return savePlatformRoutes(ctx, tx, modelID, input.Routes)
}

// DeletePlatform 在事务内锁定并删除没有被工作区业务配置引用的平台模型及其来源，仍被引用时返回 ErrPlatformInUse。
func DeletePlatform(ctx context.Context, tx bun.Tx, modelID string) error {
	if err := LockPlatform(ctx, tx, modelID); err != nil {
		return err
	}
	references, err := PlatformReferences(ctx, tx, []string{modelID})
	if err != nil {
		return err
	}
	if len(references) > 0 {
		return ErrPlatformInUse
	}
	if _, err := tx.NewDelete().Model((*servermodels.AIModelRoute)(nil)).Where("model_id = ?", modelID).Exec(ctx); err != nil {
		return err
	}
	_, err = tx.NewDelete().Model((*servermodels.AIModel)(nil)).Where("id = ?", modelID).Where("workspace_id IS NULL").Exec(ctx)
	return err
}

// LockPlatform 以 UPDATE 锁定平台模型，不存在时返回 ErrPlatformNotFound。
func LockPlatform(ctx context.Context, tx bun.Tx, modelID string) error {
	err := tx.NewSelect().Model((*servermodels.AIModel)(nil)).
		ColumnExpr("aim.id::text").
		Where("aim.id = ?", modelID).
		Where("aim.workspace_id IS NULL").
		For("UPDATE").
		Scan(ctx, new(string))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPlatformNotFound
	}
	return err
}

// platformModelRow 定义平台模型目录查询的一行。
type platformModelRow struct {
	ID              string          `bun:"id"`
	Name            string          `bun:"name"`
	Type            string          `bun:"model_type"`
	InputModalities json.RawMessage `bun:"input_modalities"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
}

// LoadPlatform 按名称读取平台模型及其按优先级排列的来源，modelIDs 为 nil 时读取全部平台模型，否则须为 UUID。
func LoadPlatform(ctx context.Context, db bun.IDB, modelIDs []string) ([]PlatformRecord, error) {
	rows := make([]platformModelRow, 0)
	query := db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.name, aim.model_type, aim.input_modalities, aim.context_window, aim.max_output_tokens").
		Where("aim.workspace_id IS NULL").
		OrderExpr("lower(aim.name) ASC, aim.id ASC")
	if modelIDs != nil {
		query = query.Where("aim.id IN (?)", bun.List(modelIDs))
	}
	if err := query.Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load platform AI models: %w", err)
	}
	records := make([]PlatformRecord, 0, len(rows))
	if len(rows) == 0 {
		return records, nil
	}
	ids := arr.Map(rows, func(row platformModelRow) string { return row.ID })
	routes := make([]PlatformRoute, 0)
	if err := db.NewSelect().TableExpr("ai_model_routes AS amr").
		ColumnExpr("amr.id::text AS id, amr.model_id::text AS model_id, amr.identifier, amr.weight, amr.enabled").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand AS provider_brand").
		Join("JOIN ai_providers AS aip ON aip.id = amr.provider_id").
		Where("amr.model_id IN (?)", bun.List(ids)).
		OrderExpr("amr.priority ASC, amr.id ASC").
		Scan(ctx, &routes); err != nil {
		return nil, fmt.Errorf("load platform AI model routes: %w", err)
	}
	routesByModel := arr.GroupBy(routes, func(route PlatformRoute) string { return route.ModelID })
	for _, row := range rows {
		inputModalities := make([]domain.AIModelInputModality, 0)
		if err := json.Unmarshal(row.InputModalities, &inputModalities); err != nil {
			return nil, fmt.Errorf("decode model %q input modalities: %w", row.ID, err)
		}
		records = append(records, PlatformRecord{
			ID: row.ID,
			Spec: Spec{
				Name: row.Name, Type: domain.AIModelType(row.Type), InputModalities: inputModalities,
				ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens,
			},
			Routes: routesByModel[row.ID],
		})
	}
	return records, nil
}

// savePlatformRoutes 按请求顺序保存平台模型的来源：顺序即优先级，未出现的已有来源被删除；来源供应商须为平台供应商并以 KEY SHARE 锁定至事务结束。
func savePlatformRoutes(ctx context.Context, tx bun.Tx, modelID string, routes []PlatformRouteInput) error {
	invalid := &common.FieldError{Fields: map[string]common.FieldCode{"routes": ValidationPlatformRoutesInvalid}}
	providerIDs := arr.Map(routes, func(route PlatformRouteInput) string { return route.ProviderID })
	var lockedProviders []string
	if err := tx.NewSelect().Model((*servermodels.AIProvider)(nil)).
		ColumnExpr("aip.id::text").
		Where("aip.id IN (?)", bun.List(providerIDs)).
		Where("aip.workspace_id IS NULL").
		For("KEY SHARE").
		Scan(ctx, &lockedProviders); err != nil {
		return err
	}
	locked := set.Collect(lockedProviders)
	var storedIDs []string
	if err := tx.NewSelect().Model((*servermodels.AIModelRoute)(nil)).
		ColumnExpr("amr.id::text").
		Where("amr.model_id = ?", modelID).
		Scan(ctx, &storedIDs); err != nil {
		return err
	}
	stored := set.Collect(storedIDs)
	kept := make([]string, 0, len(routes))
	inserted := make([]servermodels.AIModelRoute, 0, len(routes))
	for priority, route := range routes {
		if !locked.Has(route.ProviderID) {
			return invalid
		}
		record := servermodels.AIModelRoute{
			ID: route.ID, ModelID: modelID, ProviderID: route.ProviderID, Identifier: route.Identifier,
			Priority: priority, Weight: route.Weight, Enabled: route.Enabled,
		}
		if route.ID == "" {
			inserted = append(inserted, record)
			continue
		}
		if !stored.Has(route.ID) {
			return invalid
		}
		kept = append(kept, route.ID)
		if _, err := tx.NewUpdate().Model(&record).
			Column("provider_id", "identifier", "priority", "weight", "enabled").
			Where("model_id = ?", modelID).
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
	}
	deleted := tx.NewDelete().Model((*servermodels.AIModelRoute)(nil)).Where("model_id = ?", modelID)
	if len(kept) > 0 {
		deleted = deleted.Where("id NOT IN (?)", bun.List(kept))
	}
	if _, err := deleted.Exec(ctx); err != nil {
		return err
	}
	if len(inserted) == 0 {
		return nil
	}
	_, err := tx.NewInsert().Model(&inserted).
		Column("model_id", "provider_id", "identifier", "priority", "weight", "enabled").
		Exec(ctx)
	return err
}

// platformModelRecord 把模型属性转换为平台模型存储模型。
func platformModelRecord(spec Spec) (servermodels.AIModel, error) {
	inputModalities, err := json.Marshal(spec.InputModalities)
	if err != nil {
		return servermodels.AIModel{}, err
	}
	return servermodels.AIModel{
		Name: spec.Name, Type: string(spec.Type), InputModalities: inputModalities,
		ContextWindow: spec.ContextWindow, MaxOutputTokens: spec.MaxOutputTokens,
	}, nil
}

// PlatformConflictError 把平台模型写入事务结果中的名称或来源唯一冲突转换为字段校验错误，其他错误返回 nil；来源唯一约束在事务提交时校验，须对整个事务的结果调用。
func PlatformConflictError(err error) error {
	switch {
	case pgerr.UniqueViolationOn(err, "ai_models_platform_name_unique"):
		return &common.FieldError{Fields: map[string]common.FieldCode{"name": ValidationPlatformNameDuplicate}}
	case pgerr.UniqueViolationOn(err, "ai_model_routes_provider_identifier_unique"):
		return &common.FieldError{Fields: map[string]common.FieldCode{"routes": ValidationPlatformRouteDuplicate}}
	default:
		return nil
	}
}
