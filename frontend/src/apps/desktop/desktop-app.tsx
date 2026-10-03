/** 桌面端应用入口和路由。 */
import { useEffect, useState } from "react"
import { Events, Window } from "@wailsio/runtime"

import { onLocalDeviceChanged } from "@/api"
import { SharedAppRoutes } from "@/apps/shared-app-routes"
import { useClientUpdatePrompt } from "@/features/server-connection/use-client-update-prompt"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { resolveDesktopOS } from "@/platform/app-platform"

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

/** 本机设备状态变化时失效本机设备与本机环境的读取结果，未挂载的页面下次挂载时重新读取。 */
function useLocalDeviceRefresh() {
  const invalidate = useResourceInvalidator()
  useEffect(
    () =>
      onLocalDeviceChanged(() => {
        void invalidate(resourceKeys.currentDevice())
        void invalidate(resourceKeys.localEnvironment())
      }),
    [invalidate],
  )
}

/** 渲染桌面端路由。 */
export default function DesktopApp({ workspaceSlug }: { workspaceSlug: string | null }) {
  const nativeOS = resolveDesktopOS()
  const fullscreen = useWindowFullscreen(nativeOS === "darwin")
  useLocalDeviceRefresh()
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
