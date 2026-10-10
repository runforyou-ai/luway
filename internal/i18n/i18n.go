// Package i18n 提供后端用户可见文案的本地化能力，文案键常量由词条文件生成。
package i18n

//go:generate go run github.com/runforyou-ai/luway/internal/tools/i18nkeygen

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"maps"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/runforyou-ai/luway/internal/common/brand"
	"golang.org/x/text/language"
)

// Key 标识一条后端本地化文案。
type Key string

// localeFiles 是内嵌的应用语言与对客语言词条文件。
//
//go:embed locales/*.json locales/customer/*.json
var localeFiles embed.FS

// appLocaleFiles 是应用语言的完整词条文件，首个为回退语言。
var appLocaleFiles = []string{"locales/en-US.json", "locales/zh-CN.json"}

// bundle 是应用语言的翻译词条。
var bundle = func() *goi18n.Bundle {
	bundle := goi18n.NewBundle(language.AmericanEnglish)
	for _, path := range appLocaleFiles {
		if _, err := bundle.LoadMessageFileFS(localeFiles, path); err != nil {
			panic(err)
		}
	}
	return bundle
}()

// appMatcher 按语言偏好匹配应用语言。
var appMatcher = language.NewMatcher(bundle.LanguageTags())

// LoadMessageFiles 在程序包初始化阶段加载额外词条；所有调用须在启动 goroutine 与处理请求之前完成。
func LoadMessageFiles(files fs.FS, paths ...string) {
	for _, path := range paths {
		if _, err := bundle.LoadMessageFileFS(files, path); err != nil {
			panic(err)
		}
		if _, err := customerBundle.LoadMessageFileFS(files, path); err != nil {
			panic(err)
		}
	}
	appMatcher = language.NewMatcher(bundle.LanguageTags())
}

// productData 返回在模板数据中加入当前品牌产品名称的副本，产品名称按语言偏好匹配到的应用语言选取。
func productData(acceptLanguage string, data map[string]any) map[string]any {
	tags, _, _ := language.ParseAcceptLanguage(acceptLanguage)
	tag, _, _ := appMatcher.Match(tags...)
	merged := make(map[string]any, len(data)+1)
	maps.Copy(merged, data)
	merged["Product"] = brand.Current().Name(tag.String())
	return merged
}

// Localize 根据语言偏好返回本地化文案和最终匹配的语言；词条缺失或本地化失败时记录警告并回退返回键本身。
func Localize(acceptLanguage string, key Key) (string, string) {
	localizer := goi18n.NewLocalizer(bundle, acceptLanguage)
	message, tag, err := localizer.LocalizeWithTag(&goi18n.LocalizeConfig{MessageID: string(key), TemplateData: productData(acceptLanguage, nil)})
	if err != nil {
		slog.WarnContext(context.Background(), "本地化文案失败，回退返回文案键", "key", string(key), "locale", acceptLanguage, "error", err)
		return string(key), tag.String()
	}
	return message, tag.String()
}

// LocalizeTemplate 根据语言偏好用模板数据渲染本地化文案；词条缺失或本地化失败时记录警告并回退返回键本身。
func LocalizeTemplate(acceptLanguage string, key Key, data map[string]any) string {
	localizer := goi18n.NewLocalizer(bundle, acceptLanguage)
	message, err := localizer.Localize(&goi18n.LocalizeConfig{MessageID: string(key), TemplateData: productData(acceptLanguage, data)})
	if err != nil {
		slog.WarnContext(context.Background(), "本地化文案失败，回退返回文案键", "key", string(key), "locale", acceptLanguage, "error", err)
		return string(key)
	}
	return message
}

// LocalizeMap 将一组文案键翻译为对应文案；词条缺失或本地化失败时记录警告并回退返回键本身。
func LocalizeMap[K comparable](acceptLanguage string, keys map[K]Key) map[K]string {
	if len(keys) == 0 {
		return nil
	}
	localizer := goi18n.NewLocalizer(bundle, acceptLanguage)
	data := productData(acceptLanguage, nil)
	messages := make(map[K]string, len(keys))
	for name, key := range keys {
		message, err := localizer.Localize(&goi18n.LocalizeConfig{MessageID: string(key), TemplateData: data})
		if err != nil {
			slog.WarnContext(context.Background(), "本地化文案失败，回退返回文案键", "key", string(key), "locale", acceptLanguage, "error", err)
			message = string(key)
		}
		messages[name] = message
	}
	return messages
}
