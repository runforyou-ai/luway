/** 登录页，平台开放注册时提供注册入口。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate } from "react-router"

import { loadInstallationStatus } from "@/api"
import { serverURL } from "@/api/client"
import { EntryLayout } from "@/components/entry-layout"
import { PageLoading } from "@/components/page-loading"
import { LoginForm } from "@/features/auth/login-form"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAccountSession } from "@/hooks/use-account-session"
import { useResource } from "@/hooks/use-resource"
import { useBrandName } from "@/lib/brand"
import { clearSessionExpiredNotice, sessionExpiredNoticePending } from "@/lib/login-return"

/** 已登录时进入工作区入口，否则展示登录方式。 */
export function LoginPage({
  allowServerChange = false,
}: {
  allowServerChange?: boolean
}) {
  const { t } = useTranslation("auth")
  const productName = useBrandName()
  const navigate = useNavigate()
  const installation = useResource(resourceKeys.installationStatus(), (signal) => loadInstallationStatus(signal), {
    staleTime: 0,
  })
  const registrationOpen = Boolean(installation.data?.registrationOpen)
  // 原生端在产品名称上提供完整服务器地址的悬停提示。
  const connectedServer = serverURL()
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
    <EntryLayout
      title={t("title")}
      description={allowServerChange ? undefined : t("description")}
      footer={
        registrationOpen || allowServerChange ? (
          <div className="space-y-3">
            {registrationOpen ? (
              <p>
                {t("registerPrompt")}{" "}
                <Link to="/register" className="font-medium text-foreground underline-offset-4 hover:underline">
                  {t("registerLink")}
                </Link>
              </p>
            ) : null}
            {allowServerChange ? (
              <div className="flex min-w-0 items-center justify-center gap-1.5 text-xs text-muted-foreground">
                <span className="min-w-0 truncate" title={connectedServer}>{productName}</span>
                <span aria-hidden="true">·</span>
                <button
                  type="button"
                  className="shrink-0 underline-offset-4 transition-colors hover:text-foreground hover:underline focus-visible:text-foreground focus-visible:underline"
                  onClick={() => navigate("/connect")}
                >
                  {t("changeServer")}
                </button>
              </div>
            ) : null}
          </div>
        ) : null
      }
    >
      {sessionExpired ? (
        <p role="status" className="mb-4 text-sm/6 text-muted-foreground">
          {t("sessionExpired")}
        </p>
      ) : null}
      <LoginForm />
    </EntryLayout>
  )
}
