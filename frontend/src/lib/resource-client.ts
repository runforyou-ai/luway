/** 全局查询缓存客户端，缓存生命周期与登录会话绑定。 */
import { QueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { advanceSessionGeneration } from "@/api/session-scope"
import { ResourceScope } from "@/hooks/resource-keys"

/**
 * 进程级查询客户端。
 * 默认单次请求，结果在页面存活期间保持新鲜。
 * 调用方通过 refresh、失效资源或按 key 指定 staleTime 控制刷新。
 */
export const resourceClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
      staleTime: Infinity,
      gcTime: 5 * 60 * 1000,
    },
    mutations: {
      networkMode: "always",
    },
  },
})

/** 切换工作区：提升会话代次让实时连接等订阅方清理，清除工作区范围的查询缓存、全部变更记录与提示；已读取完成的账号级与部署级数据保留。 */
export function beginWorkspaceBoundary() {
  advanceSessionGeneration()
  // 代次提升后在途请求的结果按过期丢弃，读取中的账号级查询一并清除。
  resourceClient.removeQueries({
    predicate: (query) => query.queryKey[0] === ResourceScope.Workspace || query.state.fetchStatus === "fetching",
  })
  // 在途变更的结果同样按过期丢弃，清除后按变更记录给出的进行中状态随之结束。
  resourceClient.getMutationCache().clear()
  toast.dismiss()
}

const sessionBoundaryListeners = new Set<() => void>()

/** 订阅进入新的登录会话，返回取消订阅函数。 */
export function subscribeSessionBoundary(listener: () => void) {
  sessionBoundaryListeners.add(listener)
  return () => {
    sessionBoundaryListeners.delete(listener)
  }
}

/** 进入新的登录会话：先提升会话代次让实时连接等订阅方清理，再清空全部查询缓存、关闭上一会话的提示并通知订阅方。 */
export function beginSessionBoundary() {
  advanceSessionGeneration()
  resourceClient.clear()
  toast.dismiss()
  for (const listener of [...sessionBoundaryListeners]) listener()
}
