/** 平台后台门禁：确认后台 Cookie 会话有效且账号为平台管理员。 */
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Navigate } from "react-router"

import { isApiError, loadAdminAccount, SessionState, type Account } from "@/api"
import { PageLoadError } from "@/components/page-load-error"
import { PageLoading } from "@/components/page-loading"
import { adminLoginPath } from "@/features/admin/admin-paths"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 每次进入时重新读取后台账号，未登录或非管理员时进入后台登录页。 */
export function AdminGate({ children }: { children: (account: Account) => ReactNode }) {
  const { t } = useTranslation("admin")
  const account = useResource(resourceKeys.adminAccount(), (signal) => loadAdminAccount(signal), {
    staleTime: 0,
    recoverSession: false,
  })

  // 会话失效时即使还有旧缓存也回登录页，避免退出后仍渲染后台并狂打接口。
  if (isApiError(account.error) && account.error.state === SessionState.Login) {
    return <Navigate to={adminLoginPath} replace />
  }
  if (account.error && !account.data && !account.retrying) {
    return <PageLoadError message={t("accountLoadError")} onRetry={() => void account.refresh()} />
  }
  if (!account.data) return <PageLoading />
  if (!account.data.isPlatformAdmin) return <Navigate to={adminLoginPath} replace />
  return children(account.data)
}
