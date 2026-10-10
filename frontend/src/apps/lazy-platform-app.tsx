/** 按需加载各平台的应用入口，入口模块可在启动时提前下载。 */
import { lazy } from "react"

import type { AppPlatform } from "@/platform/app-platform"

// 各平台应用入口模块的加载函数。
const platformAppLoaders = {
  web: () => import("@/apps/web/web-app"),
  desktop: () => import("@/apps/desktop/desktop-app"),
  mobile: () => import("@/apps/mobile/mobile-app"),
} satisfies Record<AppPlatform, () => Promise<unknown>>

const WebApp = lazy(platformAppLoaders.web)
const DesktopApp = lazy(platformAppLoaders.desktop)
const MobileApp = lazy(platformAppLoaders.mobile)

/** 提前下载当前平台的应用入口模块。 */
export function preloadPlatformApp(platform: AppPlatform) {
  return platformAppLoaders[platform]()
}

/** 渲染当前平台的应用入口；workspaceSlug 为当前地址所在的工作区，账号级页面为空。 */
export function LazyPlatformApp({ platform, workspaceSlug }: { platform: AppPlatform; workspaceSlug: string | null }) {
  if (platform === "web") return <WebApp workspaceSlug={workspaceSlug} />
  if (platform === "desktop") return <DesktopApp workspaceSlug={workspaceSlug} />
  return <MobileApp workspaceSlug={workspaceSlug} />
}
