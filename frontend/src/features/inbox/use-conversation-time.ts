/** 桌面端与移动端共用的会话时间显示和分钟刷新。 */
import { useCallback, useMemo, useSyncExternalStore } from "react"
import { useTranslation } from "react-i18next"
import { useUserTimeZone } from "@/contexts/user-preferences"
import { createConversationTimeFormatter } from "./conversation-time"

/** 按当前语言与用户时区格式化最近消息时间。 */
export function useConversationTime() {
  const { t, i18n } = useTranslation("inbox")
  const timeZone = useUserTimeZone()
  return useMemo(
    () => createConversationTimeFormatter(i18n.resolvedLanguage, timeZone, {
      justNow: t("justNow"),
      yesterday: t("yesterday"),
    }),
    [i18n.resolvedLanguage, t, timeZone],
  )
}

/** 把等待起点格式化为等待时长，不足一分钟按一分钟计。 */
export function useWaitingDuration() {
  const { t } = useTranslation("inbox")
  return useCallback((since: string) => {
    const minutes = Math.max(1, Math.floor((Date.now() - new Date(since).getTime()) / 60_000))
    if (minutes < 60) return t("waitingMinutes", { count: minutes })
    if (minutes < 24 * 60) return t("waitingHours", { count: Math.floor(minutes / 60) })
    return t("waitingDays", { count: Math.floor(minutes / (24 * 60)) })
  }, [t])
}

// 分钟刷新由全部订阅者共用一个计时器，最后一个订阅者退订时停止。
let minuteTick = 0
let minuteTimer: number | undefined
const minuteListeners = new Set<() => void>()

/** 订阅分钟刷新，首个订阅者启动计时器。 */
function subscribeMinuteTick(listener: () => void) {
  minuteListeners.add(listener)
  if (minuteTimer === undefined) {
    minuteTimer = window.setInterval(() => {
      minuteTick += 1
      for (const notify of minuteListeners) notify()
    }, 60_000)
  }
  return () => {
    minuteListeners.delete(listener)
    if (minuteListeners.size === 0) {
      window.clearInterval(minuteTimer)
      minuteTimer = undefined
    }
  }
}

/** 每分钟触发一次重渲染，保持相对时间新鲜。 */
export function useMinuteTick() {
  useSyncExternalStore(subscribeMinuteTick, () => minuteTick)
}
