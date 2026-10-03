/** 登录页，平台开放注册时提供注册入口。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate } from "react-router"

import { loadInstallationStatus } from "@/api"
import { EntryLayout } from "@/components/entry-layout"
import { PageLoading } from "@/components/page-loading"
import { LoginForm } from "@/features/auth/login-form"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAccountSession } from "@/hooks/use-account-session"
import { useResource } from "@/hooks/use-resource"
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
  const installation = useResource(resourceKeys.installationStatus(), (signal) => loadInstallationStatus(signal), {
    staleTime: 0,
  })
  const registrationOpen = Boolean(installation.data?.registrationOpen)
  // 原生端展示已连接服务器的部署名称，未配置时展示服务器地址。
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
    <EntryLayout
      title={t("title")}
      description={
        allowServerChange ? (
          <span className="flex min-w-0 items-center gap-1.5">
            {deploymentLabel ? <span className="min-w-0 truncate">{deploymentLabel}</span> : null}
            {deploymentLabel ? <span aria-hidden="true">·</span> : null}
            <button
              type="button"
              className="shrink-0 font-medium text-foreground/80 underline-offset-4 hover:text-foreground hover:underline"
              onClick={() => navigate("/connect")}
            >
              {t("changeServer")}
            </button>
          </span>
        ) : (
          t("description")
        )
      }
      footer={
        registrationOpen ? (
          <>
            {t("registerPrompt")}{" "}
            <Link to="/register" className="font-medium text-foreground underline-offset-4 hover:underline">
              {t("registerLink")}
            </Link>
          </>
        ) : null
      }
    >
      {sessionExpired ? (
        <p role="status" className="mb-4 text-sm text-warning">
          {t("sessionExpired")}
        </p>
      ) : null}
      <LoginForm />
    </EntryLayout>
  )
}
