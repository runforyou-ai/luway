//go:build server

// 客户会话翻译、回复语言与企业翻译设置。
package direct

import (
	"context"
	"errors"
	"log/slog"

	customerserviceaction "github.com/runforyou-ai/cervi/internal/actions/customerservice"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	translationaction "github.com/runforyou-ai/cervi/internal/actions/translation"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// translationOps 汇集客户会话翻译与翻译设置的业务依赖。
type translationOps struct {
	translator                *translationaction.Translator
	getTranslationSettings    *customerserviceaction.GetTranslationSettingsQuery
	updateTranslationSettings *customerserviceaction.UpdateTranslationSettingsAction
}

// newTranslationOps 创建客户会话翻译与翻译设置的业务依赖。
func newTranslationOps(db *bun.DB, translator *translationaction.Translator) translationOps {
	return translationOps{
		translator:                translator,
		getTranslationSettings:    customerserviceaction.NewGetTranslationSettingsQuery(db),
		updateTranslationSettings: customerserviceaction.NewUpdateTranslationSettingsAction(db),
	}
}

// GetConversationTranslation 返回当前成员在客户会话中的翻译状态。
func (o *directOperations) GetConversationTranslation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ConversationTranslation, error) {
	state, err := o.translator.ConversationState(ctx, identity, conversationID)
	if err != nil {
		return appservice.ConversationTranslation{}, translationError(ctx, meta, err, i18n.ErrorTranslationStateLoadFailed, identity.Organization.ID, conversationID)
	}
	return conversationTranslationFromAction(state), nil
}

// TranslateConversationMessages 返回客户会话中指定对客消息面向当前成员语言的译文。
func (o *directOperations) TranslateConversationMessages(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.TranslateConversationMessagesInput) (appservice.ConversationMessageTranslationList, error) {
	results, err := o.translator.TranslateMessages(ctx, identity, conversationID, input.MessageIDs)
	if err != nil {
		return appservice.ConversationMessageTranslationList{}, translationError(ctx, meta, err, i18n.ErrorTranslationFailed, identity.Organization.ID, conversationID)
	}
	translations := make([]appservice.ConversationMessageTranslationResult, 0, len(results))
	for _, result := range results {
		translations = append(translations, appservice.ConversationMessageTranslationResult{MessageID: result.MessageID, Language: result.Language, Body: result.Body})
	}
	return appservice.ConversationMessageTranslationList{Translations: translations}, nil
}

// UpdateCustomerReplyLanguage 锁定或解除客户会话的对客回复语言。
func (o *directOperations) UpdateCustomerReplyLanguage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.CustomerReplyLanguageInput) (appservice.ConversationTranslation, error) {
	state, err := o.translator.SetReplyLanguage(ctx, identity, conversationID, input.Language)
	if err != nil {
		return appservice.ConversationTranslation{}, translationError(ctx, meta, err, i18n.ErrorReplyLanguageUpdateFailed, identity.Organization.ID, conversationID)
	}
	return conversationTranslationFromAction(state), nil
}

// PreviewCustomerReplyTranslation 把客服回复译为客户语言并回译为客服语言。
func (o *directOperations) PreviewCustomerReplyTranslation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.CustomerReplyTranslationInput) (appservice.CustomerReplyTranslationPreview, error) {
	preview, err := o.translator.PreviewReply(ctx, identity, conversationID, input.Body)
	if err != nil {
		return appservice.CustomerReplyTranslationPreview{}, translationError(ctx, meta, err, i18n.ErrorTranslationFailed, identity.Organization.ID, conversationID)
	}
	if preview == nil {
		return appservice.CustomerReplyTranslationPreview{}, nil
	}
	return appservice.CustomerReplyTranslationPreview{Language: preview.Language, Body: preview.Body, BackTranslation: preview.BackTranslation}, nil
}

// GetTranslationSettings 读取当前企业的翻译设置。
func (o *directOperations) GetTranslationSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.TranslationSettings, error) {
	model, err := o.getTranslationSettings.Execute(ctx, identity)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.TranslationSettings{}, ctx.Err()
		}
		slog.Warn("读取翻译设置失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.TranslationSettings{}, appservice.FailedError(meta, i18n.ErrorTranslationSettingsLoadFailed)
	}
	return translationSettingsFromDomain(model), nil
}

// UpdateTranslationSettings 修改当前企业的翻译设置。
func (o *directOperations) UpdateTranslationSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TranslationSettings) (appservice.TranslationSettings, error) {
	var model *domain.AIModelReference
	if input.Model != nil {
		model = &domain.AIModelReference{ProviderID: input.Model.ProviderID, ModelIdentifier: input.Model.ModelIdentifier}
	}
	saved, err := o.updateTranslationSettings.Execute(ctx, identity, model)
	if err != nil {
		if ctx.Err() != nil {
			return appservice.TranslationSettings{}, ctx.Err()
		}
		if validationError, ok := errors.AsType[*common.FieldError](err); ok {
			keys := map[common.FieldCode]i18n.Key{customerserviceaction.ValidationTranslationModelInvalid: i18n.FieldChatModelInvalid}
			return appservice.TranslationSettings{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
		}
		if errors.Is(err, identityaction.ErrInvalid) {
			return appservice.TranslationSettings{}, appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
		}
		slog.Warn("修改翻译设置失败", "organization_id", identity.Organization.ID, "error", err)
		return appservice.TranslationSettings{}, appservice.FailedError(meta, i18n.ErrorTranslationSettingsUpdateFailed)
	}
	return translationSettingsFromDomain(saved), nil
}

// translationSettingsFromDomain 转换企业翻译设置契约。
func translationSettingsFromDomain(model *domain.AIModelReference) appservice.TranslationSettings {
	if model == nil {
		return appservice.TranslationSettings{}
	}
	return appservice.TranslationSettings{Model: &appservice.AIModelReference{ProviderID: model.ProviderID, ModelIdentifier: model.ModelIdentifier}}
}

// conversationTranslationFromAction 转换成员在客户会话中的翻译状态。
func conversationTranslationFromAction(state translationaction.ConversationState) appservice.ConversationTranslation {
	return appservice.ConversationTranslation{
		Enabled: state.Enabled, ViewerLanguage: state.ViewerLanguage,
		CustomerLanguage: state.CustomerLanguage, ReplyLanguageLocked: state.ReplyLanguageLocked,
	}
}

// translationError 转换客户会话翻译错误，未识别的失败使用 failureKey。
func translationError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID, conversationID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, identityaction.ErrInvalid):
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	case errors.Is(err, translationaction.ErrConversationNotFound):
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound).WithReason("conversation_unavailable")
	case errors.Is(err, translationaction.ErrDisabled):
		return appservice.ConflictError(meta, i18n.ErrorTranslationDisabled, "translation_disabled")
	case errors.Is(err, translationaction.ErrCustomerLanguageUnknown):
		return appservice.ConflictError(meta, i18n.ErrorCustomerLanguageUnknown, "customer_language_unknown")
	case errors.Is(err, translationaction.ErrReplyLanguageChanged):
		return appservice.ConflictError(meta, i18n.ErrorReplyLanguageChanged, "reply_language_changed")
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		keys := map[common.FieldCode]i18n.Key{
			translationaction.ValidationMessageIDsInvalid: i18n.FieldTranslationMessagesInvalid,
			translationaction.ValidationBodyRequired:      i18n.FieldMessageBodyRequired,
			translationaction.ValidationBodyTooLong:       i18n.FieldMessageBodyTooLong,
			translationaction.ValidationLanguageInvalid:   i18n.FieldLocaleInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	slog.Warn("客户会话翻译失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, failureKey)
}
