/** 应用启动检测状态管理。 */
import { useCallback, useEffect, useState } from "react"

import { loadStartup, SessionState, type Startup } from "@/api"
import { applyBrand, currentBrand } from "@/lib/brand"

type StartupLoadState =
  | { status: "loading" | "failed"; startup?: never }
  | { status: "loaded"; startup: Startup }

let startupRequest: Promise<Startup> | null = null

/** 首次安装或连接服务器完成后把已缓存的启动检测结果改为就绪并换成当前品牌，切换路由器后重新挂载的启动检测不再回到入口页，也不回退到连接前的品牌。 */
export function markStartupReady() {
  startupRequest = (startupRequest ?? loadStartup()).then((startup) => ({
    ...startup,
    state: SessionState.SessionStateReady,
    brand: currentBrand(),
  }))
}

/** 启动时检测当前平台能否进入应用，检测失败后由 retry 重新检测。 */
export function useStartupLoader() {
  const [state, setState] = useState<StartupLoadState>({
    status: "loading",
  })
  const [attempt, setAttempt] = useState(0)
  const retry = useCallback(() => {
    setState({ status: "loading" })
    setAttempt((current) => current + 1)
  }, [])

  useEffect(() => {
    let stale = false
    // 复用当前启动检测请求。
    startupRequest ??= loadStartup().catch((error: unknown) => {
      startupRequest = null
      throw error
    })
    void startupRequest.then(
      (startup) => {
        applyBrand(startup.brand)
        if (!stale) setState({ status: "loaded", startup })
      },
      (error: unknown) => {
        if (stale) return
        console.warn("启动检测失败，停止加载后续页面", error)
        setState({ status: "failed" })
      },
    )
    return () => {
      stale = true
    }
  }, [attempt])

  return { ...state, retry }
}
