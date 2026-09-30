/** 控制成员收件箱和当前会话的前台轮询。 */
import { useSyncExternalStore } from "react"

import { usePortalContainer } from "@/components/ui/portal-container"

export const memberChatPollingInterval = 3_000

// 窗口前台状态由全部订阅者共用一组监听，最后一个订阅者退订时移除。
const windowStateListeners = new Set<() => void>()

/** 通知全部订阅者窗口前台状态可能变化。 */
function notifyWindowState() {
  for (const notify of windowStateListeners) notify()
}

/** 订阅浏览器或桌面窗口的可见与焦点变化，首个订阅者注册监听。 */
function subscribeWindowState(listener: () => void) {
  if (windowStateListeners.size === 0) {
    document.addEventListener("visibilitychange", notifyWindowState)
    window.addEventListener("focus", notifyWindowState)
    window.addEventListener("blur", notifyWindowState)
  }
  windowStateListeners.add(listener)
  return () => {
    windowStateListeners.delete(listener)
    if (windowStateListeners.size > 0) return
    document.removeEventListener("visibilitychange", notifyWindowState)
    window.removeEventListener("focus", notifyWindowState)
    window.removeEventListener("blur", notifyWindowState)
  }
}

/** 返回页面是否可见。 */
function windowVisible() {
  return document.visibilityState === "visible"
}

/** 返回页面是否可见且窗口持有焦点。 */
function windowFocused() {
  return document.visibilityState === "visible" && document.hasFocus()
}

/** 判断当前消息页是否处于允许成员消息轮询的前台状态。 */
export function useMemberChatPollingActive({
  requireWindowFocus = true,
}: {
  requireWindowFocus?: boolean
} = {}) {
  const pagePortal = usePortalContainer()
  const windowActive = useSyncExternalStore(
    subscribeWindowState,
    requireWindowFocus ? windowFocused : windowVisible,
  )
  return (pagePortal?.active ?? true) && windowActive
}
