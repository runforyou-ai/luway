/** 初始化 i18next 与 Zod 通用校验文案，同步文档语言，并把当前语言下的品牌名称同步为页面标题。 */
import i18n from "i18next"
import { initReactI18next } from "react-i18next"
import { z } from "zod"

import { appExtensions } from "@/app-extensions"
import { brandName, currentBrand, subscribeBrand } from "@/lib/brand"

import {
  defaultNamespace,
  fallbackLanguage,
  localeLoaders,
  supportedLanguages,
  type SupportedLanguage,
} from "@/i18n/resources"

const localeStorageKey = "app.locale"

/** 判断语言是否受当前应用支持。 */
function isSupportedLanguage(language: string): language is SupportedLanguage {
  return supportedLanguages.some((supported) => supported === language)
}

/** 根据浏览器语言选择应用语言。 */
export function resolveBrowserLanguage() {
  const browserLanguage = navigator.languages[0] ?? navigator.language
  const browserLocale = new Intl.Locale(browserLanguage).maximize()

  if (browserLocale.language === "zh" && browserLocale.script === "Hans") {
    return "zh-CN"
  }

  if (browserLocale.language === "en") {
    return "en-US"
  }

  return fallbackLanguage
}

/** 加载指定语言的核心词条与构建挂接的额外命名空间词条。 */
async function loadLocale(language: SupportedLanguage) {
  const [core, extension] = await Promise.all([localeLoaders[language](), appExtensions().locales[language]?.() ?? {}])
  return { ...extension, ...core }
}

/** 加载指定语言的全部命名空间，已加载的语言直接返回。 */
async function loadLanguage(language: SupportedLanguage) {
  if (i18n.hasResourceBundle(language, defaultNamespace)) return
  const namespaces = await loadLocale(language)
  for (const [namespace, resource] of Object.entries(namespaces)) {
    i18n.addResourceBundle(language, namespace, resource)
  }
}

// 语言切换请求代次，只应用最近一次选择。
let languageRequest = 0

/** 切换界面语言并同步本机启动缓存；加载期间出现更新的选择时放弃本次切换。 */
export async function changeAppLanguage(language: string) {
  if (!isSupportedLanguage(language)) {
    console.warn("忽略不支持的界面语言", { language })
    return
  }
  const request = ++languageRequest
  await loadLanguage(language)
  if (request !== languageRequest) return
  await i18n.changeLanguage(language)
  // 缓存当前界面语言供下次启动使用。
  try {
    window.localStorage.setItem(localeStorageKey, language)
  } catch (error) {
    console.warn("保存本机语言偏好失败", error)
  }
}

/** 把当前语言下的产品名称设为页面标题。 */
function syncBrandTitle() {
  document.title = brandName(currentBrand(), i18n.language)
}

i18n.on("languageChanged", (language) => {
  // 把文档语言和阅读方向同步到当前语言。
  document.documentElement.lang = language
  document.documentElement.dir = i18n.dir(language)
  syncBrandTitle()
})

subscribeBrand(syncBrandTitle)

/** 按校验问题类型返回当前语言的通用校验文案；未覆盖的问题交给 Zod 默认文案，schema 中显式给出的文案优先。 */
const validationErrorMap: z.core.$ZodErrorMap = (issue) => {
  switch (issue.code) {
    case "invalid_type":
      return issue.input === undefined || issue.input === null
        ? i18n.t("common:validation.required")
        : i18n.t("common:validation.formatInvalid")
    case "too_small":
      if (issue.origin === "string") {
        const min = Number(issue.minimum)
        return min <= 1 ? i18n.t("common:validation.required") : i18n.t("common:validation.tooShort", { min })
      }
      if (issue.origin === "array" || issue.origin === "set") {
        return i18n.t("common:validation.tooFewItems", { count: Number(issue.minimum) })
      }
      return undefined
    case "too_big":
      if (issue.origin === "string") return i18n.t("common:validation.tooLong", { max: Number(issue.maximum) })
      if (issue.origin === "array" || issue.origin === "set") {
        return i18n.t("common:validation.tooManyItems", { count: Number(issue.maximum) })
      }
      return undefined
    case "invalid_format":
      if (issue.format === "email") return i18n.t("common:validation.emailInvalid")
      if (issue.format === "url") return i18n.t("common:validation.urlInvalid")
      return i18n.t("common:validation.formatInvalid")
    case "invalid_value":
      return i18n.t("common:validation.optionInvalid")
    default:
      return undefined
  }
}

/** 初始化国际化资源，并让 Zod 在每次校验时按当前语言生成通用校验文案。 */
export async function initializeI18n() {
  // 读取本机缓存的最近一次界面语言。
  let storedLanguage: SupportedLanguage | null = null
  try {
    const language = window.localStorage.getItem(localeStorageKey)
    storedLanguage =
      language && isSupportedLanguage(language) ? language : null
  } catch (error) {
    console.warn("读取本机语言偏好失败", error)
  }
  const language = storedLanguage ?? resolveBrowserLanguage()

  await i18n.use(initReactI18next).init({
    resources: { [language]: await loadLocale(language) },
    lng: language,
    defaultNS: defaultNamespace,
    fallbackLng: fallbackLanguage,
    supportedLngs: supportedLanguages,
    returnNull: false,
    interpolation: {
      escapeValue: false,
    },
    react: {
      useSuspense: false,
    },
  })
  z.config({ customError: validationErrorMap })

  return i18n
}

export { i18n }
