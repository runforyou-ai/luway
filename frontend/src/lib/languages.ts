/** 提供 BCP 47 语言标签的比较、显示名称与常用翻译语言选项。 */

/** 无可识别语言内容的语言标签。 */
const undeterminedLanguage = "und"

/** 常用翻译语言，客服个人翻译语言与对客回复语言从中选择。 */
export const translationLanguages = [
  "zh-CN", "zh-TW", "en", "ja", "ko", "es", "pt", "fr", "de", "it", "ru", "ar", "hi", "hi-Latn",
  "bn", "id", "ms", "th", "vi", "fil", "tr", "fa", "ur", "nl", "pl", "uk",
]

/** 返回语言标签的主语言子标签。 */
function primaryLanguage(tag: string) {
  return tag.trim().split(/[-_]/)[0]?.toLowerCase() ?? ""
}

/** 返回语言标签的主语言与书写系统，书写系统未写明时按语言与地区推断，无法解析时为空。 */
function writtenLanguage(tag: string) {
  try {
    const locale = new Intl.Locale(tag.trim()).maximize()
    return `${locale.language}-${locale.script ?? ""}`
  } catch {
    return primaryLanguage(tag)
  }
}

/** 判断两个语言标签是否为同一种书面语言：主语言与书写系统都相同，忽略地区；无语言内容的标签不与任何语言相同。 */
export function sameLanguage(left: string, right: string) {
  const primary = primaryLanguage(left)
  return primary !== "" && primary !== undeterminedLanguage && primaryLanguage(right) !== undeterminedLanguage &&
    writtenLanguage(left) === writtenLanguage(right)
}

/** 判断读者无需翻译即可阅读该语言的文本：同一语言或无语言内容。 */
export function readableLanguage(text: string, reader: string) {
  return primaryLanguage(text) === undeterminedLanguage || sameLanguage(text, reader)
}

/** 按界面语言返回语言标签的显示名称，运行环境不支持时返回标签本身。 */
export function languageDisplayName(tag: string, uiLocale: string) {
  try {
    return new Intl.DisplayNames([uiLocale], { type: "language" }).of(tag) ?? tag
  } catch {
    return tag
  }
}
