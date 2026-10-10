//go:build server

package dispatch

import (
	"context"
	"errors"
	"log/slog"

	aiprovideraction "github.com/runforyou-ai/luway/internal/actions/aiprovider"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// AIProviderConnectionError 把访问模型服务实例产生的校验和连接错误转换为业务错误。
func AIProviderConnectionError(ctx context.Context, meta appservice.RequestMeta, err error, brand appservice.AIProviderBrand) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, TranslateFields(validationError.Fields, AIProviderFieldCodes))
	}
	_, kind, classified := connectiontest.Details(err)
	if !classified {
		slog.WarnContext(ctx, "模型服务调用返回未分类错误", "brand", brand, "error", err)
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

// AIProviderFieldCodes 是模型服务供应商校验错误码对应的文案键。
var AIProviderFieldCodes = map[common.FieldCode]i18n.Key{
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
