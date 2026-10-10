/** 读取登录会话代次，供登录外壳按代次重新挂载。 */
import { useSyncExternalStore } from "react"

import {
  currentSessionGeneration,
  subscribeSessionGeneration,
} from "@/api/session-scope"

/** 返回当前登录会话代次，代次变化时重新渲染。 */
export function useSessionGeneration() {
  return useSyncExternalStore(subscribeSessionGeneration, currentSessionGeneration)
}
