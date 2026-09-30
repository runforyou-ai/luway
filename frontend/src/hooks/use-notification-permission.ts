/** 在 React 页面中同步当前设备的通知权限状态。 */
import { useEffect, useRef, useState } from "react"

import { NotificationPermissionStatus } from "@/api"
import {
  checkNotificationPermission,
  requestNotificationPermission,
  type NotificationPermissionState,
} from "@/platform/notifications"

type NotificationPermissionViewState =
  | NotificationPermissionState
  | "checking"

/** 读取、刷新并申请当前设备的通知权限。 */
export function useNotificationPermission() {
  const [status, setStatus] =
    useState<NotificationPermissionViewState>("checking")
  const [requesting, setRequesting] = useState(false)
  const mountedRef = useRef(false)

  useEffect(() => {
    let active = true
    mountedRef.current = true

    /** 刷新当前设备的通知权限。 */
    async function refreshPermission() {
      try {
        const nextStatus = await checkNotificationPermission()
        if (active) {
          setStatus(nextStatus)
        }
      } catch (error) {
        if (active) {
          console.warn("读取通知权限失败", error)
          setStatus(
            NotificationPermissionStatus.NotificationPermissionStatusUnsupported,
          )
        }
      }
    }

    setStatus("checking")
    setRequesting(false)
    void refreshPermission()
    window.addEventListener("focus", refreshPermission)
    // 移动端回到前台不触发窗口焦点事件，按页面可见性再刷新一次。
    document.addEventListener("visibilitychange", refreshPermission)
    return () => {
      active = false
      mountedRef.current = false
      window.removeEventListener("focus", refreshPermission)
      document.removeEventListener("visibilitychange", refreshPermission)
    }
  }, [])

  /** 申请通知权限并同步状态。 */
  async function requestPermission() {
    setRequesting(true)
    try {
      const nextStatus = await requestNotificationPermission()
      if (!mountedRef.current) {
        return null
      }
      setStatus(nextStatus)
      return nextStatus
    } catch (error) {
      if (!mountedRef.current) {
        return null
      }
      throw error
    } finally {
      if (mountedRef.current) {
        setRequesting(false)
      }
    }
  }

  return { status, requesting, requestPermission }
}
