//go:build server

package aiprovider

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// EmbeddingReindexer 在业务事务中重新索引引用指定向量模型的知识库。
type EmbeddingReindexer interface {
	// ReindexEmbeddingModels 清空引用这些向量模型的知识库分段并按当前配置重新投递索引。
	ReindexEmbeddingModels(ctx context.Context, tx bun.Tx, organizationID string, modelIDs []string) error
}

// loadProvider 读取当前企业中的模型服务供应商。
func loadProvider(ctx context.Context, db bun.IDB, organizationID, providerID string, lock bool) (*servermodels.AIProvider, error) {
	if !common.ValidUUID(providerID) {
		return nil, ErrNotFound
	}
	provider := &servermodels.AIProvider{}
	query := db.NewSelect().
		Model(provider).
		Where("aip.id = ?", providerID).
		Where("aip.organization_id = ?", organizationID)
	if lock {
		query = query.For("UPDATE")
	}
	if err := query.Scan(ctx); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return provider, nil
}

// modelRow 定义供应商模型目录查询的一行：模型与指向该供应商的来源路由。
type modelRow struct {
	ID              string          `bun:"id"`
	Identifier      string          `bun:"identifier"`
	Name            string          `bun:"name"`
	Type            string          `bun:"model_type"`
	InputModalities json.RawMessage `bun:"input_modalities"`
	ContextWindow   int64           `bun:"context_window"`
	MaxOutputTokens int64           `bun:"max_output_tokens"`
}

// loadModels 按添加顺序读取供应商的模型目录，上游模型标识取指向该供应商的来源路由。
func loadModels(ctx context.Context, db bun.IDB, providerID string) ([]Model, error) {
	rows := make([]modelRow, 0)
	if err := db.NewSelect().TableExpr("ai_model_routes AS amr").
		ColumnExpr("aim.id::text AS id, amr.identifier, aim.name, aim.model_type, aim.input_modalities, aim.context_window, aim.max_output_tokens").
		Join("JOIN ai_models AS aim ON aim.id = amr.model_id").
		Where("amr.provider_id = ?", providerID).
		OrderExpr("aim.id ASC").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(rows))
	for _, row := range rows {
		inputModalities := make([]domain.AIModelInputModality, 0)
		if err := json.Unmarshal(row.InputModalities, &inputModalities); err != nil {
			return nil, fmt.Errorf("decode model %q input modalities: %w", row.ID, err)
		}
		models = append(models, Model{
			ID: row.ID, Identifier: row.Identifier, Name: row.Name, Type: domain.AIModelType(row.Type),
			InputModalities: inputModalities, ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens,
		})
	}
	return models, nil
}

// modelChanges 定义一次模型目录保存的增删改。
type modelChanges struct {
	inserted []Model
	updated  []Model
	deleted  []string
	// renamedEmbeddings 是上游模型标识发生变化的向量模型编号。
	renamedEmbeddings []string
}

// diffModels 按模型编号比较已保存目录与新目录；新目录引用不存在的编号时返回校验错误。
func diffModels(stored, models []Model) (modelChanges, error) {
	storedByID := make(map[string]Model, len(stored))
	for _, model := range stored {
		storedByID[model.ID] = model
	}
	changes := modelChanges{}
	kept := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model.ID == "" {
			changes.inserted = append(changes.inserted, model)
			continue
		}
		previous, exists := storedByID[model.ID]
		if !exists {
			return modelChanges{}, &ValidationError{Fields: map[string]ValidationCode{"models": ValidationModelsInvalid}}
		}
		kept[model.ID] = struct{}{}
		changes.updated = append(changes.updated, model)
		if previous.Type == domain.AIModelTypeEmbedding && model.Type == domain.AIModelTypeEmbedding && previous.Identifier != model.Identifier {
			changes.renamedEmbeddings = append(changes.renamedEmbeddings, model.ID)
		}
	}
	for _, model := range stored {
		if _, exists := kept[model.ID]; !exists {
			changes.deleted = append(changes.deleted, model.ID)
		}
	}
	return changes, nil
}

// storedReferences 读取业务配置对供应商已保存模型的全部引用。
func storedReferences(ctx context.Context, db bun.IDB, organizationID string, stored []Model) ([]aimodel.Reference, error) {
	ids := make([]string, 0, len(stored))
	for _, model := range stored {
		ids = append(ids, model.ID)
	}
	return aimodel.References(ctx, db, organizationID, ids)
}

// validateReferencedModels 校验业务配置引用的模型仍保留在新目录中且满足引用用途。
func validateReferencedModels(ctx context.Context, db bun.IDB, organizationID string, stored, models []Model) error {
	references, err := storedReferences(ctx, db, organizationID, stored)
	if err != nil {
		return err
	}
	modelByID := make(map[string]Model, len(models))
	for _, model := range models {
		modelByID[model.ID] = model
	}
	for _, reference := range references {
		model, exists := modelByID[reference.ModelID]
		if !exists || !aimodel.Satisfies(reference.Usage, model.Type, model.InputModalities) {
			return &ValidationError{Fields: map[string]ValidationCode{"models": ValidationModelsInUse}}
		}
	}
	return nil
}

