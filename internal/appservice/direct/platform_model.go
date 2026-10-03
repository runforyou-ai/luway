//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"maps"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	platformmodelaction "github.com/runforyou-ai/luway/internal/actions/platformmodel"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/modelprovider"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// platformModelOps 持有平台供应商、平台模型与平台模型调用记录的 Action 和 Query。
type platformModelOps struct {
	listPlatformAIProviders      *aiprovideraction.ListPlatformQuery
	getPlatformAIProvider        *aiprovideraction.GetPlatformQuery
	listPlatformAIProviderModels *aiprovideraction.ListPlatformModelsQuery
	createPlatformAIProvider     *aiprovideraction.CreatePlatformAction
	updatePlatformAIProvider     *aiprovideraction.UpdatePlatformAction
	deletePlatformAIProvider     *aiprovideraction.DeletePlatformAction
	listPlatformAIModels         *platformmodelaction.ListQuery
	getPlatformAIModel           *platformmodelaction.GetQuery
	createPlatformAIModel        *platformmodelaction.CreateAction
	updatePlatformAIModel        *platformmodelaction.UpdateAction
	deletePlatformAIModel        *platformmodelaction.DeleteAction
	listPlatformAIModelCalls     *platformmodelaction.ListCallsQuery
	getPlatformAIModelCall       *platformmodelaction.GetCallQuery
}

// newPlatformModelOps 创建平台模型服务的业务实现依赖。
func newPlatformModelOps(db *bun.DB, registry *modelprovider.Registry) platformModelOps {
	return platformModelOps{
		listPlatformAIProviders:      aiprovideraction.NewListPlatformQuery(db),
		getPlatformAIProvider:        aiprovideraction.NewGetPlatformQuery(db),
		listPlatformAIProviderModels: aiprovideraction.NewListPlatformModelsQuery(db, registry),
		createPlatformAIProvider:     aiprovideraction.NewCreatePlatformAction(db),
		updatePlatformAIProvider:     aiprovideraction.NewUpdatePlatformAction(db),
		deletePlatformAIProvider:     aiprovideraction.NewDeletePlatformAction(db),
		listPlatformAIModels:         platformmodelaction.NewListQuery(db),
		getPlatformAIModel:           platformmodelaction.NewGetQuery(db),
		createPlatformAIModel:        platformmodelaction.NewCreateAction(db),
		updatePlatformAIModel:        platformmodelaction.NewUpdateAction(db),
		deletePlatformAIModel:        platformmodelaction.NewDeleteAction(db),
		listPlatformAIModelCalls:     platformmodelaction.NewListCallsQuery(db),
		getPlatformAIModelCall:       platformmodelaction.NewGetCallQuery(db),
	}
}

// ListPlatformAIProviders 返回平台供应商。
func (o *directOperations) ListPlatformAIProviders(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformAIProviderList, error) {
	providers, err := o.listPlatformAIProviders.Execute(ctx)
	if err != nil {
		return appservice.PlatformAIProviderList{}, platformModelError(meta, err, i18n.ErrorPlatformAIProviderListFailed)
	}
	output := make([]appservice.PlatformAIProviderSummary, 0, len(providers))
	for _, provider := range providers {
		output = append(output, appservice.PlatformAIProviderSummary{
			ID: provider.ID, Brand: appservice.AIProviderBrand(provider.Brand), Name: provider.Name, APIURL: provider.APIURL,
			ModelCount: provider.ModelCount, RecentAttempts: provider.RecentAttempts, RecentFailures: provider.RecentFailures,
			LastError: provider.LastError, LastFailedAt: provider.LastFailedAt,
		})
	}
	return appservice.PlatformAIProviderList{Providers: output}, nil
}

// GetPlatformAIProvider 返回平台供应商详情。
func (o *directOperations) GetPlatformAIProvider(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, providerID string) (appservice.PlatformAIProvider, error) {
	provider, err := o.getPlatformAIProvider.Execute(ctx, providerID)
	if err != nil {
		return appservice.PlatformAIProvider{}, platformModelError(meta, err, i18n.ErrorPlatformAIProviderReadFailed)
	}
	return platformAIProviderFromAction(*provider), nil
}

// ListPlatformAIProviderModels 返回平台供应商可提供的模型。
func (o *directOperations) ListPlatformAIProviderModels(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, providerID string) (appservice.AIProviderModelList, error) {
	models, err := o.listPlatformAIProviderModels.Execute(ctx, providerID)
	if errors.Is(err, aiprovideraction.ErrNotFound) {
		return appservice.AIProviderModelList{}, appservice.NotFoundError(meta, i18n.ErrorAIProviderNotFound)
	}
	if err != nil {
		return appservice.AIProviderModelList{}, o.aiProviderConnectionError(meta, err, "")
	}
	return appservice.AIProviderModelList{Models: aiProviderModelsFromAction(models)}, nil
}

