package i18n

import (
	"log/slog"
	"maps"
	"slices"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"

	"github.com/runforyou-ai/luway/internal/common/brand"
	"github.com/runforyou-ai/luway/internal/domain"
)

// 网站访客接口返回给访客的错误文案。
const (
	VisitorErrorRequestInvalid       Key = "visitor_error.request_invalid"
	VisitorErrorMessageRequired      Key = "visitor_error.message_required"
	VisitorErrorMessageTooLong       Key = "visitor_error.message_too_long"
	VisitorErrorRatingCommentTooLong Key = "visitor_error.rating_comment_too_long"
	VisitorErrorFileInvalid          Key = "visitor_error.file_invalid"
	VisitorErrorAttachmentTooLarge   Key = "visitor_error.attachment_too_large"
	VisitorErrorAttachmentsDisabled  Key = "visitor_error.attachments_disabled"
	VisitorErrorChatUnavailable      Key = "visitor_error.chat_unavailable"
	VisitorErrorConversationNotFound Key = "visitor_error.conversation_not_found"
	VisitorErrorReplyTargetInvalid   Key = "visitor_error.reply_target_invalid"
	VisitorErrorRatingUnavailable    Key = "visitor_error.rating_unavailable"
	VisitorErrorMessageConflict      Key = "visitor_error.message_conflict"
	VisitorErrorLoadFailed           Key = "visitor_error.load_failed"
	VisitorErrorSendFailed           Key = "visitor_error.send_failed"
	VisitorErrorUploadFailed         Key = "visitor_error.upload_failed"
	VisitorErrorRateFailed           Key = "visitor_error.rate_failed"
	VisitorErrorInternal             Key = "visitor_error.internal"
)

// customerKeyPrefixes 是对客词条的键前缀，每种对客语言都须完整提供这些词条；messenger.preview_ 开头的管理端预览词条除外。
var customerKeyPrefixes = []string{"messenger.", "agent.customer_", "customer_email.", "visitor_error."}

// customerExcludedKeyPrefix 是对客前缀下只在管理端展示的词条前缀。
const customerExcludedKeyPrefix = "messenger.preview_"

// customerLocaleFile 返回对客语言的词条文件：应用语言共用完整词条文件，其余语言使用 locales/customer 下只含对客词条的文件。
func customerLocaleFile(locale domain.CustomerLocale) string {
	path := "locales/" + string(locale) + ".json"
	if slices.Contains(appLocaleFiles, path) {
		return path
	}
	return "locales/customer/" + string(locale) + ".json"
}

// 加载对客语言的翻译词条。
var customerBundle = func() *goi18n.Bundle {
	bundle := goi18n.NewBundle(language.AmericanEnglish)
	for _, locale := range domain.CustomerLocales {
		if _, err := bundle.LoadMessageFileFS(localeFiles, customerLocaleFile(locale)); err != nil {
			panic(err)
		}
	}
	return bundle
}()

// MatchCustomerLocale 按语言偏好列表的优先顺序返回首个可读的对客语言：主语言相同，且标签写明的书写系统与对客语言一致；简体与繁体中文都取中文。参数可以是单个语言标签或 Accept-Language 值，无匹配时返回 false。
func MatchCustomerLocale(languages string) (domain.CustomerLocale, bool) {
	tags, _, err := language.ParseAcceptLanguage(strings.TrimSpace(languages))
	if err != nil {
		return "", false
	}
	for _, tag := range tags {
		primary, script, _ := tag.Raw()
		for _, locale := range domain.CustomerLocales {
			localeTag := language.Make(string(locale))
			localePrimary, _, _ := localeTag.Raw()
			if primary != localePrimary {
				continue
			}
			// 写明的书写系统与对客语言的书写系统不同时跳过，中文简繁体除外。
			localeScript, _ := localeTag.Script()
			if script != (language.Script{}) && script != localeScript && primary.String() != "zh" {
				continue
			}
			return locale, true
		}
	}
	return "", false
}

// PreferredCustomerLocale 按语言偏好返回匹配的对客语言，无匹配时使用英文。
func PreferredCustomerLocale(languages string) domain.CustomerLocale {
	if locale, ok := MatchCustomerLocale(languages); ok {
		return locale
	}
	return domain.CustomerLocaleEnglishUnitedStates
}

// LocalizeCustomerTemplate 按对客语言用模板数据渲染对客文案；词条缺失或本地化失败时记录警告并回退返回键本身。
func LocalizeCustomerTemplate(locale domain.CustomerLocale, key Key, data map[string]any) string {
	localizer := goi18n.NewLocalizer(customerBundle, string(locale))
	merged := make(map[string]any, len(data)+1)
	maps.Copy(merged, data)
	merged["Product"] = brand.Current().Name(string(locale))
	message, err := localizer.Localize(&goi18n.LocalizeConfig{MessageID: string(key), TemplateData: merged})
	if err != nil {
		slog.Warn("本地化对客文案失败，回退返回文案键", "key", string(key), "locale", string(locale), "error", err)
		return string(key)
	}
	return message
}

// LocalizeCustomerMap 按对客语言将一组文案键翻译为对应文案；词条缺失或本地化失败时记录警告并回退返回键本身。
func LocalizeCustomerMap[K comparable](locale domain.CustomerLocale, keys map[K]Key) map[K]string {
	messages := make(map[K]string, len(keys))
	for name, key := range keys {
		messages[name] = LocalizeCustomerTemplate(locale, key, nil)
	}
	return messages
}
