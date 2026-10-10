/** 平台设置入口：读取登录账号，平台管理员进入平台设置页面，其他账号回到设置首页。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Navigate } from "react-router"

import { loadAccount, type Account } from "@/api"
import { PageLoadError } from "@/components/page-load-error"
import { PageLoading } from "@/components/page-loading"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 每次进入时重新读取账号，确认仍是平台管理员后以登录账号渲染页面。 */
export function PlatformAdminGate({ children }: { children: (account: Account) => ReactNode }) {
  const { t } = useTranslation("platform")
  const account = useResource(resourceKeys.account(), (signal) => loadAccount(signal), { staleTime: 0 })

  if (account.error && !account.data && !account.retrying) {
    return <PageLoadError message={t("accountLoadError")} onRetry={() => void account.refresh()} />
  }
  if (!account.data) return <PageLoading />
  if (!account.data.isPlatformAdmin) return <Navigate to="/settings" replace />
  return children(account.data)
}
