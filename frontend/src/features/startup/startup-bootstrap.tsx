/** 在业务路由挂载前完成统一启动检测。 */
import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Navigate, useLocation } from "react-router"

import { SessionState, type Startup } from "@/api"
import { PageLoading } from "@/components/page-loading"
import { PageLoadError } from "@/components/page-load-error"
import { StartupProvider } from "@/contexts/startup-context"
import { markStartupReady, markStartupUpgrade, useStartupLoader } from "@/features/startup/use-startup-loader"

/** 根据启动状态选择连接、初始化、客户端升级或当前应用入口；已就绪或需要升级时仍可停在连接页切换服务器。 */
function resolveStartupPath(startup: Startup, pathname: string) {
  if (startup.state === SessionState.SessionStateSetup) return "/setup"
  if (startup.state === SessionState.SessionStateConnect) return "/connect"
  if (startup.state === SessionState.SessionStateUpgrade) {
    return pathname === "/connect" ? pathname : "/upgrade"
  }
  if (startup.state === SessionState.SessionStateReady) {
    return pathname === "/setup" ? "/" : pathname
  }
  return null
}

/** 启动检测完成前阻止业务页面挂载。 */
export function StartupBootstrap({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation("common")
  const location = useLocation()
  const { status, startup, retry, reload } = useStartupLoader()
  const [completed, setCompleted] = useState(false)
  // 已就绪后使用中被要求升级客户端。
  const [upgradeRequired, setUpgradeRequired] = useState(false)
  const completeStartup = useCallback(() => {
    markStartupReady()
    setUpgradeRequired(false)
    setCompleted(true)
  }, [])
  const restartStartup = useCallback(() => {
    setUpgradeRequired(false)
    setCompleted(false)
    reload()
  }, [reload])

  // 会话恢复进入升级页时启动状态改为需要升级。
  useEffect(() => {
    if (location.pathname === "/upgrade" && startup?.state === SessionState.SessionStateReady && !upgradeRequired) {
      markStartupUpgrade()
      setUpgradeRequired(true)
    }
  }, [location.pathname, startup, upgradeRequired])

  const content = (
    <StartupProvider
      connected={startup?.state === SessionState.SessionStateReady && !upgradeRequired}
      connectReason={startup?.state === SessionState.SessionStateConnect && startup.connectReason ? startup.connectReason : null}
      completeStartup={completeStartup}
      restartStartup={restartStartup}
    >
      {children}
    </StartupProvider>
  )

  useEffect(() => {
    if (
      startup?.state === SessionState.SessionStateReady &&
      resolveStartupPath(startup, location.pathname) === location.pathname
    ) {
      setCompleted(true)
    }
  }, [location.pathname, startup])

  if (status === "failed") {
    return <PageLoadError message={t("errors.network")} onRetry={retry} />
  }
  if (status !== "loaded") {
    return <PageLoading />
  }
  if (completed) return content
  const targetPath = resolveStartupPath(startup, location.pathname)
  if (!targetPath) return <PageLoading />
  return targetPath === location.pathname ? (
    content
  ) : (
    <Navigate to={targetPath} replace />
  )
}
