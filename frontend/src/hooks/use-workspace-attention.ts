/** 各工作区的提醒数量：工作区外壳与工作区选择页保持账号实时连接，其他工作区有变化时刷新数量；切换器、个人中心、选择页与应用角标共用同一份缓存。 */
import { useEffect, useMemo, useRef } from "react"
import { useNavigate } from "react-router"
import { recoverSession } from "@/lib/session-navigation"

import { listWorkspaceAttention, workspaceActivityClient, type WorkspaceAttention } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

// 其他工作区的变化成批到达，合并在这段时间内再刷新数量。
const refreshDelayMs = 1_000

// 页面可见时按这个周期重读数量，补上实时通道短暂中断期间丢失的事件。
const fallbackRefreshMs = 60_000

/** 返回一个工作区的提醒总数，口径与应用角标一致：聊天提醒未读数加待处理会话中的未读消息数。 */
function workspaceAttentionTotal(attention: WorkspaceAttention) {
  return attention.attentionUnreadCount + attention.pendingUnreadCount
}

/** 读取各工作区的提醒数量，返回按工作区编号索引的总数、当前工作区之外的合计以及数量是否已读到。 */
export function useWorkspaceAttention(currentWorkspaceId: string) {
  const attention = useResource(resourceKeys.workspaceAttention(), (signal) => listWorkspaceAttention(signal))
  return useMemo(() => {
    const totals = new Map<string, number>()
    let others = 0
    for (const item of attention.data?.items ?? []) {
      const total = workspaceAttentionTotal(item)
      totals.set(item.workspaceId, total)
      if (item.workspaceId !== currentWorkspaceId) others += total
    }
    return { totals, others, loaded: attention.data !== undefined }
  }, [attention.data, currentWorkspaceId])
}

/** 挂载期间保持账号实时连接：其他工作区有变化、重新连接、回到前台或到达重读周期时刷新数量，重新连接时同时刷新工作区列表；账号的工作区增减后刷新数量。currentWorkspaceId 为空时全部工作区都算其他工作区。 */
export function useWorkspaceActivityConnection(currentWorkspaceId: string, workspaceIds: string[]) {
  const invalidate = useResourceInvalidator()
  const navigate = useNavigate()

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    /** 合并短时间内的多次变化后刷新数量。 */
    const refresh = () => {
      clearTimeout(timer)
      timer = setTimeout(() => void invalidate(resourceKeys.workspaceAttention()), refreshDelayMs)
    }
    const unsubscribe = workspaceActivityClient.subscribe((event) => {
      if (event.type === "session_error") { recoverSession(event.error, navigate); return }
      if (event.type === "resync") { void invalidate(resourceKeys.workspaces()); refresh(); return }
      if (event.type !== "frame") return
      // 当前工作区的数量由成员订阅维护，这里只关心其他工作区。
      if (event.frame.type === "workspace_activity" && event.frame.workspaceId !== currentWorkspaceId) {
        refresh()
      }
    })
    const resume = () => {
      if (document.visibilityState !== "visible") return
      workspaceActivityClient.resume()
      refresh()
    }
    const fallback = setInterval(() => {
      if (document.visibilityState === "visible") { workspaceActivityClient.resume(); refresh() }
    }, fallbackRefreshMs)
    window.addEventListener("online", resume)
    document.addEventListener("visibilitychange", resume)
    workspaceActivityClient.start()
    return () => {
      clearTimeout(timer)
      clearInterval(fallback)
      window.removeEventListener("online", resume)
      document.removeEventListener("visibilitychange", resume)
      unsubscribe()
      workspaceActivityClient.stop()
    }
  }, [currentWorkspaceId, invalidate, navigate])

  // 加入或离开工作区时服务端断开账号的实时连接，客户端按新的成员身份重连；这里只刷新数量。
  const membership = [...workspaceIds].sort().join(",")
  const counted = useRef(membership)
  useEffect(() => {
    if (counted.current === membership) return
    counted.current = membership
    void invalidate(resourceKeys.workspaceAttention())
  }, [membership, invalidate])
}
