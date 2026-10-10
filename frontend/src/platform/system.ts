/** 系统能力：打开外部链接、同步界面语言、通知权限、消息通知与未读提示，启动时按平台选定 Web 或原生端实现。 */
import { openNotificationPath } from "@/lib/notification-open-queue"
import { isDesktopMacOS, selectPlatformImplementation } from "@/platform/app-platform"
import {
  NotificationPermissionStatus,
  checkNotificationPermission as checkNativeNotificationPermission,
  requestNotificationPermission as requestNativeNotificationPermission,
  sendNativeMessageNotification,
  setNativeLocale,
  updateUnreadIndicator as updateNativeUnreadIndicator,
  type MessageNotificationInput,
  type UnreadIndicatorState,
} from "@/platform/native"

const macOSNotificationSettingsURL = "x-apple.systempreferences:com.apple.preference.notifications"

/** 当前端的通知权限状态。 */
export type NotificationPermissionState = Exclude<NotificationPermissionStatus, NotificationPermissionStatus.$zero>

/** 各平台提供的系统能力。 */
type SystemCapabilities = {
  /** 在系统浏览器中打开外部 URL。 */
  openExternalURL: (url: string) => Promise<void>
  /** 在系统浏览器中打开异步取得的外部 URL。 */
  openResolvedExternalURL: (resolveURL: () => Promise<string>) => Promise<void>
  /** 把账号语言同步到托盘、应用菜单与本机能力的错误文案。 */
  syncLocale: (locale: string) => void
  /** 返回当前端的通知权限状态。 */
  checkNotificationPermission: () => Promise<NotificationPermissionState>
  /** 在用户操作中申请当前端的通知权限。 */
  requestNotificationPermission: () => Promise<NotificationPermissionState>
  /** 投递一条新消息系统通知。 */
  deliverMessageNotification: (input: MessageNotificationInput) => Promise<void>
  /** 更新托盘与应用图标的未读提示。 */
  updateUnreadIndicator: (state: UnreadIndicatorState) => Promise<void>
  /** 打开系统通知设置，当前系统不支持时返回 false。 */
  openNotificationSettings: () => Promise<boolean>
}

/** 把浏览器通知权限转换为通知权限状态。 */
function browserPermissionState(permission: NotificationPermission): NotificationPermissionState {
  if (permission === "default") return NotificationPermissionStatus.NotificationPermissionStatusPrompt
  return permission === "granted"
    ? NotificationPermissionStatus.NotificationPermissionStatusGranted
    : NotificationPermissionStatus.NotificationPermissionStatusDenied
}

/** 判断浏览器是否支持系统通知。 */
function browserNotificationsSupported() {
  return "Notification" in window && window.isSecureContext
}

/** Web 端经浏览器打开链接与投递通知，语言与未读提示不处理。 */
const webSystem: SystemCapabilities = {
  openExternalURL: async (url) => {
    window.open(url, "_blank", "noopener,noreferrer")
  },
  // 点击时先同步打开空白窗口再跳转，取得失败时关闭该窗口。
  openResolvedExternalURL: async (resolveURL) => {
    const opened = window.open("", "_blank")
    if (opened) opened.opener = null
    try {
      const url = await resolveURL()
      if (opened) opened.location.href = url
      else window.open(url, "_blank", "noopener,noreferrer")
    } catch (error) {
      opened?.close()
      throw error
    }
  },
  syncLocale: () => {},
  checkNotificationPermission: async () =>
    browserNotificationsSupported()
      ? browserPermissionState(Notification.permission)
      : NotificationPermissionStatus.NotificationPermissionStatusUnsupported,
  requestNotificationPermission: async () =>
    browserNotificationsSupported()
      ? browserPermissionState(await Notification.requestPermission())
      : NotificationPermissionStatus.NotificationPermissionStatusUnsupported,
  deliverMessageNotification: async (input) => {
    if (!("Notification" in window) || Notification.permission !== "granted") {
      throw new Error("browser notification permission is unavailable")
    }
    const notification = new Notification(input.title, {
      body: input.body,
      silent: !input.soundEnabled,
      tag: input.id,
    })
    // 点击通知回到本页并打开通知对应的会话。
    notification.onclick = () => {
      window.focus()
      openNotificationPath(input.path)
      notification.close()
    }
  },
  updateUnreadIndicator: async () => {},
  openNotificationSettings: async () => false,
}

/** 原生端经 Wails 运行时与本机能力服务提供系统能力。 */
const nativeSystem: SystemCapabilities = {
  openExternalURL: async (url) => {
    const { Browser } = await import("@wailsio/runtime")
    await Browser.OpenURL(url)
  },
  openResolvedExternalURL: async (resolveURL) => nativeSystem.openExternalURL(await resolveURL()),
  syncLocale: (locale) => {
    setNativeLocale(locale).catch((error: unknown) => {
      console.warn("同步原生界面语言失败", error)
    })
  },
  checkNotificationPermission: async () => (await checkNativeNotificationPermission()) as NotificationPermissionState,
  requestNotificationPermission: async () => (await requestNativeNotificationPermission()) as NotificationPermissionState,
  deliverMessageNotification: (input) => sendNativeMessageNotification(input),
  updateUnreadIndicator: (state) => updateNativeUnreadIndicator(state),
  // 只有 macOS 提供可直接打开的通知设置页。
  openNotificationSettings: async () => {
    if (!isDesktopMacOS()) return false
    await nativeSystem.openExternalURL(macOSNotificationSettingsURL)
    return true
  },
}

// 当前平台的系统能力。
export const {
  openExternalURL,
  openResolvedExternalURL,
  syncLocale,
  checkNotificationPermission,
  requestNotificationPermission,
  deliverMessageNotification,
  updateUnreadIndicator,
  openNotificationSettings,
} = selectPlatformImplementation({ web: webSystem, native: nativeSystem })
