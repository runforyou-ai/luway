/** 提供服务器连接状态和启动完成入口。 */
import { createContext, useContext, useMemo } from "react"

import type { ConnectReason } from "@/api"

type StartupContextValue = {
  // 启动时已连接到可用的服务器。
  connected: boolean
  // 原生端已保存服务器仍进入连接页的原因，未保存服务器或已连接时为空。
  connectReason: ConnectReason | null
  completeStartup: () => void
  // 丢弃启动检测结果并重新检测，按新结果进入对应入口。
  restartStartup: () => void
}

const StartupContext = createContext<StartupContextValue | null>(null)

/** 向启动流程内的页面提供启动状态。 */
export function StartupProvider({
  connected,
  connectReason,
  completeStartup,
  restartStartup,
  children,
}: StartupContextValue & { children: React.ReactNode }) {
  const value = useMemo(
    () => ({ connected, connectReason, completeStartup, restartStartup }),
    [connected, connectReason, completeStartup, restartStartup],
  )
  return (
    <StartupContext.Provider value={value}>
      {children}
    </StartupContext.Provider>
  )
}

/** 返回启动流程状态。 */
export function useStartup() {
  const context = useContext(StartupContext)
  if (!context) throw new Error("useStartup 必须在 StartupProvider 内使用")
  return context
}
