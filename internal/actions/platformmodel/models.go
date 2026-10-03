//go:build server

package platformmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// modelColumns 是平台模型创建与修改写入的列。
var modelColumns = []string{
	"name", "model_type", "input_modalities", "context_window", "max_output_tokens",
	"input_credit_price", "output_credit_price", "request_credit_price",
}

// specFieldCodes 是模型属性字段对应的校验结果。
var specFieldCodes = map[string]ValidationCode{
	"name":            ValidationNameInvalid,
	"type":            ValidationTypeInvalid,
	"inputModalities": ValidationInputModalitiesInvalid,
	"contextWindow":   ValidationContextWindowInvalid,
	"maxOutputTokens": ValidationMaxOutputTokensInvalid,
}

// ListQuery 读取平台模型目录。
type ListQuery struct{ db *bun.DB }

// NewListQuery 创建平台模型目录查询。
func NewListQuery(db *bun.DB) *ListQuery {
	return &ListQuery{db: db}
}

// Execute 按名称返回全部平台模型及其来源。
func (q *ListQuery) Execute(ctx context.Context) ([]Record, error) {
	records, err := loadRecords(ctx, q.db, nil)
	if err != nil {
		return nil, fmt.Errorf("list platform AI models: %w", err)
	}
	return records, nil
}

// GetQuery 读取平台模型详情。
type GetQuery struct{ db *bun.DB }

// NewGetQuery 创建平台模型详情查询。
func NewGetQuery(db *bun.DB) *GetQuery {
	return &GetQuery{db: db}
}

// Execute 返回平台模型及其来源，不存在时返回 ErrNotFound。
func (q *GetQuery) Execute(ctx context.Context, modelID string) (*Record, error) {
	if !common.ValidUUID(modelID) {
		return nil, ErrNotFound
	}
	records, err := loadRecords(ctx, q.db, []string{modelID})
	if err != nil {
		return nil, fmt.Errorf("get platform AI model: %w", err)
	}
	if len(records) == 0 {
		return nil, ErrNotFound
	}
	return &records[0], nil
}

// CreateAction 创建平台模型。
type CreateAction struct{ db *bun.DB }

// NewCreateAction 创建平台模型创建操作。
func NewCreateAction(db *bun.DB) *CreateAction {
	return &CreateAction{db: db}
}

// Execute 校验属性与来源并创建对全部工作区可用的平台模型。
func (a *CreateAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	var modelID string
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := platformaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		record, err := modelRecord(input)
		if err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&record).
			Column(modelColumns...).
			Returning("id").
			Exec(ctx); err != nil {
			return err
		}
		modelID = record.ID
		return saveRoutes(ctx, tx, modelID, input.Routes)
	})
	if conflict := conflictError(err); conflict != nil {
		return nil, conflict
	}
	if err != nil {
		return nil, fmt.Errorf("create platform AI model: %w", err)
	}
	return reload(ctx, a.db, modelID)
}

// UpdateAction 修改平台模型。
type UpdateAction struct{ db *bun.DB }

// NewUpdateAction 创建平台模型修改操作。
func NewUpdateAction(db *bun.DB) *UpdateAction {
	return &UpdateAction{db: db}
}

// Execute 修改平台模型的属性与来源；工作区业务配置引用的模型须继续满足引用用途，来源变化不触发知识库重新索引。
func (a *UpdateAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, modelID string, input Input) (*Record, error) {
	input, fields := normalizeInput(input)
	if len(fields) > 0 {
		return nil, &common.FieldError{Fields: fields}
	}
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := platformaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		if err := lockModel(ctx, tx, modelID); err != nil {
			return err
		}
		references, err := aimodel.PlatformReferences(ctx, tx, []string{modelID})
		if err != nil {
			return err
		}
		for _, reference := range references {
			if !aimodel.Satisfies(reference.Usage, input.Type, input.InputModalities) {
				return &common.FieldError{Fields: map[string]ValidationCode{"type": ValidationUsageConflict}}
			}
		}
		record, err := modelRecord(input)
		if err != nil {
			return err
		}
		record.ID = modelID
		if _, err := tx.NewUpdate().Model(&record).
			Column(modelColumns...).
			Set("updated_at = now()").
			Where("organization_id IS NULL").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		return saveRoutes(ctx, tx, modelID, input.Routes)
	})
	if conflict := conflictError(err); conflict != nil {
		return nil, conflict
	}
	if err != nil {
		return nil, fmt.Errorf("update platform AI model: %w", err)
	}
	return reload(ctx, a.db, modelID)
}

