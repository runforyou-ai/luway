/** 设备通知权限、通知点击与未读提醒调用。 */
import { Events } from "@wailsio/runtime"

import {
  CheckNotificationPermission,
  RequestNotificationPermission,
  SendMessageNotification,
  TakeOpenedNotificationPath,
  UpdateUnreadIndicator,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import { bind } from "@/api/client"

/** 读取当前设备的通知权限状态。 */
export const checkNotificationPermission = bind(CheckNotificationPermission)

/** 申请当前设备的通知权限。 */
export const requestNotificationPermission = bind(
  RequestNotificationPermission,
)

/** 投递一条桌面新消息通知。 */
export const sendNativeMessageNotification = bind(SendMessageNotification)

// 与 internal/appservice/types.go 中的 NotificationOpenedEventName 保持一致。
const notificationOpenedEventName = "app:notification:opened"

/** 读取并清除原生端最近一次被点击的通知要打开的页面地址。 */
export const takeOpenedNotificationPath = bind(TakeOpenedNotificationPath)

/** 订阅原生端通知被点击，返回取消订阅函数。 */
export function onNotificationOpened(listener: () => void) {
  return Events.On(notificationOpenedEventName, () => listener())
}

/** 同步当前设备的未读数和托盘提醒状态。 */
export const updateUnreadIndicator = bind(UpdateUnreadIndicator)
