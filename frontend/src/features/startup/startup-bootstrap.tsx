/** 在业务路由挂载前完成统一启动检测。 */
import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Navigate, useLocation } from "react-router"

import { DeploymentMode, SessionState, type Startup } from "@/api"
import { PageLoading } from "@/components/page-loading"
import { PageLoadError } from "@/components/page-load-error"
import { StartupProvider } from "@/contexts/startup-context"
import { markStartupReady, useStartupLoader } from "@/features/startup/use-startup-loader"

/** 根据启动状态选择连接、初始化或当前应用入口；已就绪时仍可停在连接页切换服务器。 */
function resolveStartupPath(startup: Startup, pathname: string) {
  if (startup.state === SessionState.SessionStateSetup) return "/setup"
  if (startup.state === SessionState.SessionStateConnect) return "/connect"
  if (startup.state === SessionState.SessionStateReady) {
    return pathname === "/setup" ? "/" : pathname
  }
  return null
}

/** 启动检测完成前阻止业务页面挂载。 */
export function StartupBootstrap({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation("common")
  const location = useLocation()
  const { status, startup, retry } = useStartupLoader()
  const [completed, setCompleted] = useState(false)
  const completeStartup = useCallback(() => {
    markStartupReady()
    setCompleted(true)
  }, [])

  const content = (
    <StartupProvider
      usesOfficialLogin={startup?.deploymentMode === DeploymentMode.DeploymentModeManaged}
      connected={startup?.state === SessionState.SessionStateReady}
      connectReason={startup?.state === SessionState.SessionStateConnect && startup.connectReason ? startup.connectReason : null}
      completeStartup={completeStartup}
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