// DeleteAction 删除平台模型。
type DeleteAction struct{ db *bun.DB }

// NewDeleteAction 创建平台模型删除操作。
func NewDeleteAction(db *bun.DB) *DeleteAction {
	return &DeleteAction{db: db}
}

// Execute 删除没有被工作区业务配置引用的平台模型及其来源，仍被引用时返回 ErrInUse。
func (a *DeleteAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, modelID string) error {
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := platformaction.LockAdmin(ctx, tx, operator); err != nil {
			return err
		}
		if err := lockModel(ctx, tx, modelID); err != nil {
			return err
		}
		references, err := aimodel.PlatformReferences(ctx, tx, []string{modelID})
		if err != nil {
			return err
		}
		if len(references) > 0 {
			return ErrInUse
		}
		if _, err := tx.NewDelete().Model((*servermodels.AIModelRoute)(nil)).Where("model_id = ?", modelID).Exec(ctx); err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*servermodels.AIModel)(nil)).Where("id = ?", modelID).Where("organization_id IS NULL").Exec(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete platform AI model: %w", err)
	}
	return nil
}

// normalizeInput 规范化并校验平台模型属性、价格与来源：价格各项不为负且不超过上限，模型类型用不到的价格项置零；来源至少一个，同一供应商的上游模型标识不重复。
func normalizeInput(input Input) (Input, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	if field := aimodel.NormalizeSpec(&input.Spec); field != "" {
		fields[field] = specFieldCodes[field]
	}
	if input.Price != nil {
		price := *input.Price
		if !price.Valid() {
			fields["price"] = ValidationPriceInvalid
		}
		// 向量与重排模型没有输出 Token，判断模型不返回 Token 用量。
		switch input.Type {
		case domain.AIModelTypeEmbedding, domain.AIModelTypeRerank:
			price.Output = 0
		case domain.AIModelTypeDecision:
			price.Input, price.Output = 0, 0
		}
		input.Price = &price
	}
	routes := make([]RouteInput, 0, len(input.Routes))
	seenIDs := make(map[string]struct{}, len(input.Routes))
	seenTargets := make(map[string]struct{}, len(input.Routes))
	for _, route := range input.Routes {
		route.Identifier = strings.TrimSpace(route.Identifier)
		providerID, validProvider := common.NormalizeUUID(route.ProviderID)
		if !validProvider || !aimodel.ValidIdentifier(route.Identifier) {
			fields["routes"] = ValidationRoutesInvalid
			continue
		}
		route.ProviderID = providerID
		// 已有来源的编号须为不重复的 UUID。
		if route.ID != "" {
			id, valid := common.NormalizeUUID(route.ID)
			if _, duplicated := seenIDs[id]; !valid || duplicated {
				fields["routes"] = ValidationRoutesInvalid
				continue
			}
			route.ID = id
			seenIDs[id] = struct{}{}
		}
		target := route.ProviderID + "\n" + route.Identifier
		if _, duplicated := seenTargets[target]; duplicated {
			fields["routes"] = ValidationRouteDuplicate
			continue
		}
		seenTargets[target] = struct{}{}
		routes = append(routes, route)
	}
	if len(input.Routes) == 0 {
		fields["routes"] = ValidationRoutesInvalid
	}
	input.Routes = routes
	return input, fields
}

