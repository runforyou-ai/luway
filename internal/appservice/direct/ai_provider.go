//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	aiprovideraction "github.com/runforyou-ai/cervi/internal/actions/aiprovider"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// ListAIProviders 返回当前企业的模型服务供应商列表。
func (o *directOperations) ListAIProviders(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.AIProviderList, error) {
	providers, err := o.listAIProviders.Execute(ctx, identity)
	if err != nil {
		return appservice.AIProviderList{}, o.aiProviderError(ctx, meta, err, i18n.ErrorAIProviderListFailed, identity.Organization.ID)
	}
	output := make([]appservice.AIProviderSummary, 0, len(providers))
	for _, provider := range providers {
		models := make([]appservice.AIProviderModelSummary, 0, len(provider.Models))
		for _, model := range provider.Models {
			models = append(models, appservice.AIProviderModelSummary{
				Identifier: model.Identifier,
				Name:       model.Name,
				Type:       appservice.AIModelType(model.Type),
			})
		}
		output = append(output, appservice.AIProviderSummary{
			ID: provider.ID, Brand: appservice.AIProviderBrand(provider.Brand), Name: provider.Name, APIURL: provider.APIURL,
			Models: models,
		})
	}
	return appservice.AIProviderList{Providers: output}, nil
}

// GetAIProvider 返回当前企业中的模型服务供应商详情。
func (o *directOperations) GetAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, providerID string) (appservice.AIProvider, error) {
	provider, err := o.getAIProvider.Execute(ctx, identity, providerID)
	if err != nil {
		return appservice.AIProvider{}, o.aiProviderError(ctx, meta, err, i18n.ErrorAIProviderReadFailed, identity.Organization.ID, "provider_id", providerID)
	}
	return aiProviderFromAction(*provider), nil
}

// ListAvailableAIModels 返回指定品牌的预设模型目录，模型目录由服务实例决定的品牌返回空目录。
func (o *directOperations) ListAvailableAIModels(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, brand appservice.AIProviderBrand) (appservice.AIProviderModelList, error) {
	if !domain.ValidAIProviderBrand(domain.AIProviderBrand(brand)) {
		fields := map[string]i18n.Key{"brand": i18n.FieldAIProviderBrandInvalid}
		return appservice.AIProviderModelList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, fields)
	}
	return appservice.AIProviderModelList{Models: aiProviderModelsFromAction(aiprovideraction.AvailableModels(domain.AIProviderBrand(brand)))}, nil
}

// DiscoverAIProviderModels 读取模型服务实例当前可用的模型目录。
func (o *directOperations) DiscoverAIProviderModels(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, input appservice.AIProviderConnectionInput) (appservice.AIProviderModelList, error) {
	models, err := o.discoverAIProviderModels.Execute(ctx, aiProviderConnectionInput(input))
	if err != nil {
		return appservice.AIProviderModelList{}, o.aiProviderConnectionError(ctx, meta, err, input.Brand)
	}
	return appservice.AIProviderModelList{Models: aiProviderModelsFromAction(models)}, nil
}

// TestAIProviderConnection 测试模型服务供应商草稿配置。
func (o *directOperations) TestAIProviderConnection(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, input appservice.AIProviderConnectionInput) error {
	err := o.testAIProviderConnection.Execute(ctx, aiProviderConnectionInput(input))
	if err == nil {
		return nil
	}
	return o.aiProviderConnectionError(ctx, meta, err, input.Brand)
}

// aiProviderConnectionError 转换访问模型服务实例产生的校验和连接错误。
func (o *directOperations) aiProviderConnectionError(ctx context.Context, meta appservice.RequestMeta, err error, brand appservice.AIProviderBrand) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, aiProviderFieldKeys(validationError.Fields))
	}
	_, kind, classified := connectiontest.Details(err)
	if !classified {
		slog.Warn("模型服务调用返回未分类错误", "brand", brand)
		return appservice.UnavailableError(meta, i18n.ErrorAIProviderConnectionTestFailed, nil)
	}
	switch kind {
	case connectiontest.FailureUnauthorized:
		return appservice.UnavailableError(meta, i18n.ErrorAIProviderAuthenticationFailed, nil)
	case connectiontest.FailureForbidden:
		return appservice.UnavailableError(meta, i18n.ErrorAIProviderAuthorizationFailed, nil)
	case connectiontest.FailureRateLimited:
		return appservice.UnavailableError(meta, i18n.ErrorAIProviderRateLimited, nil)
	default:
		return appservice.UnavailableError(meta, i18n.ErrorAIProviderConnectionTestFailed, nil)
	}
}

