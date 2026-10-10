//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	aimodelaction "github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
)

// ListAIModelOptions 返回当前工作区满足指定用途的模型。
func (o *integrationOps) ListAIModelOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, usage appservice.AIModelUsage) (appservice.AIModelOptionList, error) {
	options, err := o.listAIModelOptions.Execute(ctx, identity, usage)
	if errors.Is(err, aimodelaction.ErrUsageInvalid) {
		return appservice.AIModelOptionList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{"usage": i18n.FieldAIModelUsageInvalid})
	}
	if err != nil {
		return appservice.AIModelOptionList{}, aiProviderError(meta, err, i18n.ErrorAIModelListFailed)
	}
	output := arr.Map(options, aiModelOptionFromAction)
	return appservice.AIModelOptionList{Models: output}, nil
}

// aiModelOptionFromAction 转换模型选项。
func aiModelOptionFromAction(option aimodelaction.Option) appservice.AIModelOption {
	inputModalities := append(make([]appservice.AIModelInputModality, 0, len(option.InputModalities)), option.InputModalities...)
	output := appservice.AIModelOption{
		ID: option.ID, Scope: option.Scope, Name: option.Name, Type: option.Type,
		InputModalities: inputModalities,
	}
	if option.Scope == domain.AIModelScopeWorkspace {
		output.Provider = &appservice.AIModelOptionProvider{ID: option.ProviderID, Name: option.ProviderName, Brand: option.Brand}
	}
	return output
}

// ListAIProviders 返回当前企业的模型服务供应商列表。
func (o *integrationOps) ListAIProviders(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.AIProviderList, error) {
	providers, err := o.listAIProviders.Execute(ctx, identity)
	if err != nil {
		return appservice.AIProviderList{}, aiProviderError(meta, err, i18n.ErrorAIProviderListFailed)
	}
	output := make([]appservice.AIProviderSummary, 0, len(providers))
	for _, provider := range providers {
		models := arr.Map(provider.Models, func(model aiprovideraction.ModelSummary) appservice.AIProviderModelSummary {
			return appservice.AIProviderModelSummary{
				ID:         model.ID,
				Identifier: model.Identifier,
				Name:       model.Name,
				Type:       model.Type,
			}
		})
		output = append(output, appservice.AIProviderSummary{
			ID: provider.ID, Brand: provider.Brand, Name: provider.Name, APIURL: provider.APIURL,
			Models: models,
		})
	}
	return appservice.AIProviderList{Providers: output}, nil
}

// GetAIProvider 返回当前企业中的模型服务供应商详情。
func (o *integrationOps) GetAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, providerID string) (appservice.AIProvider, error) {
	provider, err := o.getAIProvider.Execute(ctx, identity, providerID)
	if err != nil {
		return appservice.AIProvider{}, aiProviderError(meta, err, i18n.ErrorAIProviderReadFailed)
	}
	return aiProviderFromAction(*provider), nil
}

// ListAvailableAIModels 返回指定品牌的预设模型目录，模型目录由服务实例决定的品牌返回空目录。
func (o *integrationOps) ListAvailableAIModels(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, brand appservice.AIProviderBrand) (appservice.AIProviderModelList, error) {
	if !domain.ValidAIProviderBrand(brand) {
		fields := map[string]i18n.Key{"brand": i18n.FieldAIProviderBrandInvalid}
		return appservice.AIProviderModelList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, fields)
	}
	return appservice.AIProviderModelList{Models: aiProviderModelsFromAction(aiprovideraction.AvailableModels(brand))}, nil
}

// DiscoverAIProviderModels 读取模型服务实例当前可用的模型目录。
func (o *integrationOps) DiscoverAIProviderModels(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, input appservice.AIProviderConnectionInput) (appservice.AIProviderModelList, error) {
	models, err := o.discoverAIProviderModels.Execute(ctx, aiProviderConnectionInput(input))
	if err != nil {
		return appservice.AIProviderModelList{}, dispatch.AIProviderConnectionError(ctx, meta, err, input.Brand)
	}
	return appservice.AIProviderModelList{Models: aiProviderModelsFromAction(models)}, nil
}

// TestAIProviderConnection 测试模型服务供应商草稿配置。
func (o *integrationOps) TestAIProviderConnection(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, input appservice.AIProviderConnectionInput) error {
	err := o.testAIProviderConnection.Execute(ctx, aiProviderConnectionInput(input))
	if err == nil {
		return nil
	}
	return dispatch.AIProviderConnectionError(ctx, meta, err, input.Brand)
}

// aiProviderConnectionInput 转换模型服务连接草稿配置。
func aiProviderConnectionInput(input appservice.AIProviderConnectionInput) aiprovideraction.ConnectionInput {
	return aiprovideraction.ConnectionInput{
		Brand:          input.Brand,
		CredentialType: input.CredentialType,
		APIKey:         input.APIKey,
		APIURL:         input.APIURL,
	}
}

