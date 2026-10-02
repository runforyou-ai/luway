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

// loadModels 按添加顺序读取供应商的模型目录。
func loadModels(ctx context.Context, db bun.IDB, providerID string) ([]Model, error) {
	records := make([]servermodels.AIModel, 0)
	if err := db.NewSelect().
		Model(&records).
		Where("aim.provider_id = ?", providerID).
		Order("aim.id ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(records))
	for _, record := range records {
		inputModalities := make([]domain.AIModelInputModality, 0)
		if err := json.Unmarshal(record.InputModalities, &inputModalities); err != nil {
			return nil, fmt.Errorf("decode model %q input modalities: %w", record.ID, err)
		}
		models = append(models, Model{
			ID: record.ID, Identifier: record.Identifier, Name: record.Name, Type: domain.AIModelType(record.Type),
			InputModalities: inputModalities, ContextWindow: record.ContextWindow, MaxOutputTokens: record.MaxOutputTokens,
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

// saveModels 按增删改保存供应商模型目录，返回带编号的完整目录。
func saveModels(ctx context.Context, tx bun.Tx, providerID string, models []Model, changes modelChanges) ([]Model, error) {
	if len(changes.deleted) > 0 {
		if _, err := tx.NewDelete().Model((*servermodels.AIModel)(nil)).
			Where("provider_id = ?", providerID).
			Where("id IN (?)", bun.In(changes.deleted)).
			Exec(ctx); err != nil {
			return nil, err
		}
	}
	for _, model := range changes.updated {
		record, err := modelRecord(providerID, model)
		if err != nil {
			return nil, err
		}
		if _, err := tx.NewUpdate().Model(&record).
			Column("identifier", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").
			Set("updated_at = now()").
			Where("provider_id = ?", providerID).
			WherePK().
			Exec(ctx); err != nil {
			return nil, err
		}
	}
	inserted := make([]servermodels.AIModel, 0, len(changes.inserted))
	for _, model := range changes.inserted {
		record, err := modelRecord(providerID, model)
		if err != nil {
			return nil, err
		}
		inserted = append(inserted, record)
	}
	if len(inserted) > 0 {
		if _, err := tx.NewInsert().Model(&inserted).
			Column("provider_id", "identifier", "name", "model_type", "input_modalities", "context_window", "max_output_tokens").
			Returning("id").
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

// modelRecord 把模型目录项转换为存储模型。
func modelRecord(providerID string, model Model) (servermodels.AIModel, error) {
	inputModalities, err := json.Marshal(model.InputModalities)
	if err != nil {
		return servermodels.AIModel{}, err
	}
	return servermodels.AIModel{
		ID: model.ID, ProviderID: providerID, Identifier: model.Identifier, Name: model.Name, Type: string(model.Type),
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
	case pgerr.UniqueViolationOn(err, "ai_models_provider_identifier_unique"):
		return &ValidationError{Fields: map[string]ValidationCode{"models": ValidationModelsInvalid}}
	default:
		return nil
	}
}