// saveModels 按增删改保存工作区供应商的模型目录：每个模型属于该工作区，并以唯一一条来源路由指向该供应商；返回带编号的完整目录。
func saveModels(ctx context.Context, tx bun.Tx, organizationID, providerID string, models []Model, changes modelChanges) ([]Model, error) {
	if err := deleteModels(ctx, tx, organizationID, changes.deleted); err != nil {
		return nil, err
	}
	for _, model := range changes.updated {
		record, err := modelRecord(organizationID, model)
		if err != nil {
			return nil, err
		}
		if _, err := tx.NewUpdate().Model(&record).
			Column("name", "model_type", "input_modalities", "context_window", "max_output_tokens").
			Set("updated_at = now()").
			Where("organization_id = ?", organizationID).
			WherePK().
			Exec(ctx); err != nil {
			return nil, err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.AIModelRoute)(nil)).
			Set("identifier = ?", model.Identifier).
			Set("updated_at = now()").
			Where("provider_id = ?", providerID).
			Where("model_id = ?", model.ID).
			Exec(ctx); err != nil {
			return nil, err
		}
	}
	inserted := make([]servermodels.AIModel, 0, len(changes.inserted))
	for _, model := range changes.inserted {
		record, err := modelRecord(organizationID, model)
		if err != nil {
			return nil, err
		}
		inserted = append(inserted, record)
	}
	if len(inserted) > 0 {
		if _, err := tx.NewInsert().Model(&inserted).
			Column("organization_id", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").
			Returning("id").
			Exec(ctx); err != nil {
			return nil, err
		}
		routes := make([]servermodels.AIModelRoute, 0, len(inserted))
		for index, record := range inserted {
			routes = append(routes, servermodels.AIModelRoute{ModelID: record.ID, ProviderID: providerID, Identifier: changes.inserted[index].Identifier, Enabled: true})
		}
		if _, err := tx.NewInsert().Model(&routes).
			Column("model_id", "provider_id", "identifier", "priority", "enabled").
			Exec(ctx); err != nil {
			return nil, err
		}
	}
	// 按请求顺序为新增模型补齐编号。
	saved := make([]Model, len(models))
	next := 0
	for index, model := range models {
		if model.ID == "" {
			model.ID = inserted[next].ID
			next++
		}
		saved[index] = model
	}
	return saved, nil
}

// deleteModels 删除工作区模型及其全部来源路由。
func deleteModels(ctx context.Context, tx bun.Tx, organizationID string, modelIDs []string) error {
	if len(modelIDs) == 0 {
		return nil
	}
	if _, err := tx.NewDelete().Model((*servermodels.AIModelRoute)(nil)).
		Where("model_id IN (SELECT id FROM ai_models WHERE organization_id = ? AND id IN (?))", organizationID, bun.In(modelIDs)).
		Exec(ctx); err != nil {
		return err
	}
	_, err := tx.NewDelete().Model((*servermodels.AIModel)(nil)).
		Where("organization_id = ?", organizationID).
		Where("id IN (?)", bun.In(modelIDs)).
		Exec(ctx)
	return err
}

// modelRecord 把模型目录项转换为属于工作区的存储模型。
func modelRecord(organizationID string, model Model) (servermodels.AIModel, error) {
	inputModalities, err := json.Marshal(model.InputModalities)
	if err != nil {
		return servermodels.AIModel{}, err
	}
	return servermodels.AIModel{
		ID: model.ID, OrganizationID: &organizationID, Name: model.Name, Type: string(model.Type),
		InputModalities: inputModalities, ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
	}, nil
}

// recordFromModel 转换模型服务供应商存储模型。
func recordFromModel(provider servermodels.AIProvider, models []Model) Record {
	return Record{
		ID: provider.ID, Brand: domain.AIProviderBrand(provider.Brand), Name: provider.Name,
		CredentialType: domain.AIProviderCredentialType(provider.CredentialType),
		APIKey:         provider.APIKey, APIURL: provider.APIURL, Models: models,
	}
}

// conflictError 把供应商名称或模型标识的唯一约束冲突转换为字段校验错误，其他错误返回 nil。
func conflictError(err error) error {
	switch {
	case pgerr.UniqueViolationOn(err, "ai_providers_organization_name_unique"):
		return &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	case pgerr.UniqueViolationOn(err, "ai_model_routes_provider_identifier_unique"):
		return &ValidationError{Fields: map[string]ValidationCode{"models": ValidationModelsInvalid}}
	default:
		return nil
	}
}
