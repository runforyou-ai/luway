/** 初始化国际化并挂载前端应用。 */
import React, { useEffect, useMemo, useRef, useState } from "react"
import ReactDOM from "react-dom/client"
import { QueryClientProvider } from "@tanstack/react-query"
import { ThemeProvider } from "next-themes"
import { createHashRouter } from "react-router"
import { RouterProvider } from "react-router/dom"

import App from "@/App"
import { preloadPlatformApp } from "@/apps/lazy-platform-app"
import { TooltipProvider } from "@/components/ui/tooltip"
import { initializeI18n } from "@/i18n"
import "@/index.css"
import { beginWorkspaceBoundary, resourceClient } from "@/lib/resource-client"
import { workspaceBasename, workspaceSlugFromHash } from "@/lib/workspace-route"
import { resolveAppPlatform, type AppPlatform } from "@/platform/app-platform"
import { scheduleDesktopWindowReveal } from "@/platform/desktop-window"

/** 按地址中的工作区标识创建哈希路由器：工作区地址以 /w/<标识> 为 basename，切换工作区时重建路由器并进入新的会话代次，账号级数据保留。 */
function AppRouter({ platform }: { platform: AppPlatform }) {
  const [workspaceSlug, setWorkspaceSlug] = useState(() => workspaceSlugFromHash(window.location.hash))
  const currentSlug = useRef(workspaceSlug)

  useEffect(() => {
    /** 地址进入、离开或切换工作区时同步路由器，换到其他工作区前丢弃上一工作区的缓存与实时连接。 */
    function syncWorkspace() {
      const nextSlug = workspaceSlugFromHash(window.location.hash)
      if (nextSlug === currentSlug.current) return
      currentSlug.current = nextSlug
      beginWorkspaceBoundary()
      setWorkspaceSlug(nextSlug)
    }
    window.addEventListener("hashchange", syncWorkspace)
    window.addEventListener("popstate", syncWorkspace)
    return () => {
      window.removeEventListener("hashchange", syncWorkspace)
      window.removeEventListener("popstate", syncWorkspace)
    }
  }, [])

  const router = useMemo(
    () =>
      createHashRouter(
        [{ path: "*", element: <App platform={platform} workspaceSlug={workspaceSlug} /> }],
        workspaceSlug ? { basename: workspaceBasename(workspaceSlug) } : undefined,
      ),
    [platform, workspaceSlug],
  )
  return <RouterProvider key={workspaceSlug ?? ""} router={router} />
}

/** 启动前端应用。 */
async function bootstrap() {
  const platform = resolveAppPlatform()
  // 桌面端主窗口隐藏创建，启动检测或加载过久时按当前地址显示。
  scheduleDesktopWindowReveal(3000)
  // 平台入口模块与界面词条同时下载，加载失败由渲染时的懒加载报告。
  void preloadPlatformApp(platform).catch(() => undefined)
  await initializeI18n()
  // 根元素标记平台，样式用 touch: 变体给移动端触屏尺寸。
  document.documentElement.dataset.platform = platform
  // Web 和桌面端禁用原生右键菜单。
  if (platform !== "mobile") {
    document.addEventListener("contextmenu", (event) => {
      event.preventDefault()
    })
  }

  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
    <React.StrictMode>
      <QueryClientProvider client={resourceClient}>
        <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
          <TooltipProvider>
            <AppRouter platform={platform} />
          </TooltipProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </React.StrictMode>,
  )
}

void bootstrap().catch((error: unknown) => {
  console.error("应用初始化失败", error)
})