// CreatePlatformAIProvider 创建平台供应商。
func (o *directOperations) CreatePlatformAIProvider(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformAIProviderInput) (appservice.PlatformAIProvider, error) {
	provider, err := o.createPlatformAIProvider.Execute(ctx, account, aiprovideraction.PlatformInput{
		Brand: domain.AIProviderBrand(input.Brand), Name: input.Name, CredentialType: domain.AIProviderCredentialType(input.CredentialType),
		APIKey: input.APIKey, APIURL: input.APIURL,
	})
	if err != nil {
		return appservice.PlatformAIProvider{}, platformModelError(meta, err, i18n.ErrorPlatformAIProviderCreateFailed)
	}
	slog.Info("平台供应商创建成功", "account_id", account.Account.ID, "provider_id", provider.ID, "brand", provider.Brand)
	return platformAIProviderFromAction(*provider), nil
}

// UpdatePlatformAIProvider 修改平台供应商。
func (o *directOperations) UpdatePlatformAIProvider(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, providerID string, input appservice.PlatformAIProviderUpdateInput) (appservice.PlatformAIProvider, error) {
	provider, err := o.updatePlatformAIProvider.Execute(ctx, account, providerID, aiprovideraction.PlatformInput{
		Name: input.Name, CredentialType: domain.AIProviderCredentialType(input.CredentialType), APIKey: input.APIKey, APIURL: input.APIURL,
	})
	if err != nil {
		return appservice.PlatformAIProvider{}, platformModelError(meta, err, i18n.ErrorPlatformAIProviderUpdateFailed)
	}
	slog.Info("平台供应商保存成功", "account_id", account.Account.ID, "provider_id", provider.ID)
	return platformAIProviderFromAction(*provider), nil
}

// DeletePlatformAIProvider 删除不是任何平台模型来源的平台供应商。
func (o *directOperations) DeletePlatformAIProvider(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, providerID string) error {
	if err := o.deletePlatformAIProvider.Execute(ctx, account, providerID); err != nil {
		return platformModelError(meta, err, i18n.ErrorPlatformAIProviderDeleteFailed)
	}
	slog.Info("平台供应商删除成功", "account_id", account.Account.ID, "provider_id", providerID)
	return nil
}

// ListPlatformAIModels 返回平台模型目录。
func (o *directOperations) ListPlatformAIModels(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformAIModelList, error) {
	models, err := o.listPlatformAIModels.Execute(ctx)
	if err != nil {
		return appservice.PlatformAIModelList{}, platformModelError(meta, err, i18n.ErrorPlatformAIModelListFailed)
	}
	output := make([]appservice.PlatformAIModel, 0, len(models))
	for _, model := range models {
		output = append(output, platformAIModelFromAction(model))
	}
	return appservice.PlatformAIModelList{Models: output}, nil
}

// GetPlatformAIModel 返回平台模型详情。
func (o *directOperations) GetPlatformAIModel(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, modelID string) (appservice.PlatformAIModel, error) {
	model, err := o.getPlatformAIModel.Execute(ctx, modelID)
	if err != nil {
		return appservice.PlatformAIModel{}, platformModelError(meta, err, i18n.ErrorPlatformAIModelReadFailed)
	}
	return platformAIModelFromAction(*model), nil
}

// CreatePlatformAIModel 创建对全部工作区可用的平台模型。
func (o *directOperations) CreatePlatformAIModel(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformAIModelInput) (appservice.PlatformAIModel, error) {
	model, err := o.createPlatformAIModel.Execute(ctx, account, platformAIModelInput(input))
	if err != nil {
		return appservice.PlatformAIModel{}, platformModelError(meta, err, i18n.ErrorPlatformAIModelCreateFailed)
	}
	slog.Info("平台模型创建成功", "account_id", account.Account.ID, "model_id", model.ID, "route_count", len(model.Routes))
	return platformAIModelFromAction(*model), nil
}

// UpdatePlatformAIModel 修改平台模型的属性与来源。
func (o *directOperations) UpdatePlatformAIModel(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, modelID string, input appservice.PlatformAIModelInput) (appservice.PlatformAIModel, error) {
	model, err := o.updatePlatformAIModel.Execute(ctx, account, modelID, platformAIModelInput(input))
	if err != nil {
		return appservice.PlatformAIModel{}, platformModelError(meta, err, i18n.ErrorPlatformAIModelUpdateFailed)
	}
	slog.Info("平台模型保存成功", "account_id", account.Account.ID, "model_id", model.ID, "route_count", len(model.Routes))
	return platformAIModelFromAction(*model), nil
}

