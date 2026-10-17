//go:build server

package direct

import (
	"context"
	"errors"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/webtools"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	integrationwebfetch "github.com/runforyou-ai/luway/internal/integration/webfetch"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/uptrace/bun"
)

// webFetchOps 持有网页读取设置的数据库与测试依赖。
type webFetchOps struct {
	db      *bun.DB
	runner  *connectiontest.Runner
	fetcher webtools.Fetcher
}

// newWebFetchOps 创建网页读取设置的业务依赖。
func newWebFetchOps(db *bun.DB, connectionRunner *connectiontest.Runner) *webFetchOps {
	return &webFetchOps{db: db, runner: connectionRunner, fetcher: integrationwebfetch.NewClient()}
}

// GetWebFetchSettings 读取当前企业的网页读取设置。
func (o *webFetchOps) GetWebFetchSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.WebFetchSettings, error) {
	config, err := webtools.GetFetchSettings(ctx, o.db, identity)
	if err != nil {
		return appservice.WebFetchSettings{}, appservice.FailedError(meta, i18n.ErrorWebFetchSettingsLoadFailed, err)
	}
	return webFetchSettingsFromConfig(config), nil
}

// UpdateWebFetchSettings 修改当前企业的网页读取设置。
func (o *webFetchOps) UpdateWebFetchSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.WebFetchSettings) (appservice.WebFetchSettings, error) {
	saved, err := webtools.UpdateFetchSettings(ctx, o.db, identity, webFetchConfig(input))
	if err != nil {
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			return appservice.WebFetchSettings{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, webFetchFieldKeys))
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.WebFetchSettings{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		return appservice.WebFetchSettings{}, appservice.FailedError(meta, i18n.ErrorWebFetchSettingsUpdateFailed, err)
	}
	return webFetchSettingsFromConfig(saved), nil
}

// TestWebFetchService 用草稿配置读取一次固定页面，验证读取服务可用。
func (o *webFetchOps) TestWebFetchService(ctx context.Context, meta appservice.RequestMeta, _ *servermodels.Identity, input appservice.WebFetchSettings) error {
	err := webtools.TestFetch(ctx, o.runner, o.fetcher, webFetchConfig(input))
	if err == nil {
		return nil
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, webFetchFieldKeys))
	}
	return webFetchError(meta, err)
}

// webFetchError 按连接失败原因转换调用读取服务产生的错误。
func webFetchError(meta appservice.RequestMeta, err error) error {
	_, kind, _ := connectiontest.Details(err)
	switch kind {
	case connectiontest.FailureUnauthorized:
		return appservice.UnavailableError(meta, i18n.ErrorWebFetchAuthenticationFailed, nil)
	case connectiontest.FailureForbidden:
		return appservice.UnavailableError(meta, i18n.ErrorWebFetchAuthorizationFailed, nil)
	case connectiontest.FailureRateLimited:
		return appservice.UnavailableError(meta, i18n.ErrorWebFetchRateLimited, nil)
	default:
		return appservice.UnavailableError(meta, i18n.ErrorWebFetchTestFailed, nil)
	}
}

// webFetchFieldKeys 把网页读取设置校验错误码映射为本地化文案键。
var webFetchFieldKeys = map[common.FieldCode]i18n.Key{
	webtools.ValidationFetchProviderInvalid: i18n.FieldWebFetchProviderInvalid,
	webtools.ValidationFetchAPIKeyRequired:  i18n.FieldAPIKeyRequired,
	webtools.ValidationFetchBaseURLInvalid:  i18n.FieldWebFetchBaseURLInvalid,
}

// webFetchConfig 转换网页读取设置契约。
func webFetchConfig(settings appservice.WebFetchSettings) integrationwebfetch.Config {
	return integrationwebfetch.Config{Provider: settings.Provider, APIKey: settings.APIKey, BaseURL: settings.BaseURL}
}

// webFetchSettingsFromConfig 转换企业网页读取设置契约。
func webFetchSettingsFromConfig(config integrationwebfetch.Config) appservice.WebFetchSettings {
	return appservice.WebFetchSettings{Provider: config.Provider, APIKey: config.APIKey, BaseURL: config.BaseURL}
}
