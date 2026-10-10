/** 维护界面使用的产品品牌：构建品牌为默认值，启动检测后改用服务端下发的品牌。 */
import { useSyncExternalStore } from "react"
import { useTranslation } from "react-i18next"

import type { Brand } from "@/api"

/** 产品名称的回退语言。 */
const defaultLocale = "en-US"

let current: Brand = __BUILD_BRAND__
const listeners = new Set<() => void>()

/** 按界面语言返回产品名称：先精确匹配语言标签，再匹配主语言，都没有时使用 en-US 名称。 */
export function brandName(brand: Brand, language: string) {
  const names = brand.names ?? {}
  const exact = names[language]
  if (exact) return exact
  const base = language.split("-")[0]
  const matched = Object.keys(names)
    .sort()
    .find((locale) => locale.split("-")[0] === base)
  return (matched && names[matched]) || names[defaultLocale] || ""
}

/** 返回网站嵌入脚本在宿主页使用的全局对象名、设置对象名和打开挂件的属性名。 */
export function embedSDKNames(brand: Brand) {
  const name = brand.sdkName
  return {
    global: name,
    settings: `${name.charAt(0).toLowerCase()}${name.slice(1)}Settings`,
    openAttribute: `data-${name.toLowerCase()}-open`,
  }
}

/** 读取当前品牌。 */
export function currentBrand() {
  return current
}

/** 替换当前品牌并通知订阅者。 */
export function applyBrand(brand: Brand) {
  current = brand
  for (const listener of listeners) listener()
}

/** 订阅品牌变化，返回取消订阅函数。 */
export function subscribeBrand(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** 读取当前品牌并在品牌变化时刷新组件。 */
export function useBrand() {
  return useSyncExternalStore(subscribeBrand, currentBrand)
}

/** 返回当前界面语言下的产品名称。 */
export function useBrandName() {
  const brand = useBrand()
  const { i18n } = useTranslation()
  return brandName(brand, i18n.language)
}
