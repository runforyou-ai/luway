//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	websearchaction "github.com/runforyou-ai/cervi/internal/actions/websearch"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	"github.com/runforyou-ai/cervi/internal/integration/websearch"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// webSearchOps 持有联网搜索设置的 Action 和 Query。
type webSearchOps struct {
	getWebSearchSettings    *websearchaction.GetSettingsQuery
	updateWebSearchSettings *websearchaction.UpdateSettingsAction
	testWebSearchService    *websearchaction.TestAction
}

// newWebSearchOps 创建联网搜索设置的业务依赖。
func newWebSearchOps(db *bun.DB, connectionRunner *connectiontest.Runner) webSearchOps {
	return webSearchOps{
		getWebSearchSettings:    websearchaction.NewGetSettingsQuery(db),
		updateWebSearchSettings: websearchaction.NewUpdateSettingsAction(db),
		testWebSearchService:    websearchaction.NewTestAction(connectionRunner, websearch.NewClient()),
	}
}

// GetWebSearchSettings 读取当前企业的联网搜索设置。
func (o *directOperations) GetWebSearchSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.WebSearchSettings, error) {
	config, err := o.getWebSearchSettings.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.WebSearchSettings{}, ctx.Err()
		}
		slog.Warn("读取联网搜索设置失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.WebSearchSettings{}, appservice.FailedError(meta, i18n.ErrorWebSearchSettingsLoadFailed)
	}
	return webSearchSettingsFromConfig(config), nil
}

// UpdateWebSearchSettings 修改当前企业的联网搜索设置。
func (o *directOperations) UpdateWebSearchSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.WebSearchSettings) (appservice.WebSearchSettings, error) {
	var config *websearch.Config
	if input.Service != nil {
		converted := webSearchConfig(*input.Service)
		config = &converted
	}
	saved, err := o.updateWebSearchSettings.Execute(ctx, identity, config)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.WebSearchSettings{}, ctx.Err()
		}
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			return appservice.WebSearchSettings{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, webSearchFieldKeys(validationError.Fields))
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.WebSearchSettings{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("修改联网搜索设置失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.WebSearchSettings{}, appservice.FailedError(meta, i18n.ErrorWebSearchSettingsUpdateFailed)
	}
	return webSearchSettingsFromConfig(saved), nil
}

// TestWebSearchService 用草稿配置执行一次搜索，验证搜索服务可用。
func (o *directOperations) TestWebSearchService(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, input appservice.WebSearchService) error {
	err := o.testWebSearchService.Execute(ctx, webSearchConfig(input))
	if err == nil {
		return nil
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, webSearchFieldKeys(validationError.Fields))
	}
	return webSearchError(ctx, meta, err)
}

// webSearchError 按连接失败原因转换调用搜索服务产生的错误。
func webSearchError(ctx context.Context, meta appservice.RequestMeta, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, kind, _ := connectiontest.Details(err)
	switch kind {
	case connectiontest.FailureUnauthorized:
		return appservice.UnavailableError(meta, i18n.ErrorWebSearchAuthenticationFailed, nil)
	case connectiontest.FailureForbidden:
		return appservice.UnavailableError(meta, i18n.ErrorWebSearchAuthorizationFailed, nil)
	case connectiontest.FailureRateLimited:
		return appservice.UnavailableError(meta, i18n.ErrorWebSearchRateLimited, nil)
	default:
		return appservice.UnavailableError(meta, i18n.ErrorWebSearchTestFailed, nil)
	}
}

// webSearchFieldKeys 转换联网搜索设置的字段校验结果。
func webSearchFieldKeys(fields map[string]common.FieldCode) map[string]i18n.Key {
	keys := map[common.FieldCode]i18n.Key{
		websearchaction.ValidationProviderInvalid: i18n.FieldWebSearchProviderInvalid,
		websearchaction.ValidationAPIKeyRequired:  i18n.FieldAPIKeyRequired,
		websearchaction.ValidationBaseURLRequired: i18n.FieldWebSearchBaseURLRequired,
		websearchaction.ValidationBaseURLInvalid:  i18n.FieldWebSearchBaseURLInvalid,
	}
	return translateValidationFields(fields, keys)
}

// webSearchConfig 转换搜索服务契约。
func webSearchConfig(service appservice.WebSearchService) websearch.Config {
	return websearch.Config{Provider: domain.WebSearchProvider(service.Provider), APIKey: service.APIKey, BaseURL: service.BaseURL}
}

// webSearchSettingsFromConfig 转换企业联网搜索设置契约。
func webSearchSettingsFromConfig(config *websearch.Config) appservice.WebSearchSettings {
	if config == nil {
		return appservice.WebSearchSettings{}
	}
	return appservice.WebSearchSettings{Service: &appservice.WebSearchService{
		Provider: appservice.WebSearchProvider(config.Provider), APIKey: config.APIKey, BaseURL: config.BaseURL,
	}}
}
