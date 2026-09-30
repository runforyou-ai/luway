/** 注册应用支持的语言和按语言加载的翻译资源。 */
import type enUS from "@/i18n/locales/en-US"

type LocaleShape<T> = {
  readonly [Key in keyof T]: T[Key] extends string
    ? string
    : LocaleShape<T[Key]>
}

export type LocaleResources = typeof enUS

export const defaultNamespace = "common"
export const fallbackLanguage = "en-US"
export const supportedLanguages = ["zh-CN", "en-US"] as const

export type SupportedLanguage = (typeof supportedLanguages)[number]

/** 各语言翻译资源的独立加载入口，词条结构以英文为准。 */
export const localeLoaders: Record<
  SupportedLanguage,
  () => Promise<LocaleShape<LocaleResources>>
> = {
  "en-US": () => import("@/i18n/locales/en-US").then((module) => module.default),
  "zh-CN": () => import("@/i18n/locales/zh-CN").then((module) => module.default),
}
