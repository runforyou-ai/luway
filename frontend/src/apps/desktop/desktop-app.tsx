/** 桌面端应用入口和路由。 */
import { useEffect, useState } from "react"
import { Events, Window } from "@wailsio/runtime"

import { SharedAppRoutes } from "@/apps/shared-app-routes"
import { useClientUpdatePrompt } from "@/features/server-connection/use-client-update-prompt"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { subscribeSessionBoundary } from "@/lib/resource-client"
import { resolveDesktopOS } from "@/platform/app-platform"
import { isDesktopMainWindow } from "@/platform/desktop-window"
import { closeConversationWindows, onLocalComputerChanged } from "@/platform/native"

/** 同步原生窗口全屏状态。 */
function useWindowFullscreen(enabled: boolean) {
  const [fullscreen, setFullscreen] = useState(false)

  useEffect(() => {
    if (!enabled) {
      return
    }

    void Window.IsFullscreen()
      .then(setFullscreen)
      .catch((error: unknown) => {
        console.warn("读取窗口全屏状态失败", error)
      })

    const stopFullscreen = Events.On(
      Events.Types.Common.WindowFullscreen,
      () => setFullscreen(true),
    )
    const stopUnFullscreen = Events.On(
      Events.Types.Common.WindowUnFullscreen,
      () => setFullscreen(false),
    )

    return () => {
      stopFullscreen()
      stopUnFullscreen()
    }
  }, [enabled])

  return fullscreen
}

/** 本机电脑状态变化时失效本机电脑与本机环境的读取结果，未挂载的页面下次挂载时重新读取。 */
function useLocalComputerRefresh() {
  const invalidate = useResourceInvalidator()
  useEffect(
    () =>
      onLocalComputerChanged(() => {
        void invalidate(resourceKeys.currentComputer())
        void invalidate(resourceKeys.localEnvironment())
      }),
    [invalidate],
  )
}

/** 主窗口进入新的登录会话时关闭上一会话打开的会话独立窗口。 */
function useConversationWindowsClosing() {
  useEffect(() => {
    let unsubscribe = () => {}
    let stale = false
    void isDesktopMainWindow().then((main) => {
      if (!main || stale) return
      unsubscribe = subscribeSessionBoundary(() => {
        closeConversationWindows().catch((error: unknown) => console.warn("关闭会话独立窗口失败", error))
      })
    })
    return () => {
      stale = true
      unsubscribe()
    }
  }, [])
}

/** 渲染桌面端路由。 */
export default function DesktopApp({ workspaceSlug }: { workspaceSlug: string | null }) {
  const nativeOS = resolveDesktopOS()
  const fullscreen = useWindowFullscreen(nativeOS !== null)
  useLocalComputerRefresh()
  useConversationWindowsClosing()
  useClientUpdatePrompt()

  return (
    <div
      className="app-desktop-app min-h-dvh"
      data-native-os={nativeOS ?? undefined}
      data-window-fullscreen={fullscreen ? "true" : "false"}
    >
      <div aria-hidden="true" className="app-window-drag-region" />
      <SharedAppRoutes platform="desktop" workspaceSlug={workspaceSlug} />
    </div>
  )
}