// DeletePlatformAIModel 删除没有被工作区引用的平台模型。
func (o *directOperations) DeletePlatformAIModel(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, modelID string) error {
	if err := o.deletePlatformAIModel.Execute(ctx, account, modelID); err != nil {
		return platformModelError(meta, err, i18n.ErrorPlatformAIModelDeleteFailed)
	}
	slog.Info("平台模型删除成功", "account_id", account.Account.ID, "model_id", modelID)
	return nil
}

// ListPlatformAIModelCalls 返回平台模型调用记录。
func (o *directOperations) ListPlatformAIModelCalls(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformAIModelCallListInput) (appservice.PlatformAIModelCallList, error) {
	list, err := o.listPlatformAIModelCalls.Execute(ctx, platformmodelaction.CallListInput{
		ModelID: input.ModelID, Status: domain.AIModelCallStatus(input.Status), Query: input.Query, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformAIModelCallList{}, platformModelError(meta, err, i18n.ErrorPlatformAIModelCallListFailed)
	}
	calls := make([]appservice.PlatformAIModelCall, 0, len(list.Calls))
	for _, call := range list.Calls {
		calls = append(calls, platformAIModelCallFromAction(call))
	}
	return appservice.PlatformAIModelCallList{Calls: calls, Page: appservice.PageInfo(list.Page)}, nil
}

// GetPlatformAIModelCall 返回平台模型调用及其上游尝试。
func (o *directOperations) GetPlatformAIModelCall(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, callID string) (appservice.PlatformAIModelCallDetail, error) {
	detail, err := o.getPlatformAIModelCall.Execute(ctx, callID)
	if errors.Is(err, platformmodelaction.ErrNotFound) {
		return appservice.PlatformAIModelCallDetail{}, appservice.NotFoundError(meta, i18n.ErrorPlatformAIModelCallNotFound)
	}
	if err != nil {
		return appservice.PlatformAIModelCallDetail{}, platformModelError(meta, err, i18n.ErrorPlatformAIModelCallReadFailed)
	}
	attempts := make([]appservice.PlatformAIModelCallAttempt, 0, len(detail.Attempts))
	for _, attempt := range detail.Attempts {
		attempts = append(attempts, appservice.PlatformAIModelCallAttempt{
			ID: attempt.ID, CreatedAt: attempt.CreatedAt, FinishedAt: attempt.FinishedAt,
			ProviderName: attempt.ProviderName, Identifier: attempt.Identifier, Status: appservice.AIModelCallStatus(attempt.Status),
			InputTokens: attempt.InputTokens, CachedInputTokens: attempt.CachedInputTokens, OutputTokens: attempt.OutputTokens,
			ErrorMessage: attempt.ErrorMessage,
		})
	}
	return appservice.PlatformAIModelCallDetail{Call: platformAIModelCallFromAction(detail.Call), Attempts: attempts}, nil
}

// platformModelFieldCodes 是平台供应商与平台模型校验错误码对应的文案键。
var platformModelFieldCodes = func() map[common.FieldCode]i18n.Key {
	codes := map[common.FieldCode]i18n.Key{
		platformmodelaction.ValidationNameInvalid:            i18n.FieldPlatformAIModelNameInvalid,
		platformmodelaction.ValidationNameDuplicate:          i18n.FieldPlatformAIModelNameDuplicate,
		platformmodelaction.ValidationTypeInvalid:            i18n.FieldPlatformAIModelTypeInvalid,
		platformmodelaction.ValidationInputModalitiesInvalid: i18n.FieldPlatformAIModelInputModalitiesInvalid,
		platformmodelaction.ValidationContextWindowInvalid:   i18n.FieldPlatformAIModelContextWindowInvalid,
		platformmodelaction.ValidationMaxOutputTokensInvalid: i18n.FieldPlatformAIModelMaxOutputTokensInvalid,
		platformmodelaction.ValidationRoutesInvalid:          i18n.FieldPlatformAIModelRoutesInvalid,
		platformmodelaction.ValidationRouteDuplicate:         i18n.FieldPlatformAIModelRouteDuplicate,
		platformmodelaction.ValidationUsageConflict:          i18n.FieldPlatformAIModelUsageConflict,
		platformmodelaction.ValidationPriceInvalid:           i18n.FieldPlatformAIModelPriceInvalid,
		platformmodelaction.ValidationCallQueryInvalid:       i18n.FieldPlatformAIModelCallQueryInvalid,
	}
	maps.Copy(codes, aiProviderFieldCodes)
	return codes
}()

// platformModelError 转换平台模型服务操作错误。
func platformModelError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if mapped := commonActionError(meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, platformModelFieldCodes))
	}
	switch {
	case errors.Is(err, platformaction.ErrNotPlatformAdmin):
		return appservice.ForbiddenError(meta, i18n.ErrorPlatformAdminRequired)
	case errors.Is(err, aiprovideraction.ErrNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorAIProviderNotFound)
	case errors.Is(err, aiprovideraction.ErrPlatformInUse):
		return appservice.InvalidError(meta, i18n.ErrorPlatformAIProviderInUse, nil)
	case errors.Is(err, platformmodelaction.ErrNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorPlatformAIModelNotFound)
	case errors.Is(err, platformmodelaction.ErrInUse):
		return appservice.InvalidError(meta, i18n.ErrorPlatformAIModelInUse, nil)
	}
	return appservice.FailedError(meta, failureKey, err)
}