// aiProviderConnectionInput 转换模型服务连接草稿配置。
func aiProviderConnectionInput(input appservice.AIProviderConnectionInput) aiprovideraction.ConnectionInput {
	return aiprovideraction.ConnectionInput{
		Brand:          domain.AIProviderBrand(input.Brand),
		CredentialType: domain.AIProviderCredentialType(input.CredentialType),
		APIKey:         input.APIKey,
		APIURL:         input.APIURL,
	}
}

// CreateAIProvider 创建模型服务供应商。
func (o *directOperations) CreateAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIProviderInput) (appservice.AIProvider, error) {
	provider, err := o.createAIProvider.Execute(ctx, identity, aiProviderInput(input))
	if err != nil {
		return appservice.AIProvider{}, o.aiProviderMutationError(ctx, meta, err, i18n.ErrorAIProviderCreateFailed, identity.Organization.ID)
	}
	slog.Info("模型服务供应商创建成功", "organization_id", identity.Organization.ID, "provider_id", provider.ID, "brand", provider.Brand, "model_count", len(provider.Models))
	return aiProviderFromAction(*provider), nil
}

// UpdateAIProvider 修改模型服务供应商。
func (o *directOperations) UpdateAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, providerID string, input appservice.AIProviderUpdateInput) (appservice.AIProvider, error) {
	provider, err := o.updateAIProvider.Execute(ctx, identity, providerID, aiprovideraction.UpdateInput{
		Name: input.Name, CredentialType: domain.AIProviderCredentialType(input.CredentialType),
		APIKey: input.APIKey, APIURL: input.APIURL, Models: aiProviderModelsInput(input.Models),
	})
	if err != nil {
		return appservice.AIProvider{}, o.aiProviderMutationError(ctx, meta, err, i18n.ErrorAIProviderUpdateFailed, identity.Organization.ID, "provider_id", providerID)
	}
	slog.Info("模型服务供应商保存成功", "organization_id", identity.Organization.ID, "provider_id", provider.ID, "brand", provider.Brand, "model_count", len(provider.Models))
	return aiProviderFromAction(*provider), nil
}

// DeleteAIProvider 删除模型服务供应商。
func (o *directOperations) DeleteAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, providerID string) error {
	if err := o.deleteAIProvider.Execute(ctx, identity, providerID); err != nil {
		return o.aiProviderError(ctx, meta, err, i18n.ErrorAIProviderDeleteFailed, identity.Organization.ID, "provider_id", providerID)
	}
	slog.Info("模型服务供应商删除成功", "organization_id", identity.Organization.ID, "provider_id", providerID)
	return nil
}

// aiProviderMutationError 转换模型服务供应商写入错误。
func (o *directOperations) aiProviderMutationError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID string, attributes ...any) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, aiProviderFieldKeys(validationError.Fields))
	}
	return o.aiProviderError(ctx, meta, err, failureKey, organizationID, attributes...)
}

// aiProviderError 转换模型服务供应商操作错误。
func (o *directOperations) aiProviderError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID string, attributes ...any) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, aiprovideraction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorAIProviderNotFound)
	}
	if errors.Is(err, aiprovideraction.ErrInUse) {
		return appservice.InvalidError(meta, i18n.ErrorAIProviderInUse, nil)
	}
	logAttributes := []any{"organization_id", organizationID, "failure", failureKey, "error", err}
	slog.Warn("模型服务供应商操作失败", append(logAttributes, attributes...)...)
	return appservice.FailedError(meta, failureKey)
}

