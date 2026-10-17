/** 根路径下的账号级路由与工作区路由器内的转交路由。 */
import { useEffect, type ReactNode } from "react"
import { Navigate, Route, Routes, useLocation, useSearchParams } from "react-router"

import { appExtensions } from "@/app-extensions"
import { ChannelBindingPage } from "@/features/account/channel-binding-page"
import { InvitationPage } from "@/features/account/invitation-page"
import { RegisterPage } from "@/features/account/register-page"
import { WorkspaceCreatePage } from "@/features/account/workspace-create-page"
import { WorkspaceEntry } from "@/features/account/workspace-entry"
import { WorkspaceListPage } from "@/features/account/workspace-list-page"
import { AdminGate } from "@/features/admin/admin-gate"
import { AdminLoginPage } from "@/features/admin/admin-login-page"
import {
  adminDeploymentPath,
  adminLicensePath,
  adminLoginPath,
  adminOverviewPath,
  adminOverviewSectionPath,
  adminOverviewSections,
  adminRootPath,
  adminSystemPath,
  adminSystemSectionPath,
  adminSystemSections,
  adminAccountsPath,
  adminDashboardPath,
  adminOverviewScalePath,
  adminWorkspacesPath,
  type AdminOverviewSection,
  type AdminSystemSection,
} from "@/features/admin/admin-paths"
import { AdminShell, useAdminOutlet } from "@/features/admin/admin-shell"
import { LoginPage } from "@/features/auth/login-page"
import { SetupPage } from "@/features/installation/setup-page"
import { ClientUpgradePage, ServerConnectionPage } from "@/features/server-connection/public"
import { WorkspaceGate } from "@/apps/shell/chrome/workspace-gate"
import { lazyNamed } from "@/lib/lazy-named"
import { navigateToHashPath } from "@/lib/workspace-route"
import type { AppPlatform } from "@/platform/app-platform"

const PlatformOverviewPage = lazyNamed(() => import("@/features/platform/public"), "PlatformOverviewPage")
const PlatformDashboardPage = lazyNamed(() => import("@/features/platform/public"), "PlatformDashboardPage")
const PlatformWorkspacesPage = lazyNamed(() => import("@/features/platform/public"), "PlatformWorkspacesPage")
const PlatformAccountsPage = lazyNamed(() => import("@/features/platform/public"), "PlatformAccountsPage")
const PlatformSitePage = lazyNamed(() => import("@/features/platform/public"), "PlatformSitePage")
const PlatformDeploymentPage = lazyNamed(() => import("@/features/platform/public"), "PlatformDeploymentPage")
const PlatformLicensePage = lazyNamed(() => import("@/features/platform/public"), "PlatformLicensePage")

/** 把 /admin 下的绝对路径转为相对后台路由的路径。 */
function relativeAdminPath(path: string) {
  return path.startsWith(`${adminRootPath}/`) ? path.slice(adminRootPath.length + 1) : path
}

function AdminWorkspacesPage() {
  const { account } = useAdminOutlet()
  return <PlatformWorkspacesPage account={account} />
}

function AdminAccountsPage() {
  const { account } = useAdminOutlet()
  return <PlatformAccountsPage account={account} />
}

/** 去掉 tab 后保留其余查询参数。 */
function pathWithPreservedQuery(path: string, params: URLSearchParams) {
  const next = new URLSearchParams(params)
  next.delete("tab")
  const query = next.toString()
  return query ? `${path}?${query}` : path
}

/** 旧概览 ?tab= 地址映射到监控一级菜单；平台规模与缺省入口改到仪表板。 */
function LegacyOverviewRedirect() {
  const [params] = useSearchParams()
  const tab = params.get("tab")
  if (!tab || tab === "scale") {
    return <Navigate to={pathWithPreservedQuery(adminDashboardPath, params)} replace />
  }
  const section = adminOverviewSections.includes(tab as AdminOverviewSection) ? (tab as AdminOverviewSection) : "usage"
  return <Navigate to={pathWithPreservedQuery(adminOverviewSectionPath(section), params)} replace />
}

/** 旧系统设置 ?tab= 地址：集成页保留原入口，其余并入站点设置。 */
function LegacyDeploymentRedirect() {
  const [params] = useSearchParams()
  const tab = params.get("tab")
  if (adminSystemSections.includes(tab as AdminSystemSection)) {
    return <Navigate to={pathWithPreservedQuery(adminSystemSectionPath(tab as AdminSystemSection), params)} replace />
  }
  return <Navigate to={pathWithPreservedQuery(adminSystemPath, params)} replace />
}