// CreateAIProvider 创建模型服务供应商。
func (o *integrationOps) CreateAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.AIProviderInput) (appservice.AIProvider, error) {
	provider, err := o.createAIProvider.Execute(ctx, identity, aiProviderInput(input))
	if err != nil {
		return appservice.AIProvider{}, aiProviderMutationError(meta, err, i18n.ErrorAIProviderCreateFailed)
	}
	slog.InfoContext(ctx, "模型服务供应商创建成功", "provider_id", provider.ID, "brand", provider.Brand, "model_count", len(provider.Models))
	return aiProviderFromAction(*provider), nil
}

// UpdateAIProvider 修改模型服务供应商。
func (o *integrationOps) UpdateAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, providerID string, input appservice.AIProviderUpdateInput) (appservice.AIProvider, error) {
	provider, err := o.updateAIProvider.Execute(ctx, identity, providerID, aiprovideraction.UpdateInput{
		Name: input.Name, CredentialType: input.CredentialType,
		APIKey: input.APIKey, APIURL: input.APIURL, Models: aiProviderModelsInput(input.Models),
	})
	if err != nil {
		return appservice.AIProvider{}, aiProviderMutationError(meta, err, i18n.ErrorAIProviderUpdateFailed)
	}
	slog.InfoContext(ctx, "模型服务供应商保存成功", "provider_id", provider.ID, "brand", provider.Brand, "model_count", len(provider.Models))
	return aiProviderFromAction(*provider), nil
}

// DeleteAIProvider 删除模型服务供应商。
func (o *integrationOps) DeleteAIProvider(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, providerID string) error {
	if err := o.deleteAIProvider.Execute(ctx, identity, providerID); err != nil {
		return aiProviderError(meta, err, i18n.ErrorAIProviderDeleteFailed)
	}
	slog.InfoContext(ctx, "模型服务供应商删除成功", "provider_id", providerID)
	return nil
}

// aiProviderMutationError 转换模型服务供应商写入错误。
func aiProviderMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return aiProviderMutationErrors.Translate(meta, err, failureKey)
}

// aiProviderErrors 是模型服务供应商操作的错误转换规则。
var aiProviderErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(aiprovideraction.ErrNotFound, dispatch.NotFound(i18n.ErrorAIProviderNotFound)),
	dispatch.Is(aiprovideraction.ErrInUse, dispatch.Invalid(i18n.ErrorAIProviderInUse)),
})

// aiProviderMutationErrors 是模型服务供应商写入的错误转换规则。
var aiProviderMutationErrors = dispatch.Catalogs(dispatch.Catalog{dispatch.FieldRule(dispatch.AIProviderFieldCodes)}, aiProviderErrors)

// aiProviderError 转换模型服务供应商操作错误。
func aiProviderError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return aiProviderErrors.Translate(meta, err, failureKey)
}

// aiProviderInput 转换模型服务供应商输入。
func aiProviderInput(input appservice.AIProviderInput) aiprovideraction.Input {
	return aiprovideraction.Input{
		Brand: input.Brand, Name: input.Name,
		CredentialType: input.CredentialType,
		APIKey:         input.APIKey, APIURL: input.APIURL, Models: aiProviderModelsInput(input.Models),
	}
}

// aiProviderModelsInput 转换模型目录输入。
func aiProviderModelsInput(input []appservice.AIProviderModel) []aiprovideraction.Model {
	models := make([]aiprovideraction.Model, 0, len(input))
	for _, model := range input {
		// 转换模型输入模态到领域值。
		inputModalities := append(make([]domain.AIModelInputModality, 0, len(model.InputModalities)), model.InputModalities...)
		models = append(models, aiprovideraction.Model{
			ID: model.ID, Identifier: model.Identifier, Name: model.Name, Type: model.Type,
			InputModalities: inputModalities,
			ContextWindow:   model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
		})
	}
	return models
}

// aiProviderFromAction 转换模型服务供应商输出。
func aiProviderFromAction(input aiprovideraction.Record) appservice.AIProvider {
	return appservice.AIProvider{
		ID: input.ID, Brand: input.Brand, Name: input.Name,
		CredentialType: input.CredentialType,
		APIKey:         input.APIKey, APIURL: input.APIURL,
		Models: aiProviderModelsFromAction(input.Models),
	}
}

// aiProviderModelsFromAction 转换 AI 模型目录。
func aiProviderModelsFromAction(input []aiprovideraction.Model) []appservice.AIProviderModel {
	models := make([]appservice.AIProviderModel, 0, len(input))
	for _, model := range input {
		// 转换领域模型输入模态到应用契约。
		inputModalities := append(make([]appservice.AIModelInputModality, 0, len(model.InputModalities)), model.InputModalities...)
		models = append(models, appservice.AIProviderModel{
			ID: model.ID, Identifier: model.Identifier, Name: model.Name, Type: model.Type,
			InputModalities: inputModalities,
			ContextWindow:   model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
		})
	}
	return models
}
