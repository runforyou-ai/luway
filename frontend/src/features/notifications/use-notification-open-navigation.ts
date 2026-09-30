/** 点击系统通知后，主界面打开通知对应的会话。 */
import { useEffect } from "react"

import { isApiError, loadAccount, onNotificationOpened, sessionPath, SessionState, takeOpenedNotificationPath } from "@/api"
import { confirmActiveUnsavedChanges } from "@/components/unsaved-changes-guard"
import {
  openNotificationPath,
  registerNotificationNavigator,
} from "@/lib/notification-open-queue"
import { navigateToHashPath, workspaceSlugFromHash } from "@/lib/workspace-route"
import { rememberPendingReturnPath } from "@/lib/login-return"
import { resolveAppPlatform } from "@/platform/app-platform"

// 会话独立窗口以会话地址启动，不响应通知点击，由主窗口打开。
const standaloneConversationWindow = /^#\/w\/[^/]+\/conversations\//.test(window.location.hash)

// 原生端读取待打开页面的调用串行进行，重新挂载时取到的页面交给当时登记的跳转处理。
let nativeReads = Promise.resolve()

/** 读取原生端待打开的通知页面并交给跳转处理。 */
function readNativeOpenedPath() {
  nativeReads = nativeReads
    .then(async () => openNotificationPath(await takeOpenedNotificationPath()))
    .catch((error: unknown) => {
      console.warn("读取通知对应的页面失败", error)
    })
}

/** 跳转到通知对应的工作区页面：账号会话尚不可用（未登录、未连接服务器）时记住页面并前往登录或连接，完成后由工作区入口打开；当前页面有未保存内容时先确认。 */
async function navigateToNotificationPath(path: string) {
  if (!workspaceSlugFromHash(path)) return
  try {
    await loadAccount()
  } catch (error) {
    const state = isApiError(error) ? error.state : ""
    const entry = state && state !== SessionState.SessionStateWorkspace ? sessionPath(state) : null
    if (entry) {
      rememberPendingReturnPath(path)
      navigateToHashPath(entry, { replace: true })
      return
    }
    // 其他读取失败照常前往，由工作区页面按会话状态处理。
  }
  if (!(await confirmActiveUnsavedChanges())) return
  navigateToHashPath(path)
}

/** 在主界面登记通知跳转：Web 由浏览器通知的点击交来页面；原生端挂载时读取点击通知唤起应用时暂存的页面，之后在每次通知被点击时读取。 */
export function useNotificationOpenNavigation() {
  useEffect(() => {
    if (standaloneConversationWindow) return
    const unregister = registerNotificationNavigator((path) => void navigateToNotificationPath(path))
    if (resolveAppPlatform() === "web") return unregister
    readNativeOpenedPath()
    const unsubscribe = onNotificationOpened(readNativeOpenedPath)
    return () => {
      unsubscribe()
      unregister()
    }
  }, [])
}
