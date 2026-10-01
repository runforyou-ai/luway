/** 登录页，部署开放注册时提供注册入口。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate } from "react-router"

import { loadInstallationStatus } from "@/api"
import { PageLoading } from "@/components/page-loading"
import { LoginForm } from "@/features/auth/login-form"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAccountSession } from "@/hooks/use-account-session"
import { useResource } from "@/hooks/use-resource"
import { useBrandName } from "@/lib/brand"
import { clearSessionExpiredNotice, sessionExpiredNoticePending } from "@/lib/login-return"
import { resolveServerURL } from "@/lib/server-url"

/** 已登录时进入工作区入口，否则展示登录方式。 */
export function LoginPage({
  allowServerChange = false,
}: {
  allowServerChange?: boolean
}) {
  const { t } = useTranslation("auth")
  const navigate = useNavigate()
  const productName = useBrandName()
  const installation = useResource(resourceKeys.installationStatus(), (signal) => loadInstallationStatus(signal), {
    staleTime: 0,
  })
  const registrationOpen = Boolean(installation.data?.registrationOpen)
  // 原生端展示已连接的部署，未配置部署名称时展示服务器地址。
  const serverURL = useResource(resourceKeys.serverURL(), () => resolveServerURL(), { enabled: allowServerChange })
  const deploymentLabel = installation.data?.deploymentName || (serverURL.data ? new URL(serverURL.data).host : "")
  const { account, error, redirectPath } = useAccountSession()
  // 登录过期后进入登录页时提示原因，提示只显示一次。
  const [sessionExpired] = useState(sessionExpiredNoticePending)
  useEffect(() => {
    if (sessionExpired) clearSessionExpiredNotice()
  }, [sessionExpired])

  if (account) return <Navigate to="/" replace />
  if (redirectPath) return <Navigate to={redirectPath} replace />
  if (!error) {
    return (
      <PageLoading />
    )
  }
  return (
    <main className="flex min-h-dvh w-full items-center justify-center px-6 pt-[max(1.5rem,env(safe-area-inset-top))] pb-[max(1.5rem,env(safe-area-inset-bottom))] md:p-10">
      <div className="w-full max-w-sm">
        <div className="mb-8 w-full">
          <p className="text-center text-xl font-medium tracking-tight">
            {productName}
            {allowServerChange ? (
              <button
                type="button"
                className="ml-2.5 inline-block whitespace-nowrap align-bottom text-xs font-medium tracking-[0.16em] text-muted-foreground transition-colors hover:text-foreground"
                onClick={() => navigate("/connect")}
              >
                {t("changeServer")}
              </button>
            ) : null}
          </p>
          {allowServerChange && deploymentLabel ? (
            <p className="mt-1.5 truncate text-center text-sm text-muted-foreground">{deploymentLabel}</p>
          ) : null}
        </div>
        {sessionExpired ? (
          <p role="status" className="mb-4 text-center text-sm text-warning">
            {t("sessionExpired")}
          </p>
        ) : null}
        <LoginForm />
        {registrationOpen ? (
          <p className="mt-6 text-center text-sm text-muted-foreground">
            {t("registerPrompt")}{" "}
            <Link to="/register" className="font-medium text-foreground underline-offset-4 hover:underline">
              {t("registerLink")}
            </Link>
          </p>
        ) : null}
      </div>
    </main>
  )
}