// aiProviderInput 转换模型服务供应商输入。
func aiProviderInput(input appservice.AIProviderInput) aiprovideraction.Input {
	return aiprovideraction.Input{
		Brand: domain.AIProviderBrand(input.Brand), Name: input.Name,
		CredentialType: domain.AIProviderCredentialType(input.CredentialType),
		APIKey:         input.APIKey, APIURL: input.APIURL, Models: aiProviderModelsInput(input.Models),
	}
}

// aiProviderModelsInput 转换模型目录输入。
func aiProviderModelsInput(input []appservice.AIProviderModel) []aiprovideraction.Model {
	models := make([]aiprovideraction.Model, 0, len(input))
	for _, model := range input {
		// 转换模型输入模态到领域值。
		inputModalities := make([]domain.AIModelInputModality, 0, len(model.InputModalities))
		for _, modality := range model.InputModalities {
			inputModalities = append(inputModalities, domain.AIModelInputModality(modality))
		}
		models = append(models, aiprovideraction.Model{
			Identifier: model.Identifier, Name: model.Name, Type: domain.AIModelType(model.Type),
			InputModalities: inputModalities,
			ContextWindow:   model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
		})
	}
	return models
}

// aiProviderFromAction 转换模型服务供应商输出。
func aiProviderFromAction(input aiprovideraction.Record) appservice.AIProvider {
	return appservice.AIProvider{
		ID: input.ID, Brand: appservice.AIProviderBrand(input.Brand), Name: input.Name,
		CredentialType: appservice.AIProviderCredentialType(input.CredentialType),
		APIKey:         input.APIKey, APIURL: input.APIURL,
		Models: aiProviderModelsFromAction(input.Models),
	}
}

// aiProviderModelsFromAction 转换 AI 模型目录。
func aiProviderModelsFromAction(input []aiprovideraction.Model) []appservice.AIProviderModel {
	models := make([]appservice.AIProviderModel, 0, len(input))
	for _, model := range input {
		// 转换领域模型输入模态到应用契约。
		inputModalities := make([]appservice.AIModelInputModality, 0, len(model.InputModalities))
		for _, modality := range model.InputModalities {
			inputModalities = append(inputModalities, appservice.AIModelInputModality(modality))
		}
		models = append(models, appservice.AIProviderModel{
			Identifier: model.Identifier, Name: model.Name, Type: appservice.AIModelType(model.Type),
			InputModalities: inputModalities,
			ContextWindow:   model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
		})
	}
	return models
}

// aiProviderFieldKeys 映射模型服务供应商校验错误。
func aiProviderFieldKeys(fields map[string]common.FieldCode) map[string]i18n.Key {
	keys := map[common.FieldCode]i18n.Key{
		aiprovideraction.ValidationBrandInvalid:          i18n.FieldAIProviderBrandInvalid,
		aiprovideraction.ValidationCredentialTypeInvalid: i18n.FieldAIProviderCredentialTypeInvalid,
		aiprovideraction.ValidationNameRequired:          i18n.FieldAIProviderNameRequired,
		aiprovideraction.ValidationNameTooLong:           i18n.FieldAIProviderNameTooLong,
		aiprovideraction.ValidationNameDuplicate:         i18n.FieldAIProviderNameDuplicate,
		aiprovideraction.ValidationAPIKeyRequired:        i18n.FieldAPIKeyRequired,
		aiprovideraction.ValidationAPIKeyTooLong:         i18n.FieldAIProviderAPIKeyTooLong,
		aiprovideraction.ValidationAPIURLRequired:        i18n.FieldAIProviderAPIURLRequired,
		aiprovideraction.ValidationAPIURLInvalid:         i18n.FieldAIProviderAPIURLInvalid,
		aiprovideraction.ValidationModelsInvalid:         i18n.FieldAIProviderModelsInvalid,
		aiprovideraction.ValidationModelsInUse:           i18n.FieldAIProviderModelsInUse,
	}
	return translateValidationFields(fields, keys)
}
