/** 为 i18next 声明当前项目的命名空间和词条类型。 */
import "i18next"

import type { defaultNamespace, LocaleResources } from "@/i18n/resources"

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: typeof defaultNamespace
    resources: LocaleResources
    returnNull: false
  }
}