// platformAIProviderFromAction 转换平台供应商详情。
func platformAIProviderFromAction(provider aiprovideraction.PlatformRecord) appservice.PlatformAIProvider {
	return appservice.PlatformAIProvider{
		ID: provider.ID, Brand: appservice.AIProviderBrand(provider.Brand), Name: provider.Name,
		CredentialType: appservice.AIProviderCredentialType(provider.CredentialType), APIKey: provider.APIKey, APIURL: provider.APIURL,
	}
}

// platformAIModelInput 转换平台模型输入。
func platformAIModelInput(input appservice.PlatformAIModelInput) platformmodelaction.Input {
	inputModalities := make([]domain.AIModelInputModality, 0, len(input.InputModalities))
	for _, modality := range input.InputModalities {
		inputModalities = append(inputModalities, domain.AIModelInputModality(modality))
	}
	routes := make([]platformmodelaction.RouteInput, 0, len(input.Routes))
	for _, route := range input.Routes {
		routes = append(routes, platformmodelaction.RouteInput{ID: route.ID, ProviderID: route.ProviderID, Identifier: route.Identifier, Enabled: route.Enabled})
	}
	return platformmodelaction.Input{
		Spec: aimodel.Spec{
			Name: input.Name, Type: domain.AIModelType(input.Type), InputModalities: inputModalities,
			ContextWindow: input.ContextWindow, MaxOutputTokens: input.MaxOutputTokens,
		},
		Price:  (*domain.CreditPrice)(input.Price),
		Routes: routes,
	}
}

// platformAIModelFromAction 转换平台模型输出。
func platformAIModelFromAction(model platformmodelaction.Record) appservice.PlatformAIModel {
	inputModalities := make([]appservice.AIModelInputModality, 0, len(model.InputModalities))
	for _, modality := range model.InputModalities {
		inputModalities = append(inputModalities, appservice.AIModelInputModality(modality))
	}
	routes := make([]appservice.PlatformAIModelRoute, 0, len(model.Routes))
	for _, route := range model.Routes {
		routes = append(routes, appservice.PlatformAIModelRoute{
			ID: route.ID, ProviderID: route.ProviderID, ProviderName: route.ProviderName,
			ProviderBrand: appservice.AIProviderBrand(route.ProviderBrand), Identifier: route.Identifier, Enabled: route.Enabled,
		})
	}
	return appservice.PlatformAIModel{
		ID: model.ID, Name: model.Name, Type: appservice.AIModelType(model.Type), InputModalities: inputModalities,
		ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens, Price: creditPriceFromDomain(model.Price), Routes: routes,
	}
}

// platformAIModelCallFromAction 转换平台模型调用记录。
func platformAIModelCallFromAction(call platformmodelaction.Call) appservice.PlatformAIModelCall {
	return appservice.PlatformAIModelCall{
		ID: call.ID, CreatedAt: call.CreatedAt, FinishedAt: call.FinishedAt, ModelID: call.ModelID, ModelName: call.ModelName,
		Usage: appservice.AIModelUsage(call.Usage), WorkspaceID: call.WorkspaceID, WorkspaceName: call.WorkspaceName,
		Actor: appservice.AIModelCallActor(call.Actor), Status: appservice.AIModelCallStatus(call.Status),
		InputTokens: call.InputTokens, CachedInputTokens: call.CachedInputTokens, OutputTokens: call.OutputTokens,
		ErrorMessage: call.ErrorMessage, AttemptCount: call.AttemptCount, Credits: call.Credits, CreditShortfall: call.CreditShortfall,
	}
}
