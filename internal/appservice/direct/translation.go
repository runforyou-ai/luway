//go:build server

// 客户会话翻译、回复语言与企业翻译设置。

package direct

import (
	"context"

	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	translationaction "github.com/runforyou-ai/luway/internal/actions/translation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// translationOps 汇集客户会话翻译与翻译设置的业务依赖。
type translationOps struct {
	translator                *translationaction.Translator
	getTranslationSettings    *customerserviceaction.GetTranslationSettingsQuery
	updateTranslationSettings *customerserviceaction.UpdateTranslationSettingsAction
}

// newTranslationOps 创建客户会话翻译与翻译设置的业务依赖。
func newTranslationOps(db *bun.DB, translator *translationaction.Translator) *translationOps {
	return &translationOps{
		translator:                translator,
		getTranslationSettings:    customerserviceaction.NewGetTranslationSettingsQuery(db),
		updateTranslationSettings: customerserviceaction.NewUpdateTranslationSettingsAction(db),
	}
}

// GetConversationTranslation 返回当前成员在客户会话中的翻译状态。
func (o *translationOps) GetConversationTranslation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string) (appservice.ConversationTranslation, error) {
	state, err := o.translator.ConversationState(ctx, identity, conversationID)
	if err != nil {
		return appservice.ConversationTranslation{}, translationError(meta, err, i18n.ErrorTranslationStateLoadFailed)
	}
	return conversationTranslationFromAction(state), nil
}

// TranslateConversationMessages 返回客户会话中指定对客消息面向当前成员语言的译文。
func (o *translationOps) TranslateConversationMessages(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.TranslateConversationMessagesInput) (appservice.ConversationMessageTranslationList, error) {
	results, err := o.translator.TranslateMessages(ctx, identity, conversationID, input.MessageIDs)
	if err != nil {
		return appservice.ConversationMessageTranslationList{}, translationError(meta, err, i18n.ErrorTranslationFailed)
	}
	translations := arr.Map(results, func(result translationaction.MessageTranslation) appservice.ConversationMessageTranslationResult {
		return appservice.ConversationMessageTranslationResult{MessageID: result.MessageID, Language: result.Language, Body: result.Body}
	})
	return appservice.ConversationMessageTranslationList{Translations: translations}, nil
}

// UpdateCustomerReplyLanguage 锁定或解除客户会话的对客回复语言。
func (o *translationOps) UpdateCustomerReplyLanguage(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.CustomerReplyLanguageInput) (appservice.ConversationTranslation, error) {
	state, err := o.translator.SetReplyLanguage(ctx, identity, conversationID, input.Language)
	if err != nil {
		return appservice.ConversationTranslation{}, translationError(meta, err, i18n.ErrorReplyLanguageUpdateFailed)
	}
	return conversationTranslationFromAction(state), nil
}

// PreviewCustomerReplyTranslation 把客服回复译为客户语言并回译为客服语言。
func (o *translationOps) PreviewCustomerReplyTranslation(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.CustomerReplyTranslationInput) (appservice.CustomerReplyTranslationPreview, error) {
	preview, err := o.translator.PreviewReply(ctx, identity, conversationID, input.Body)
	if err != nil {
		return appservice.CustomerReplyTranslationPreview{}, translationError(meta, err, i18n.ErrorTranslationFailed)
	}
	if preview == nil {
		return appservice.CustomerReplyTranslationPreview{}, nil
	}
	return appservice.CustomerReplyTranslationPreview{Language: preview.Language, Body: preview.Body, BackTranslation: preview.BackTranslation}, nil
}

// GetTranslationSettings 读取当前企业的翻译设置。
func (o *translationOps) GetTranslationSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.TranslationSettings, error) {
	model, err := o.getTranslationSettings.Execute(ctx, identity)
	if err != nil {
		return appservice.TranslationSettings{}, appservice.FailedError(meta, i18n.ErrorTranslationSettingsLoadFailed, err)
	}
	return appservice.TranslationSettings{ModelID: model}, nil
}

// UpdateTranslationSettings 修改当前企业的翻译设置。
func (o *translationOps) UpdateTranslationSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.TranslationSettings) (appservice.TranslationSettings, error) {
	saved, err := o.updateTranslationSettings.Execute(ctx, identity, input.ModelID)
	if err != nil {
		return appservice.TranslationSettings{}, dispatch.Catalog{dispatch.FieldRule(translationSettingsFieldKeys), dispatch.SessionRule}.Translate(meta, err, i18n.ErrorTranslationSettingsUpdateFailed)
	}
	return appservice.TranslationSettings{ModelID: saved}, nil
}

// conversationTranslationFromAction 转换成员在客户会话中的翻译状态。
func conversationTranslationFromAction(state translationaction.ConversationState) appservice.ConversationTranslation {
	return appservice.ConversationTranslation{
		Enabled: state.Enabled, ViewerLanguage: state.ViewerLanguage,
		CustomerLanguage: state.CustomerLanguage, ReplyLanguageLocked: state.ReplyLanguageLocked,
	}
}

// translationFieldKeys 把客户会话翻译校验错误码映射为本地化文案键。
var translationFieldKeys = map[common.FieldCode]i18n.Key{
	translationaction.ValidationLanguageInvalid: i18n.FieldLocaleInvalid,
}

// translationSettingsFieldKeys 把翻译设置校验错误码映射为本地化文案键。
var translationSettingsFieldKeys = map[common.FieldCode]i18n.Key{
	customerserviceaction.ValidationTranslationModelInvalid: i18n.FieldChatModelInvalid,
}

// translationErrors 是客户会话翻译的错误转换规则。
var translationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.SessionRule,
	dispatch.Is(translationaction.ErrConversationNotFound, dispatch.WithReason(dispatch.NotFound(i18n.ErrorConversationNotFound), "conversation_unavailable")),
	dispatch.Is(translationaction.ErrDisabled, dispatch.Conflict(i18n.ErrorTranslationDisabled, "translation_disabled")),
	dispatch.Is(translationaction.ErrCustomerLanguageUnknown, dispatch.Conflict(i18n.ErrorCustomerLanguageUnknown, "customer_language_unknown")),
	dispatch.Is(translationaction.ErrReplyLanguageChanged, dispatch.Conflict(i18n.ErrorReplyLanguageChanged, "reply_language_changed")),
	dispatch.FieldRule(translationFieldKeys),
})

// translationError 转换客户会话翻译错误，未识别的失败使用 failureKey。
func translationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return translationErrors.Translate(meta, err, failureKey)
}
