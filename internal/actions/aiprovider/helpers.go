//go:build server

package aiprovider

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

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

// loadModels 读取供应商的模型目录。
func loadModels(ctx context.Context, db bun.IDB, organizationID, providerID string) ([]Model, error) {
	records := make([]servermodels.AIProviderModel, 0)
	if err := db.NewSelect().
		Model(&records).
		Where("aipm.organization_id = ?", organizationID).
		Where("aipm.provider_id = ?", providerID).
		Order("aipm.created_at ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(records))
	for _, record := range records {
		inputModalities := make([]domain.AIModelInputModality, 0)
		if err := json.Unmarshal(record.InputModalities, &inputModalities); err != nil {
			return nil, fmt.Errorf("decode model %q input modalities: %w", record.Identifier, err)
		}
		models = append(models, Model{
			Identifier: record.Identifier, Name: record.Name, Type: domain.AIModelType(record.Type),
			InputModalities: inputModalities, ContextWindow: record.ContextWindow, MaxOutputTokens: record.MaxOutputTokens,
		})
	}
	return models, nil
}

// replaceModels 替换供应商的全部已启用模型。
func replaceModels(ctx context.Context, tx bun.Tx, organizationID, providerID string, models []Model) error {
	if _, err := tx.NewDelete().
		Model((*servermodels.AIProviderModel)(nil)).
		Where("organization_id = ?", organizationID).
		Where("provider_id = ?", providerID).
		Exec(ctx); err != nil {
		return err
	}
	records := make([]servermodels.AIProviderModel, 0, len(models))
	for _, model := range models {
		inputModalities, err := json.Marshal(model.InputModalities)
		if err != nil {
			return err
		}
		records = append(records, servermodels.AIProviderModel{
			ProviderID: providerID, OrganizationID: organizationID, Identifier: model.Identifier,
			Name: model.Name, Type: string(model.Type), ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
			InputModalities: inputModalities,
		})
	}
	_, err := tx.NewInsert().
		Model(&records).
		Column(
			"provider_id", "organization_id", "identifier", "name", "model_type", "input_modalities",
			"context_window", "max_output_tokens",
		).
		Exec(ctx)
	return err
}

// modelReference 表示业务配置对供应商模型的一处引用，字段为模型标识和该用途要求的模型类型与文本输入。
type modelReference struct {
	Identifier   string
	Type         domain.AIModelType
	RequiresText bool
}

// providerReferences 读取 AI 员工当前版本、知识库和客服设置对指定供应商模型的全部引用。
func providerReferences(ctx context.Context, db bun.IDB, organizationID, providerID string) ([]modelReference, error) {
	references := make([]modelReference, 0)
	// AI 员工当前版本引用的对话模型须支持文本输入。
	agentIdentifiers := make([]string, 0)
	if err := db.NewSelect().TableExpr("agents AS a").
		ColumnExpr("DISTINCT ar.configuration #>> '{model,identifier}'").
		Join("JOIN agent_revisions AS ar ON ar.id = a.active_revision_id AND ar.organization_id = a.organization_id AND ar.agent_id = a.id").
		Where("a.organization_id = ?", organizationID).
		Where("ar.execution_mode = ?", domain.AgentExecutionModeManaged).
		Where("ar.configuration #>> '{model,providerId}' = ?", providerID).
		Scan(ctx, &agentIdentifiers); err != nil {
		return nil, err
	}
	for _, identifier := range agentIdentifiers {
		references = append(references, modelReference{Identifier: identifier, Type: domain.AIModelTypeChat, RequiresText: true})
	}
	// 知识库引用向量模型和重排模型。
	bases := make([]servermodels.KnowledgeBase, 0)
	if err := db.NewSelect().Model(&bases).Where("organization_id = ?", organizationID).
		Where("embedding_provider_id = ? OR rerank_provider_id = ?", providerID, providerID).Scan(ctx); err != nil {
		return nil, err
	}
	for _, base := range bases {
		if base.EmbeddingProviderID == providerID {
			references = append(references, modelReference{Identifier: base.EmbeddingModelIdentifier, Type: domain.AIModelTypeEmbedding})
		}
		if base.RerankProviderID == providerID {
			references = append(references, modelReference{Identifier: base.RerankModelIdentifier, Type: domain.AIModelTypeRerank})
		}
	}
	// 客服设置引用判断模型，以及须支持文本输入的小结模型和翻译模型。
	setting := &servermodels.CustomerServiceSetting{}
	err := db.NewSelect().Model(setting).
		Column("decision_provider_id", "decision_model_identifier", "summary_provider_id", "summary_model_identifier",
			"translation_provider_id", "translation_model_identifier").
		Where("organization_id = ?", organizationID).
		Where("decision_provider_id = ? OR summary_provider_id = ? OR translation_provider_id = ?", providerID, providerID, providerID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return references, nil
	}
	if err != nil {
		return nil, err
	}
	for _, reference := range []struct {
		providerID, identifier *string
		modelType              domain.AIModelType
	}{
		{setting.DecisionProviderID, setting.DecisionModelIdentifier, domain.AIModelTypeDecision},
		{setting.SummaryProviderID, setting.SummaryModelIdentifier, domain.AIModelTypeChat},
		{setting.TranslationProviderID, setting.TranslationModelIdentifier, domain.AIModelTypeChat},
	} {
		if reference.providerID != nil && *reference.providerID == providerID && reference.identifier != nil {
			references = append(references, modelReference{Identifier: *reference.identifier, Type: reference.modelType, RequiresText: reference.modelType == domain.AIModelTypeChat})
		}
	}
	return references, nil
}

// validateReferencedModels 校验新目录按原有用途保留供应商被引用的全部模型。
func validateReferencedModels(ctx context.Context, db bun.IDB, organizationID, providerID string, models []Model) error {
	references, err := providerReferences(ctx, db, organizationID, providerID)
	if err != nil {
		return err
	}
	for _, reference := range references {
		found := false
		for _, model := range models {
			textInput := !reference.RequiresText || slices.Contains(model.InputModalities, domain.AIModelInputModalityText)
			found = found || (model.Identifier == reference.Identifier && model.Type == reference.Type && textInput)
		}
		if !found {
			return &ValidationError{Fields: map[string]ValidationCode{"models": ValidationModelsInUse}}
		}
	}
	return nil
}

// recordFromModel 转换模型服务供应商存储模型。
func recordFromModel(provider servermodels.AIProvider, models []Model) Record {
	return Record{
		ID: provider.ID, Brand: domain.AIProviderBrand(provider.Brand), Name: provider.Name,
		CredentialType: domain.AIProviderCredentialType(provider.CredentialType),
		APIKey:         provider.APIKey, APIURL: provider.APIURL, Models: models,
	}
}

// isNameConflict 判断企业内供应商名称是否重复。
func isNameConflict(err error) bool {
	return pgerr.UniqueViolationOn(err, "ai_providers_organization_name_unique")
}
