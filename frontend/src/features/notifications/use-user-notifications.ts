/** 把服务端生成的用户通知投递为各端的系统通知：当前工作区的通知在用户正在查看应用时不弹出，其他工作区的通知照常弹出，点击后进入对应工作区的会话。 */
import { useEffect, useEffectEvent } from "react"

import { workspaceActivityClient, type Workspace } from "@/api"
import type { RealtimeServerFrame } from "@/api/realtime/protocol"
import { currentSessionGeneration } from "@/api/session-scope"
import type { NotificationTarget } from "@/lib/workspace-paths"
import { workspaceHref } from "@/lib/workspace-route"
import { resolveAppPlatform } from "@/platform/app-platform"
import {
  canSendNotification,
  readNotificationDevicePreferences,
} from "@/platform/notifications"
import { checkNotificationPermission, deliverMessageNotification } from "@/platform/system"

type UserNotificationFrame = Extract<RealtimeServerFrame, { type: "user_notification" }>

let deliveryQueue: Promise<void> = Promise.resolve()

/** 判断用户是否正在查看应用。 */
function isApplicationVisible() {
  if (document.visibilityState !== "visible") {
    return false
  }
  // 移动端 WebView 没有窗口焦点语义，应用在前台即视为正在查看。
  return resolveAppPlatform() === "mobile" || document.hasFocus()
}

/** 在工作区外壳内投递本人在各工作区的用户通知；conversationPath 给出会话在工作区内的页面，onDelivered 在当前工作区的通知投递后回调。 */
export function useUserNotifications(
  currentWorkspaceId: string,
  workspaces: Workspace[],
  conversationPath: (target: NotificationTarget) => string,
  onDelivered: () => void,
) {
  const deliver = useEffectEvent((frame: UserNotificationFrame) => {
    const workspace = workspaces.find((item) => item.id === frame.workspaceId)
    if (!workspace) {
      return
    }
    const current = frame.workspaceId === currentWorkspaceId
    // 登录、退出、换账号与切换工作区都会进入新的登录会话代次，之前入队的通知随之作废。
    const generation = currentSessionGeneration()
    /** 判断通知仍可投递：登录会话代次未变，当前工作区的通知只在用户未查看应用时弹出。 */
    const deliverable = () => currentSessionGeneration() === generation && !(current && isApplicationVisible())
    const delivery = deliveryQueue.then(async () => {
      if (!deliverable() || !canSendNotification(await checkNotificationPermission()) || !deliverable()) {
        return
      }
      const { soundEnabled } = readNotificationDevicePreferences({ workspaceId: frame.workspaceId, userId: frame.userId })
      await deliverMessageNotification({
        id: frame.id,
        title: frame.title,
        body: frame.body,
        soundEnabled,
        path: workspaceHref(workspace.slug, conversationPath({ view: frame.view, conversationId: frame.conversationId })),
      })
      if (current) {
        onDelivered()
      }
    })
    deliveryQueue = delivery.catch((error: unknown) => {
      console.warn("投递用户通知失败", { notification_id: frame.id, error })
    })
  })

  useEffect(
    () =>
      workspaceActivityClient.subscribe((event) => {
        if (event.type === "frame" && event.frame.type === "user_notification") {
          deliver(event.frame)
        }
      }),
    [],
  )
}
