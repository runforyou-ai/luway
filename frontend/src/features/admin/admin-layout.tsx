/** 部署管理外壳：校验部署管理员身份，渲染一级导航和部署管理页面。 */
import type { CSSProperties } from "react"
import { useTranslation } from "react-i18next"
import { Navigate, Route, Routes } from "react-router"

import { loadAccount, type Account } from "@/api"
import { PageLoadError } from "@/components/page-load-error"
import { PageLoading } from "@/components/page-loading"
import { useWorkspaceRail, WorkspaceRailResizer } from "@/components/workspace-rail"
import { UserPreferencesProvider } from "@/contexts/user-preferences"
import { AdminAccountListPage } from "@/features/admin/admin-account-list-page"
import { AdminNavigation } from "@/features/admin/admin-navigation"
import { AdminOverviewPage } from "@/features/admin/admin-overview-page"
import { AdminRegistrationPage } from "@/features/admin/admin-registration-page"
import { AdminWorkspaceListPage } from "@/features/admin/admin-workspace-list-page"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { useIsNarrowViewport } from "@/hooks/use-narrow-viewport"
import { resolveAppPlatform } from "@/platform/app-platform"
import { useDesktopWindowMode } from "@/platform/desktop-window"

/** 读取登录账号，部署管理员进入部署管理外壳，其他账号和移动端回到工作区选择页。 */
export function AdminLayout() {
  const { t } = useTranslation("admin")
  const mobile = resolveAppPlatform() === "mobile"
  const account = useResource(resourceKeys.account(), (signal) => loadAccount(signal), { enabled: !mobile })
  if (mobile) return <Navigate to="/workspaces" replace />

  if (account.error && !account.retrying) {
    return <PageLoadError message={t("overview.loadError")} onRetry={() => void account.refresh()} />
  }
  if (!account.data) return <PageLoading />
  if (!account.data.isDeploymentAdmin) return <Navigate to="/workspaces" replace />
  return (
    <UserPreferencesProvider user={account.data}>
      <AdminShell account={account.data} />
    </UserPreferencesProvider>
  )
}

/** 按工作台布局渲染部署管理导航和当前管理页面；窄视口下导航固定为窄栏。 */
function AdminShell({ account }: { account: Account }) {
  useDesktopWindowMode("workspace")
  const rail = useWorkspaceRail()
  const narrow = useIsNarrowViewport()
  const collapsed = narrow || rail.collapsed

  return (
    <div
      className="app-workspace-shell relative flex h-svh min-h-0 w-full overflow-hidden"
      data-rail-collapsed={collapsed ? "true" : undefined}
      style={collapsed ? undefined : ({ "--app-workspace-rail-width": `${rail.width}px` } as CSSProperties)}
    >
      <AdminNavigation collapsed={collapsed} onToggleRail={narrow ? undefined : rail.toggleCollapsed} />
      {collapsed ? null : <WorkspaceRailResizer onWidthChange={rail.changeWidth} />}
      <div aria-hidden="true" className="app-workspace-top-drag-region" />
      <main className="app-workspace-content-frame relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-xl bg-background shadow-sm">
        <Routes>
          <Route index element={<Navigate to="overview" replace />} />
          <Route path="overview" element={<AdminOverviewPage />} />
          <Route path="accounts" element={<AdminAccountListPage account={account} />} />
          <Route path="workspaces" element={<AdminWorkspaceListPage />} />
          <Route path="registration" element={<AdminRegistrationPage />} />
          <Route path="*" element={<Navigate to="overview" replace />} />
        </Routes>
      </main>
    </div>
  )
}
