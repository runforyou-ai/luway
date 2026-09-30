/** 标记当前登录外壳已由实时事件流与兜底探针驱动资源失效。 */
import { createContext, createElement, useContext, type ReactNode } from "react"

const RealtimeSyncContext = createContext(false)

/** 在已启动同步协调器的登录外壳内声明实时同步可用。 */
export function RealtimeSyncProvider({ children }: { children: ReactNode }) {
  return createElement(RealtimeSyncContext.Provider, { value: true }, children)
}

/** 返回当前页面是否由实时同步驱动资源失效，未接入的外壳继续轮询。 */
export function useRealtimeSyncActive() {
  return useContext(RealtimeSyncContext)
}