// lockModel 以 UPDATE 锁定平台模型，不存在时返回 ErrNotFound。
func lockModel(ctx context.Context, tx bun.Tx, modelID string) error {
	if !common.ValidUUID(modelID) {
		return ErrNotFound
	}
	var id string
	err := tx.NewSelect().Model((*servermodels.AIModel)(nil)).
		ColumnExpr("aim.id::text").
		Where("aim.id = ?", modelID).
		Where("aim.organization_id IS NULL").
		For("UPDATE").
		Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// saveRoutes 按请求顺序保存平台模型的来源：顺序即尝试优先级，未出现的已有来源被删除；来源供应商须为平台供应商并以 KEY SHARE 锁定至事务结束。
func saveRoutes(ctx context.Context, tx bun.Tx, modelID string, routes []RouteInput) error {
	providerIDs := make([]string, 0, len(routes))
	for _, route := range routes {
		providerIDs = append(providerIDs, route.ProviderID)
	}
	var lockedProviders []string
	if err := tx.NewSelect().Model((*servermodels.AIProvider)(nil)).
		ColumnExpr("aip.id::text").
		Where("aip.id IN (?)", bun.In(providerIDs)).
		Where("aip.organization_id IS NULL").
		For("KEY SHARE").
		Scan(ctx, &lockedProviders); err != nil {
		return err
	}
	locked := make(map[string]struct{}, len(lockedProviders))
	for _, id := range lockedProviders {
		locked[id] = struct{}{}
	}
	var storedIDs []string
	if err := tx.NewSelect().Model((*servermodels.AIModelRoute)(nil)).
		ColumnExpr("amr.id::text").
		Where("amr.model_id = ?", modelID).
		Scan(ctx, &storedIDs); err != nil {
		return err
	}
	stored := make(map[string]struct{}, len(storedIDs))
	for _, id := range storedIDs {
		stored[id] = struct{}{}
	}
	kept := make([]string, 0, len(routes))
	inserted := make([]servermodels.AIModelRoute, 0, len(routes))
	for priority, route := range routes {
		if _, ok := locked[route.ProviderID]; !ok {
			return &common.FieldError{Fields: map[string]ValidationCode{"routes": ValidationRoutesInvalid}}
		}
		record := servermodels.AIModelRoute{
			ID: route.ID, ModelID: modelID, ProviderID: route.ProviderID, Identifier: route.Identifier,
			Priority: priority, Enabled: route.Enabled,
		}
		if route.ID == "" {
			inserted = append(inserted, record)
			continue
		}
		if _, ok := stored[route.ID]; !ok {
			return &common.FieldError{Fields: map[string]ValidationCode{"routes": ValidationRoutesInvalid}}
		}
		kept = append(kept, route.ID)
		if _, err := tx.NewUpdate().Model(&record).
			Column("provider_id", "identifier", "priority", "enabled").
			Set("updated_at = now()").
			Where("model_id = ?", modelID).
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
	}
	deleted := tx.NewDelete().Model((*servermodels.AIModelRoute)(nil)).Where("model_id = ?", modelID)
	if len(kept) > 0 {
		deleted = deleted.Where("id NOT IN (?)", bun.In(kept))
	}
	if _, err := deleted.Exec(ctx); err != nil {
		return err
	}
	if len(inserted) == 0 {
		return nil
	}
	_, err := tx.NewInsert().Model(&inserted).
		Column("model_id", "provider_id", "identifier", "priority", "enabled").
		Exec(ctx)
	return err
}

// modelRow 定义平台模型目录查询的一行。
type modelRow struct {
	ID                 string          `bun:"id"`
	Name               string          `bun:"name"`
	Type               string          `bun:"model_type"`
	InputModalities    json.RawMessage `bun:"input_modalities"`
	ContextWindow      int64           `bun:"context_window"`
	MaxOutputTokens    int64           `bun:"max_output_tokens"`
	InputCreditPrice   *int64          `bun:"input_credit_price"`
	OutputCreditPrice  *int64          `bun:"output_credit_price"`
	RequestCreditPrice *int64          `bun:"request_credit_price"`
}

// loadRecords 按名称读取平台模型及其按优先级排列的来源，modelIDs 为空时读取全部平台模型。
func loadRecords(ctx context.Context, db bun.IDB, modelIDs []string) ([]Record, error) {
	rows := make([]modelRow, 0)
	query := db.NewSelect().TableExpr("ai_models AS aim").
		ColumnExpr("aim.id::text AS id, aim.name, aim.model_type, aim.input_modalities, aim.context_window, aim.max_output_tokens").
		ColumnExpr("aim.input_credit_price, aim.output_credit_price, aim.request_credit_price").
		Where("aim.organization_id IS NULL").
		OrderExpr("lower(aim.name) ASC, aim.id ASC")
	if modelIDs != nil {
		query = query.Where("aim.id IN (?)", bun.In(modelIDs))
	}
	if err := query.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(rows))
	if len(rows) == 0 {
		return records, nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	routes := make([]Route, 0)
	if err := db.NewSelect().TableExpr("ai_model_routes AS amr").
		ColumnExpr("amr.id::text AS id, amr.model_id::text AS model_id, amr.identifier, amr.enabled").
		ColumnExpr("aip.id::text AS provider_id, aip.name AS provider_name, aip.brand AS provider_brand").
		Join("JOIN ai_providers AS aip ON aip.id = amr.provider_id").
		Where("amr.model_id IN (?)", bun.In(ids)).
		OrderExpr("amr.priority ASC, amr.id ASC").
		Scan(ctx, &routes); err != nil {
		return nil, err
	}
	routesByModel := make(map[string][]Route, len(rows))
	for _, route := range routes {
		routesByModel[route.ModelID] = append(routesByModel[route.ModelID], route)
	}
	for _, row := range rows {
		inputModalities := make([]domain.AIModelInputModality, 0)
		if err := json.Unmarshal(row.InputModalities, &inputModalities); err != nil {
			return nil, fmt.Errorf("decode model %q input modalities: %w", row.ID, err)
		}
		var price *domain.CreditPrice
		if row.InputCreditPrice != nil {
			price = &domain.CreditPrice{Input: *row.InputCreditPrice, Output: *row.OutputCreditPrice, Request: *row.RequestCreditPrice}
		}
		records = append(records, Record{
			ID: row.ID, Price: price,
			Spec: aimodel.Spec{
				Name: row.Name, Type: domain.AIModelType(row.Type), InputModalities: inputModalities,
				ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens,
			},
			Routes: routesByModel[row.ID],
		})
	}
	return records, nil
}

// reload 读取写入后的平台模型详情。
func reload(ctx context.Context, db bun.IDB, modelID string) (*Record, error) {
	records, err := loadRecords(ctx, db, []string{modelID})
	if err != nil {
		return nil, fmt.Errorf("reload platform AI model: %w", err)
	}
	if len(records) == 0 {
		return nil, ErrNotFound
	}
	return &records[0], nil
}

// modelRecord 把模型属性与价格转换为平台模型存储模型，未定价时三项价格为空。
func modelRecord(input Input) (servermodels.AIModel, error) {
	inputModalities, err := json.Marshal(input.InputModalities)
	if err != nil {
		return servermodels.AIModel{}, err
	}
	record := servermodels.AIModel{
		Name: input.Name, Type: string(input.Type), InputModalities: inputModalities,
		ContextWindow: input.ContextWindow, MaxOutputTokens: input.MaxOutputTokens,
	}
	if input.Price != nil {
		record.InputCreditPrice, record.OutputCreditPrice, record.RequestCreditPrice = &input.Price.Input, &input.Price.Output, &input.Price.Request
	}
	return record, nil
}

// conflictError 把平台模型名称或来源的唯一约束冲突转换为字段校验错误，其他错误返回 nil。
func conflictError(err error) error {
	switch {
	case pgerr.UniqueViolationOn(err, "ai_models_platform_name_unique"):
		return &common.FieldError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	case pgerr.UniqueViolationOn(err, "ai_model_routes_provider_identifier_unique"):
		return &common.FieldError{Fields: map[string]ValidationCode{"routes": ValidationRouteDuplicate}}
	default:
		return nil
	}
}
