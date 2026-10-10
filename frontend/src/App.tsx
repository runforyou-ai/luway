/** 按运行平台加载 Web、桌面端或移动端应用。 */
import { Suspense, type CSSProperties } from "react"

import { LazyPlatformApp } from "@/apps/lazy-platform-app"
import { PageLoading } from "@/components/page-loading"
import { Toaster } from "@/components/ui/sonner"
import { useNotificationOpenNavigation } from "@/features/notifications/use-notification-open-navigation"
import { useServerLinkNavigation } from "@/features/server-connection/use-server-link-navigation"
import { StartupBootstrap } from "@/features/startup/startup-bootstrap"
import type { AppPlatform } from "@/platform/app-platform"

/** 启动检测完成后登记原生端连接链接处理。 */
function ServerLinkNavigation() {
  useServerLinkNavigation()
  return null
}

/** 根应用，按平台渲染对应入口；workspaceSlug 为当前地址所在的工作区，账号级页面为空。 */
function App({ platform, workspaceSlug }: { platform: AppPlatform; workspaceSlug: string | null }) {
  const mobile = platform === "mobile"
  // 通知跳转覆盖工作区内与账号级页面。
  useNotificationOpenNavigation()
  // 移动端轻提示容器横跨视口，胶囊在容器内水平居中。
  const mobileToastOffset = mobile
    ? {
        top: "calc(env(safe-area-inset-top) + 0.75rem)",
        right: "0px",
        left: "0px",
      }
    : undefined

  return (
    <>
      <StartupBootstrap>
        <ServerLinkNavigation />
        <Suspense
          fallback={
            <PageLoading />
          }
        >
          <LazyPlatformApp platform={platform} workspaceSlug={workspaceSlug} />
        </Suspense>
      </StartupBootstrap>
      <Toaster
        position={mobile ? "top-center" : undefined}
        offset={mobileToastOffset}
        mobileOffset={mobileToastOffset}
        closeButton={!mobile}
        style={
          mobile ? ({ "--width": "100vw" } as CSSProperties) : undefined
        }
        toastOptions={
          mobile
            ? {
                classNames: {
                  toast:
                    "inset-x-0! mx-auto! w-fit! max-w-[calc(100%-2rem)]! rounded-full! border-0! bg-neutral-900/85! px-4! py-2.5! text-white! shadow-[0_4px_24px_rgb(0_0_0/0.16)]! backdrop-blur-md! dark:bg-neutral-700/90!",
                  title: "text-sm! leading-5! font-medium!",
                  description: "text-[13px]! leading-5! text-white/70!",
                  icon: "hidden!",
                },
              }
            : undefined
        }
      />
    </>
  )
}

export default App
