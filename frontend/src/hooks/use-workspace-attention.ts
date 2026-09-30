/** 各工作区的提醒数量：工作区外壳与工作区选择页保持工作区动态事件流，其他工作区有变化时刷新数量；切换器、个人中心、选择页与应用角标共用同一份缓存。 */
import { useEffect, useMemo, useRef } from "react"

import { listWorkspaceAttention, workspaceActivityClient, type WorkspaceAttention } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"

// 其他工作区的变化成批到达，合并在这段时间内再刷新数量。
const refreshDelayMs = 1_000

// 事件可能在实时通道短暂中断时丢失，页面可见时按这个周期兜底重读数量。
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

/** 挂载期间保持工作区动态事件流：其他工作区有变化、重新连接、回到前台或到达兜底周期时刷新数量；账号的工作区增减后重建事件流以订阅新工作区。currentWorkspaceId 为空时全部工作区都算其他工作区。 */
export function useWorkspaceActivityConnection(currentWorkspaceId: string, workspaceIds: string[]) {
  const invalidate = useResourceInvalidator()

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    /** 合并短时间内的多次变化后刷新数量。 */
    const refresh = () => {
      clearTimeout(timer)
      timer = setTimeout(() => void invalidate(resourceKeys.workspaceAttention()), refreshDelayMs)
    }
    const unsubscribe = workspaceActivityClient.subscribe((event) => {
      if (event.type !== "frame") return
      // 当前工作区的数量由其成员事件流维护，这里只关心其他工作区。
      if (event.frame.type === "workspace_activity" ? event.frame.workspaceId !== currentWorkspaceId : event.frame.type === "server_hello") {
        refresh()
      }
    })
    const resume = () => {
      if (document.visibilityState !== "visible") return
      workspaceActivityClient.resume()
      refresh()
    }
    const fallback = setInterval(() => {
      if (document.visibilityState === "visible") refresh()
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
  }, [currentWorkspaceId, invalidate])

  // 事件流按建立时的成员身份订阅，加入或离开工作区后重新建立；连接中或等待重连时同样重建。
  const membership = [...workspaceIds].sort().join(",")
  const subscribed = useRef(membership)
  useEffect(() => {
    if (subscribed.current === membership) return
    subscribed.current = membership
    workspaceActivityClient.stop()
    workspaceActivityClient.start()
    void invalidate(resourceKeys.workspaceAttention())
  }, [membership, invalidate])
}
