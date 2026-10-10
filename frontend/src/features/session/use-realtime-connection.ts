/** 登录外壳挂载期间保持成员实时事件流与同步协调器，并把会话错误恢复到对应入口。 */
import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "react-router"

import { getSyncHeads, realtimeClient } from "@/api"
import { ResourceRefresher } from "@/features/session/resource-refresher"
import { SyncCoordinator } from "@/features/session/sync-coordinator"
import { recoverSession } from "@/lib/session-navigation"

/** 身份就绪后连接成员实时事件流并在前台执行兜底校验，卸载时关闭；restartOnResume 在回到前台时重建已建立的事件流。 */
export function useRealtimeConnection(
  enabled: boolean,
  { restartOnResume = false }: { restartOnResume?: boolean } = {},
) {
  const navigate = useNavigate()
  const client = useQueryClient()

  useEffect(() => {
    if (!enabled) {
      return
    }
    const refresher = new ResourceRefresher(client)
    const coordinator = new SyncCoordinator({
      invalidate: refresher.invalidate,
      retry: refresher.retry,
      readHeads: () => { realtimeClient.resume(); return getSyncHeads() },
      failed: (error) => {
        if (!recoverSession(error, navigate)) {
          console.warn("读取同步探针失败", error)
        }
      },
    })
    const unsubscribe = realtimeClient.subscribe((event) => {
      if (event.type === "frame") {
        coordinator.receive(event.frame)
      } else if (event.type === "session_error") {
        recoverSession(event.error, navigate)
      }
    })
    // 网络恢复时唤醒尚未建立的连接，并立即做一次兜底校验。
    const resume = () => {
      if (document.visibilityState !== "visible") {
        return
      }
      realtimeClient.resume()
      void coordinator.probe()
    }
    // 页面在后台时暂停周期探针；回到前台时系统挂起期间的连接按可能失活处理，先重建事件流再恢复探针并立即校验。
    const changeVisibility = () => {
      if (document.visibilityState !== "visible") {
        coordinator.suspend()
        return
      }
      if (restartOnResume) {
        realtimeClient.restart()
      }
      realtimeClient.resume()
      coordinator.start()
    }
    window.addEventListener("online", resume)
    document.addEventListener("visibilitychange", changeVisibility)
    realtimeClient.start()
    // 在后台启动时由连接问候携带首个探针值，回到前台后开始周期探针。
    if (document.visibilityState === "visible") {
      coordinator.start()
    }
    return () => {
      window.removeEventListener("online", resume)
      document.removeEventListener("visibilitychange", changeVisibility)
      unsubscribe()
      realtimeClient.stop()
      coordinator.dispose()
    }
  }, [client, enabled, navigate, restartOnResume])
}
