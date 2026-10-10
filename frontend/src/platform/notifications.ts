/** 各端通知的本机偏好、按日申请权限与按序更新未读提示。 */
import {
  NotificationPermissionStatus,
  type UnreadIndicatorState,
} from "@/platform/native"
import {
  requestNotificationPermission,
  updateUnreadIndicator,
  type NotificationPermissionState,
} from "@/platform/system"

const notificationPreferencesStoragePrefix = "app.notifications"
let unreadIndicatorQueue: Promise<void> = Promise.resolve()

export type NotificationDeviceScope = {
  workspaceId: string
  userId: string
}

type NotificationDevicePreferences = {
  soundEnabled: boolean
  permissionMenuClickedOn: string
  permissionAutoRequested: boolean
}

const defaultNotificationDevicePreferences: NotificationDevicePreferences = {
  soundEnabled: true,
  permissionMenuClickedOn: "",
  permissionAutoRequested: false,
}

/** 返回当前企业用户的通知偏好存储键。 */
function notificationPreferencesStorageKey(scope: NotificationDeviceScope) {
  return `${notificationPreferencesStoragePrefix}:${scope.workspaceId}:${scope.userId}`
}

/** 读取当前企业用户的本机通知偏好。 */
export function readNotificationDevicePreferences(
  scope: NotificationDeviceScope,
): NotificationDevicePreferences {
  const storageKey = notificationPreferencesStorageKey(scope)
  try {
    const stored = window.localStorage.getItem(storageKey)
    if (!stored) {
      return { ...defaultNotificationDevicePreferences }
    }

    const parsed = JSON.parse(stored) as NotificationDevicePreferences
    if (
      typeof parsed.soundEnabled !== "boolean" ||
      typeof parsed.permissionMenuClickedOn !== "string"
    ) {
      throw new Error("invalid notification preferences")
    }
    return {
      soundEnabled: parsed.soundEnabled,
      permissionMenuClickedOn: parsed.permissionMenuClickedOn,
      permissionAutoRequested: parsed.permissionAutoRequested === true,
    }
  } catch (error) {
    console.warn("读取本机通知偏好失败", { storage_key: storageKey, error })
    return { ...defaultNotificationDevicePreferences }
  }
}

/** 保存当前企业用户的本机通知偏好。 */
function writeNotificationDevicePreferences(
  scope: NotificationDeviceScope,
  preferences: NotificationDevicePreferences,
) {
  const storageKey = notificationPreferencesStorageKey(scope)
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(preferences))
  } catch (error) {
    console.warn("保存本机通知偏好失败", { storage_key: storageKey, error })
  }
}

/** 保存当前企业用户在本设备上的通知声音开关。 */
export function setNotificationSoundEnabled(
  scope: NotificationDeviceScope,
  soundEnabled: boolean,
) {
  writeNotificationDevicePreferences(scope, {
    ...readNotificationDevicePreferences(scope),
    soundEnabled,
  })
}

/** 记录当前设备已经为该用户自动申请过通知权限。 */
export function markNotificationPermissionRequested(
  scope: NotificationDeviceScope,
) {
  writeNotificationDevicePreferences(scope, {
    ...readNotificationDevicePreferences(scope),
    permissionAutoRequested: true,
  })
}

/** 点击消息菜单后每天最多申请一次通知权限。 */
export function requestNotificationPermissionFromMessageMenu(
  scope: NotificationDeviceScope,
) {
  const preferences = readNotificationDevicePreferences(scope)
  // 返回当前设备的本地日期。
  const now = new Date()
  const year = now.getFullYear()
  const month = String(now.getMonth() + 1).padStart(2, "0")
  const day = String(now.getDate()).padStart(2, "0")
  const today = `${year}-${month}-${day}`
  if (preferences.permissionMenuClickedOn === today) {
    return Promise.resolve(null)
  }

  writeNotificationDevicePreferences(scope, {
    ...preferences,
    permissionMenuClickedOn: today,
  })
  return requestNotificationPermission()
}

/** 判断当前权限状态是否允许发送通知。 */
export function canSendNotification(state: NotificationPermissionState) {
  return (
    state === NotificationPermissionStatus.NotificationPermissionStatusGranted
  )
}

/** 按调用顺序更新桌面端未读提示。 */
export function updateNotificationUnreadIndicator(
  state: UnreadIndicatorState,
) {
  const update = unreadIndicatorQueue.then(() => updateUnreadIndicator(state))
  unreadIndicatorQueue = update.catch(() => undefined)
  return update
}
