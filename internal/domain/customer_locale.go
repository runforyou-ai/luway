package domain

// CustomerLocale 定义面向客户的界面、系统话术与通知邮件支持的语言。
type CustomerLocale string

const (
	CustomerLocaleChineseSimplified   CustomerLocale = "zh-CN"
	CustomerLocaleEnglishUnitedStates CustomerLocale = "en-US"
	CustomerLocaleHindiIndia          CustomerLocale = "hi-IN"
)

// CustomerLocales 是全部对客语言，按匹配优先顺序排列；新增对客语言时在此登记并补充对应词条文件。
var CustomerLocales = []CustomerLocale{
	CustomerLocaleChineseSimplified,
	CustomerLocaleEnglishUnitedStates,
	CustomerLocaleHindiIndia,
}
