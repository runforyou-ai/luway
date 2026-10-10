package appservice

import "context"

// TranslationBackend 定义客户会话翻译与翻译设置的业务调用。
type TranslationBackend interface {
	// GetConversationTranslation 返回当前成员在客户会话中的翻译状态。
	//appservice:route GET /conversations/{conversationID:uuid}/translation perm=none
	GetConversationTranslation(context.Context, RequestMeta, string) (ConversationTranslation, error)
	// TranslateConversationMessages 返回客户会话中指定对客消息面向当前成员语言的译文，尚无译文的消息即时翻译。
	//appservice:route POST /conversations/{conversationID:uuid}/translations perm=none
	TranslateConversationMessages(context.Context, RequestMeta, string, TranslateConversationMessagesInput) (ConversationMessageTranslationList, error)
	// UpdateCustomerReplyLanguage 锁定或解除客户会话的对客回复语言。
	//appservice:route PUT /conversations/{conversationID:uuid}/reply-language perm=none
	UpdateCustomerReplyLanguage(context.Context, RequestMeta, string, CustomerReplyLanguageInput) (ConversationTranslation, error)
	// PreviewCustomerReplyTranslation 把客服回复译为客户语言并回译为客服语言，供发送前核对。
	//appservice:route POST /conversations/{conversationID:uuid}/reply-translation perm=none
	PreviewCustomerReplyTranslation(context.Context, RequestMeta, string, CustomerReplyTranslationInput) (CustomerReplyTranslationPreview, error)
	// GetTranslationSettings 读取当前企业的翻译设置。
	//appservice:route GET /settings/customer-service/translation perm=customer_service.manage
	GetTranslationSettings(context.Context, RequestMeta) (TranslationSettings, error)
	// UpdateTranslationSettings 修改当前企业的翻译设置。
	//appservice:route PUT /settings/customer-service/translation perm=customer_service.manage
	UpdateTranslationSettings(context.Context, RequestMeta, TranslationSettings) (TranslationSettings, error)
}