// 账号级页面的路径，工作区路由器内出现这些地址时转交根路由器。
const accountPaths = [
  "/login",
  "/register",
  "/setup",
  "/connect",
  "/upgrade",
  "/workspaces",
  "/workspaces/new",
  "/invitations/:token",
  "/channel-bindings/:token",
  "/admin",
  "/admin/*",
]

/** 渲染登录、注册、首次安装、平台后台、服务器连接、客户端升级、工作区列表、创建工作区、邀请页和渠道绑定页；其余地址进入最近使用的工作区。 */
export function AccountRoutes({ platform }: { platform: AppPlatform }) {
  const native = platform !== "web"
  const extensionAdminRoutes = appExtensions().adminRoutes
  return (
    <Routes>
      <Route path="/login" element={<LoginPage allowServerChange={native} />} />
      <Route path="/register" element={<RegisterPage />} />
      <Route path="/setup" element={native ? <Navigate to="/connect" replace /> : <SetupPage />} />
      {native ? <Route path="/connect" element={<ServerConnectionPage />} /> : null}
      {native ? <Route path="/upgrade" element={<ClientUpgradePage />} /> : null}
      <Route path="/workspaces" element={<WorkspaceListPage />} />
      <Route path="/workspaces/new" element={<WorkspaceCreatePage />} />
      <Route path="/invitations/:token" element={<InvitationPage />} />
      <Route path="/channel-bindings/:token" element={<ChannelBindingPage />} />
      <Route path={adminLoginPath} element={<AdminLoginPage />} />
      <Route
        path={adminRootPath}
        element={
          <AdminGate>{(account) => <AdminShell account={account} />}</AdminGate>
        }
      >
        <Route index element={<Navigate to={adminDashboardPath} replace />} />
        <Route path={relativeAdminPath(adminDashboardPath)} element={<PlatformDashboardPage />} />
        <Route path={relativeAdminPath(adminOverviewPath)} element={<LegacyOverviewRedirect />} />
        <Route path={relativeAdminPath(adminOverviewScalePath)} element={<Navigate to={adminDashboardPath} replace />} />
        {adminOverviewSections.map((section) => (
          <Route
            key={section}
            path={relativeAdminPath(adminOverviewSectionPath(section))}
            element={<PlatformOverviewPage section={section} />}
          />
        ))}
        <Route path={relativeAdminPath(adminWorkspacesPath)} element={<AdminWorkspacesPage />} />
        <Route path={relativeAdminPath(adminAccountsPath)} element={<AdminAccountsPage />} />
        <Route path={relativeAdminPath(adminSystemPath)} element={<PlatformSitePage />} />
        <Route path={`${relativeAdminPath(adminSystemPath)}/:section`} element={<PlatformDeploymentPage />} />
        <Route path={relativeAdminPath(adminDeploymentPath)} element={<LegacyDeploymentRedirect />} />
        <Route path="license" element={<PlatformLicensePage />} />
        {extensionAdminRoutes.map((route) => (
          <Route
            key={route.path}
            path={relativeAdminPath(route.path)}
            element={route.element}
          />
        ))}
      </Route>
      <Route path="*" element={<WorkspaceEntry />} />
    </Routes>
  )
}

/** 把工作区路由器内的账号级地址交给根路由器。 */
function LeaveWorkspace() {
  const location = useLocation()
  useEffect(() => {
    navigateToHashPath(`${location.pathname}${location.search}`, { replace: true })
  }, [location.pathname, location.search])
  return null
}

/** 工作区路由器：账号级地址转交根路由器，其余地址在确定工作区后渲染工作区页面。 */
export function WorkspaceRoutes({ slug, children }: { slug: string; children: ReactNode }) {
  return (
    <Routes>
      {accountPaths.map((path) => (
        <Route key={path} path={path} element={<LeaveWorkspace />} />
      ))}
      <Route path="*" element={<WorkspaceGate slug={slug}>{children}</WorkspaceGate>} />
    </Routes>
  )
}

export {
  adminAccountsPath,
  adminDashboardPath,
  adminDeploymentPath,
  adminLicensePath,
  adminOverviewPath,
  adminWorkspacesPath,
}
