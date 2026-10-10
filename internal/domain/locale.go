package domain

// Locale 定义应用支持的本地化语言。
type Locale string

const (
	LocaleChineseSimplified   Locale = "zh-CN"
	LocaleEnglishUnitedStates Locale = "en-US"
)

// Valid 判断语言是否为受支持的取值。
func (locale Locale) Valid() bool {
	return locale == LocaleChineseSimplified || locale == LocaleEnglishUnitedStates
}
