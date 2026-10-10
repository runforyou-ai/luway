/** 构建在公开前端上挂接的工作台页面、设置菜单入口、平台工作区的行操作与词条；公开构建不挂接任何内容。 */
import type { LucideIcon } from "lucide-react"
import type { ReactElement, ReactNode } from "react"

import type { PermissionCode, PlatformWorkspace } from "@/api"
import type { ResourceRowAction } from "@/components/resource-table"
import type { SupportedLanguage } from "@/i18n/resources"

/** 挂接的工作台页面路由，地址相对工作区。 */
export type ExtensionRoute = {
  path: string
  element: ReactElement
  /** 进入页面所需的权限，未声明时所有成员可进入。 */
  permission?: PermissionCode
}

/** 设置菜单中的一个入口。 */
export type SettingsMenuLink = {
  to: string
  icon: LucideIcon
  label: string
}

/** 设置菜单追加的入口：工作区分组的末尾，集成分组的模型服务之后，平台分组的末尾。 */
export type SettingsMenuLinks = {
  workspace: SettingsMenuLink[]
  integrations: SettingsMenuLink[]
  platform: SettingsMenuLink[]
}

/** 平台工作区列表追加的行操作，以及随列表渲染的弹窗。 */
export type PlatformWorkspaceActions = {
  rowActions: (workspace: PlatformWorkspace) => ResourceRowAction[]
  dialogs: ReactNode
}

/** 按语言加载额外命名空间的词条，键为命名空间。 */
export type ExtensionLocaleLoader = () => Promise<Record<string, Record<string, unknown>>>

/** 一次构建挂接的全部内容；use 开头的成员是在对应组件内调用的 hook。 */
export type AppExtensions = {
  routes: readonly ExtensionRoute[]
  useSettingsMenuLinks: () => SettingsMenuLinks
  usePlatformWorkspaceActions: () => PlatformWorkspaceActions
  locales: Partial<Record<SupportedLanguage, ExtensionLocaleLoader>>
}

const noSettingsMenuLinks: SettingsMenuLinks = { workspace: [], integrations: [], platform: [] }
const noPlatformWorkspaceActions: PlatformWorkspaceActions = { rowActions: () => [], dialogs: null }

const none: AppExtensions = {
  routes: [],
  useSettingsMenuLinks: () => noSettingsMenuLinks,
  usePlatformWorkspaceActions: () => noPlatformWorkspaceActions,
  locales: {},
}

let current = none

/** 设置本次构建挂接的内容，只在应用启动前调用一次。 */
export function setAppExtensions(extensions: Partial<AppExtensions>) {
  current = { ...none, ...extensions }
}

/** 返回本次构建挂接的内容。 */
export function appExtensions() {
  return current
}
