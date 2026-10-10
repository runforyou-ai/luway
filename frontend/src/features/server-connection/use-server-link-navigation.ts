/** 原生端被连接链接唤起后进入连接页，预填链接携带的部署地址并检测。 */
import { useEffect } from "react"

import { serverURL as currentServerURL } from "@/api"
import { confirmActiveUnsavedChanges } from "@/components/unsaved-changes-guard"
import { offerServerLink } from "@/lib/server-link-queue"
import { navigateToHashPath } from "@/lib/workspace-route"
import { resolveAppPlatform } from "@/platform/app-platform"
import { onServerLinkOpened, takeOpenedServerLink } from "@/platform/native"

// 会话独立窗口以会话地址启动，不响应连接链接，由主窗口处理。
const standaloneConversationWindow = /^#\/w\/[^/]+\/conversations\//.test(window.location.hash)

// 读取待处理链接的调用串行进行，同一链接只处理一次。
let nativeReads = Promise.resolve()

/** 读取原生端待处理的连接链接：与当前服务器相同时保持当前页面，否则确认未保存内容后进入连接页。 */
function readOpenedServerLink() {
  nativeReads = nativeReads
    .then(async () => {
      const serverURL = await takeOpenedServerLink()
      if (!serverURL) return
      if (currentServerURL() === serverURL.replace(/\/+$/, "")) return
      if (!(await confirmActiveUnsavedChanges())) return
      offerServerLink(serverURL)
      navigateToHashPath("/connect")
    })
    .catch((error: unknown) => {
      console.warn("读取连接链接失败", error)
    })
}

/** 在原生端主界面登记连接链接处理：挂载时读取唤起应用时暂存的链接，之后在每次收到链接时读取。 */
export function useServerLinkNavigation() {
  useEffect(() => {
    if (standaloneConversationWindow || resolveAppPlatform() === "web") return
    readOpenedServerLink()
    return onServerLinkOpened(readOpenedServerLink)
  }, [])
}
