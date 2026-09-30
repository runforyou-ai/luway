/** 当前会话的新消息通知策略与投递编排。 */
import {
  WorkStatus,
  type MessageNotificationInput,
} from "@/api"
import { currentSessionGeneration } from "@/api/session-scope"
import { resolveAppPlatform } from "@/platform/app-platform"
import {
  canSendNotification,
  checkNotificationPermission,
  deliverMessageNotification,
  readNotificationDevicePreferences,
  type NotificationDeviceScope,
} from "@/platform/notifications"

type NewMessageNotificationOptions = Omit<
  MessageNotificationInput,
  "soundEnabled"
> & {
  scope: NotificationDeviceScope
}

type NotificationPolicy = {
  token: symbol
  scope: NotificationDeviceScope
  attentionEnabled: boolean
}

let activeNotificationPolicy: NotificationPolicy | null = null
let messageNotificationQueue: Promise<void> = Promise.resolve()

/** 判断当前会话策略是否仍允许发送通知。 */
function canDeliverWithPolicy(
  scope: NotificationDeviceScope,
  token: symbol,
) {
  return (
    activeNotificationPolicy?.attentionEnabled === true &&
    // 判断两个通知设备范围是否相同。
    activeNotificationPolicy.scope.organizationId === scope.organizationId &&
    activeNotificationPolicy.scope.userId === scope.userId &&
    activeNotificationPolicy.token === token
  )
}

/** 判断用户是否正在查看应用。 */
function isApplicationVisible() {
  if (document.visibilityState !== "visible") {
    return false
  }
  // 移动端 WebView 没有窗口焦点语义，应用在前台即视为正在查看。
  return resolveAppPlatform() === "mobile" || document.hasFocus()
}

/** 激活当前用户的新消息通知策略。 */
export function activateNotificationPolicy(
  scope: NotificationDeviceScope,
  messageNotificationsEnabled: boolean,
  workStatus: WorkStatus,
) {
  const token = Symbol("notification-policy")
  activeNotificationPolicy = {
    token,
    scope,
    attentionEnabled:
      messageNotificationsEnabled &&
      workStatus === WorkStatus.WorkStatusWorking,
  }

  return () => {
    if (activeNotificationPolicy?.token === token) {
      activeNotificationPolicy = null
    }
  }
}

/** 停止当前用户的新消息通知策略。 */
export function deactivateNotificationPolicy() {
  activeNotificationPolicy = null
}

/** 按到达顺序处理一条新消息通知。 */
export function notifyNewMessage(options: NewMessageNotificationOptions) {
  const policy = activeNotificationPolicy
  if (
    !policy ||
    !canDeliverWithPolicy(options.scope, policy.token) ||
    isApplicationVisible()
  ) {
    return Promise.resolve(false)
  }

  const delivery = messageNotificationQueue.then(async () => {
    if (
      !canDeliverWithPolicy(options.scope, policy.token) ||
      isApplicationVisible()
    ) {
      return false
    }

    const permission = await checkNotificationPermission()
    if (
      !canSendNotification(permission) ||
      !canDeliverWithPolicy(options.scope, policy.token) ||
      isApplicationVisible()
    ) {
      return false
    }

    const { soundEnabled } = readNotificationDevicePreferences(options.scope)
    await deliverMessageNotification({
      id: options.id,
      title: options.title,
      body: options.body,
      soundEnabled,
      path: options.path,
    })
    return true
  })
  messageNotificationQueue = delivery.then(
    () => undefined,
    (error) => {
      console.warn("处理新消息通知失败", {
        notification_id: options.id,
        error,
      })
    },
  )
  return delivery
}

/** 按到达顺序处理一条其他工作区的新消息通知；入队时的登录会话代次未变、仍在登录中的工作区且 attentionEnabled 在排队后与权限检查前后都为真时投递，应用在前台时同样投递。 */
export function notifyOtherWorkspaceMessage(
  options: NewMessageNotificationOptions,
  attentionEnabled: () => Promise<boolean>,
) {
  if (!activeNotificationPolicy) {
    return Promise.resolve(false)
  }
  // 登录、退出、换账号与切换工作区都会进入新的登录会话代次，之前入队的通知随之作废。
  const generation = currentSessionGeneration()
  /** 判断该工作区本人仍开启提醒，且读取完成后仍在入队时的登录会话内。 */
  const deliverable = async () =>
    (await attentionEnabled()) && currentSessionGeneration() === generation && activeNotificationPolicy !== null
  const delivery = messageNotificationQueue.then(async () => {
    if (!(await deliverable())) {
      return false
    }
    const permission = await checkNotificationPermission()
    if (!canSendNotification(permission) || !(await deliverable())) {
      return false
    }
    const { soundEnabled } = readNotificationDevicePreferences(options.scope)
    await deliverMessageNotification({
      id: options.id,
      title: options.title,
      body: options.body,
      soundEnabled,
      path: options.path,
    })
    return true
  })
  messageNotificationQueue = delivery.then(
    () => undefined,
    (error) => {
      console.warn("处理其他工作区的新消息通知失败", {
        notification_id: options.id,
        error,
      })
    },
  )
  return delivery
}
