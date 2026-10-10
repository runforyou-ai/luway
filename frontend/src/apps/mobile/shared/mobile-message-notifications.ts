/** 移动端用户通知与应用角标。 */
import { useEffect } from "react"

import { WorkStatus, type Identity } from "@/api"
import { mobileNotificationPath } from "@/apps/mobile/shared/mobile-navigation"
import { useWorkspaceScope } from "@/contexts/workspace-scope-context"
import { loadInboxAttention } from "@/features/inbox/list/inbox-attention"
import { useUserNotifications } from "@/features/notifications/use-user-notifications"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { useWorkspaceActivityConnection, useWorkspaceAttention } from "@/hooks/use-workspace-attention"
import { NotificationPermissionStatus } from "@/platform/native"
import {
  markNotificationPermissionRequested,
  readNotificationDevicePreferences,
  updateNotificationUnreadIndicator,
} from "@/platform/notifications"
import { checkNotificationPermission, requestNotificationPermission } from "@/platform/system"

/** 按当前身份投递用户通知，并把提醒总数同步到应用角标。 */
export function useMobileMessageNotifications(identity: Identity | null) {
  const workspaceId = identity?.workspace.id
  const userId = identity?.user.id
  const notificationWorkspaceId = identity?.user.workspaceId
  const messageNotificationsEnabled = identity?.user.messageNotificationsEnabled
  const workStatus = identity?.user.workStatus

  /** 登录后为本设备自动申请一次系统通知授权，之后交给偏好设置。 */
  useEffect(() => {
    if (!notificationWorkspaceId || !userId) {
      return
    }
    const scope = { workspaceId: notificationWorkspaceId, userId }
    if (readNotificationDevicePreferences(scope).permissionAutoRequested) {
      return
    }
    void (async () => {
      const status = await checkNotificationPermission()
      // 仅在系统尚未记录选择时申请，已授权或已拒绝时跳过。
      if (
        status !==
        NotificationPermissionStatus.NotificationPermissionStatusPrompt
      ) {
        return
      }
      markNotificationPermissionRequested(scope)
      await requestNotificationPermission()
    })().catch((error) => {
      console.warn("申请移动端通知权限失败", error)
    })
  }, [notificationWorkspaceId, userId])

  // 提醒总数按权威查询读取，与底部导航角标共用同一份缓存。
  const attention = useResource(
    resourceKeys.inboxAttention({
      workspaceId: workspaceId ?? "",
      userId: userId ?? "",
    }),
    loadInboxAttention,
    { enabled: Boolean(workspaceId && userId) },
  )
  // 其他工作区的提醒计入应用角标，由工作区动态事件流刷新。
  const workspaceScope = useWorkspaceScope()
  useWorkspaceActivityConnection(
    workspaceScope.current.id,
    workspaceScope.workspaces.map((workspace) => workspace.id),
  )
  const otherWorkspacesUnread = useWorkspaceAttention(workspaceScope.current.id).others
  // 服务端生成的用户通知投递为系统通知。
  useUserNotifications(workspaceScope.current.id, workspaceScope.workspaces, mobileNotificationPath, () => {})
  // 应用角标合计聊天提醒未读数与待处理会话中的未读消息数，待处理总数不计入。
  const unreadCount = attention.data === undefined ? undefined : attention.data.total + otherWorkspacesUnread
  const attentionEnabled =
    Boolean(messageNotificationsEnabled) &&
    workStatus === WorkStatus.Working

  /** 同步应用图标角标，提醒总数读到之前不改动角标。 */
  useEffect(() => {
    if (!userId || unreadCount === undefined) {
      return
    }
    void updateNotificationUnreadIndicator({
      count: unreadCount,
      attentionEnabled,
      attentionPending: false,
    }).catch((error) => {
      console.warn("同步移动端未读角标失败", { count: unreadCount, error })
    })
  }, [userId, unreadCount, attentionEnabled])

  /** 离开登录工作区时清除应用角标。 */
  useEffect(() => {
    return () => {
      void updateNotificationUnreadIndicator({
        count: 0,
        attentionEnabled: false,
        attentionPending: false,
      }).catch((error) => {
        console.warn("清除移动端未读角标失败", error)
      })
    }
  }, [])
}
