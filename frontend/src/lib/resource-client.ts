/** 全局查询缓存客户端，缓存生命周期与登录会话绑定。 */
import { QueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { advanceSessionGeneration } from "@/api/session-scope"
import { accountScopedKeyPrefixes } from "@/hooks/resource-keys"

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
  },
})

/** 切换工作区：提升会话代次让实时连接等订阅方清理，清除工作区范围的查询缓存与提示；已读取完成的账号级数据保留。 */
export function beginWorkspaceBoundary() {
  advanceSessionGeneration()
  // 代次提升后在途请求的结果不再交付，读取中的账号级查询一并清除。
  resourceClient.removeQueries({
    predicate: (query) => !accountScopedKeyPrefixes.has(query.queryKey[0]) || query.state.fetchStatus === "fetching",
  })
  toast.dismiss()
}

/** 进入新的登录会话：先提升会话代次让实时连接等订阅方清理，再清空全部查询缓存并关闭上一会话的提示。 */
export function beginSessionBoundary() {
  advanceSessionGeneration()
  resourceClient.clear()
  toast.dismiss()
}
