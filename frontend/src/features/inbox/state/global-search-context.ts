/** 提供全局搜索入口。 */
import { createContext, useContext } from "react"

type GlobalSearchContextValue = {
  open: (conversationId?: string) => void
}

export const GlobalSearchContext = createContext<GlobalSearchContextValue | null>(null)

/** 返回全局搜索入口，未挂载搜索模态时返回空。 */
export function useGlobalSearch() {
  return useContext(GlobalSearchContext)
}
