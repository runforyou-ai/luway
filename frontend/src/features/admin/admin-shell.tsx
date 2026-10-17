/** 平台后台外壳：可收起的分组侧栏、带面包屑与账号菜单的顶栏和全宽内容区。 */
import { useCallback, useEffect, useMemo, useState } from "react"
import {
  ActivityIcon,
  HardDriveIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LayoutGridIcon,
  LogOutIcon,
  MailIcon,
  MessageCircleIcon,
  PanelLeftIcon,
  ServerCogIcon,
  TriangleAlertIcon,
  UsersIcon,
  type LucideIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, NavLink, Outlet, useLocation, useNavigate, useOutletContext } from "react-router"

import { adminLogout, type Account } from "@/api"
import { appExtensions, type AdminNavGroup } from "@/app-extensions"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { UserPreferencesProvider } from "@/contexts/user-preferences"
import { AdminChangePasswordDialog } from "@/features/admin/admin-change-password-dialog"
import {
  adminAccountsPath,
  adminDashboardPath,
  adminLicensePath,
  adminLoginPath,
  adminOverviewSectionPath,
  adminOverviewSections,
  adminSystemPath,
  adminSystemSectionPath,
  adminSystemSections,
  adminWorkspacesPath,
  type AdminOverviewSection,
  type AdminSystemSection,
} from "@/features/admin/admin-paths"
import { AdminSurface } from "@/lib/admin-surface"
import { useLicenseReminder } from "@/features/platform/public"
import { useBrandName } from "@/lib/brand"
import { cn } from "@/lib/utils"

/** 侧栏收起状态在本机的存储键。 */
const sidebarCollapsedKey = "admin.sidebarCollapsed"

/** 监控各页面的导航图标。 */
const overviewIcons: Record<AdminOverviewSection, LucideIcon> = {
  usage: ActivityIcon,
  runtime: ServerCogIcon,
  errors: TriangleAlertIcon,
}

/** 系统组集成页的导航图标。 */
const systemIcons: Record<AdminSystemSection, LucideIcon> = {
  storage: HardDriveIcon,
  email: MailIcon,
  wechat: MessageCircleIcon,
}

/** 返回核心导航分组：运营、监控在前，系统在后。 */
function useCoreNavGroups(): { leading: AdminNavGroup[]; trailing: AdminNavGroup[] } {
  const { t } = useTranslation("admin")
  return useMemo(
    () => ({
      leading: [
        {
          id: "operations",
          label: t("nav.groups.operations"),
          items: [
            { to: adminDashboardPath, icon: LayoutDashboardIcon, label: t("nav.dashboard") },
            { to: adminWorkspacesPath, icon: LayoutGridIcon, label: t("nav.workspaces") },
            { to: adminAccountsPath, icon: UsersIcon, label: t("nav.accounts") },
          ],
        },
        {
          id: "monitor",
          label: t("nav.groups.monitor"),
          items: adminOverviewSections.map((section) => ({
            to: adminOverviewSectionPath(section),
            icon: overviewIcons[section],
            label: t(`nav.overview.${section}`),
          })),
        },
      ],
      trailing: [
        {
          id: "system",
          label: t("nav.groups.system"),
          items: [
            { to: adminSystemPath, icon: ServerCogIcon, label: t("nav.system.site"), end: true },
            ...adminSystemSections.map((section) => ({
              to: adminSystemSectionPath(section),
              icon: systemIcons[section],
              label: t(`nav.system.${section}`),
            })),
            { to: adminLicensePath, icon: KeyRoundIcon, label: t("nav.license") },
          ],
        },
      ],
    }),
    [t],
  )
}

/** 读取并持久化侧栏收起状态。 */
function useSidebarCollapsed() {
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return window.localStorage.getItem(sidebarCollapsedKey) === "1"
    } catch {
      return false
    }
  })

  useEffect(() => {
    try {
      window.localStorage.setItem(sidebarCollapsedKey, collapsed ? "1" : "0")
    } catch (error) {
      console.warn("保存侧栏状态失败", error)
    }
  }, [collapsed])

  const toggle = useCallback(() => setCollapsed((current) => !current), [])
  return [collapsed, toggle] as const
}

/** 平台后台布局。 */
export function AdminShell({ account }: { account: Account }) {
  const { t } = useTranslation("admin")
  const productName = useBrandName()
  const navigate = useNavigate()
  const core = useCoreNavGroups()
  const extensionGroups = appExtensions().useAdminNavGroups()
  const groups = useMemo(() => [...core.leading, ...extensionGroups, ...core.trailing], [core, extensionGroups])
  const [collapsed, toggleCollapsed] = useSidebarCollapsed()
  useLicenseReminder()

  async function logout() {
    await adminLogout()
    navigate(adminLoginPath, { replace: true })
  }

  return (
    <AdminSurface>
      <UserPreferencesProvider user={account}>
        <TooltipProvider delayDuration={200}>
          <div data-admin className="fixed inset-0 flex overflow-hidden bg-background text-foreground">
            <aside
              data-collapsed={collapsed}
              className={cn(
                "flex shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground transition-[width] duration-200",
                collapsed ? "w-14" : "w-60",
              )}
            >
              <div className={cn("flex h-12 shrink-0 items-center gap-2.5 border-b border-sidebar-border", collapsed ? "justify-center px-0" : "px-4")}>
                <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-background p-0.5 shadow-sm ring-1 ring-border/60">
                  <img src="/favicon.png" alt="" className="size-full object-contain" draggable={false} />
                </span>
                {collapsed ? null : <span className="truncate text-sm font-semibold tracking-tight text-sidebar-accent-foreground">{productName}</span>}
              </div>
              <nav className="min-h-0 flex-1 overflow-y-auto px-2 py-3" aria-label={t("shell.navLabel")}>
                {groups.map((group, index) => (
                  <div key={group.id} className="mb-4 last:mb-0">
                    {collapsed ? (
                      index > 0 ? <div className="mx-2 mb-2 border-t border-sidebar-border" /> : null
                    ) : (
                      <div className="px-2.5 pb-1.5 text-[11px] font-medium tracking-wide text-sidebar-foreground/50">{group.label}</div>
                    )}
                    <ul className="space-y-0.5">
                      {group.items.map((item) => (
                        <li key={item.to}>
                          <AdminNavItem item={item} collapsed={collapsed} />
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </nav>
            </aside>
            <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
              <header className="flex h-12 shrink-0 items-center gap-2 border-b border-border bg-card px-3">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t(collapsed ? "shell.expandSidebar" : "shell.collapseSidebar")}
                  title={t(collapsed ? "shell.expandSidebar" : "shell.collapseSidebar")}
                  aria-expanded={!collapsed}
                  onClick={toggleCollapsed}
                >
                  <PanelLeftIcon />
                </Button>
                <div className="h-4 w-px bg-border" aria-hidden="true" />
                <AdminBreadcrumb groups={groups} />
                <div className="ml-auto">
                  <AccountMenu account={account} onLogout={() => void logout()} />
                </div>
              </header>
              <main className="flex min-h-0 flex-1 flex-col overflow-hidden bg-background">
                <Outlet context={{ account } satisfies AdminOutletContext} />
              </main>
            </div>
          </div>
        </TooltipProvider>
      </UserPreferencesProvider>
    </AdminSurface>
  )
}

/** 平台后台子页面从外壳取得的上下文。 */
export type AdminOutletContext = {
  account: Account
}

/** 读取平台后台外壳上下文。 */
export function useAdminOutlet() {
  return useOutletContext<AdminOutletContext>()
}

/** 侧栏的一个一级入口；收起时只显示图标，悬停显示名称。 */
function AdminNavItem({ item, collapsed }: { item: AdminNavGroup["items"][number]; collapsed: boolean }) {
  const Icon = item.icon
  const { pathname } = useLocation()
  // TooltipTrigger asChild 会把函数 className 拼成字符串，因此用路径自行判断选中态。
  const isActive = pathname === item.to || (!item.end && pathname.startsWith(`${item.to}/`))
  const link = (
    <NavLink
      to={item.to}
      aria-label={collapsed ? item.label : undefined}
      className={cn(
        "relative flex h-8 items-center gap-2.5 rounded-md text-[13px] transition-colors",
        collapsed ? "justify-center" : "px-2.5",
        isActive
          ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground before:absolute before:inset-y-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-sidebar-primary"
          : "text-sidebar-foreground/80 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" aria-hidden="true" />
      {collapsed ? null : <span className="truncate">{item.label}</span>}
    </NavLink>
  )
  if (!collapsed) return link
  return (
    <Tooltip>
      <TooltipTrigger asChild>{link}</TooltipTrigger>
      <TooltipContent side="right" sideOffset={8}>
        {item.label}
      </TooltipContent>
    </Tooltip>
  )
}

/** 按当前路径匹配最长的入口地址，显示「分组 / 入口」，位于入口子页面时入口可点回列表。 */
function AdminBreadcrumb({ groups }: { groups: AdminNavGroup[] }) {
  const { t } = useTranslation("admin")
  const { pathname } = useLocation()
  const match = useMemo(() => {
    let best: { group: AdminNavGroup; item: AdminNavGroup["items"][number] } | undefined
    for (const group of groups) {
      for (const item of group.items) {
        if (pathname !== item.to && (item.end || !pathname.startsWith(`${item.to}/`))) continue
        if (!best || item.to.length > best.item.to.length) best = { group, item }
      }
    }
    return best
  }, [groups, pathname])

  if (!match) return null
  const nested = pathname !== match.item.to
  return (
    <nav aria-label={t("shell.breadcrumbLabel")} className="min-w-0">
      <ol className="flex items-center gap-1.5 text-sm">
        <li className="truncate text-muted-foreground">{match.group.label}</li>
        <li aria-hidden="true" className="text-muted-foreground/60">
          /
        </li>
        <li className="truncate">
          {nested ? (
            <Link to={match.item.to} className="text-muted-foreground hover:text-foreground">
              {match.item.label}
            </Link>
          ) : (
            <span aria-current="page" className="font-medium">
              {match.item.label}
            </span>
          )}
        </li>
      </ol>
    </nav>
  )
}

/** 顶栏的账号菜单：显示当前管理员，并提供修改密码与退出后台。 */
function AccountMenu({ account, onLogout }: { account: Account; onLogout: () => void }) {
  const { t } = useTranslation("admin")
  const [changePasswordOpen, setChangePasswordOpen] = useState(false)
  const initial = Array.from(account.displayName.trim())[0] ?? "?"
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" className="h-8 gap-2 px-1.5" aria-label={t("shell.accountMenu")}>
            <span className="flex size-6 items-center justify-center rounded-full bg-primary text-xs font-medium text-primary-foreground">{initial}</span>
            <span className="hidden max-w-40 truncate text-sm font-normal sm:inline">{account.displayName}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-52">
          <DropdownMenuLabel className="font-normal">
            <div className="truncate text-sm font-medium">{account.displayName}</div>
            <div className="truncate text-xs text-muted-foreground">{account.email}</div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => setChangePasswordOpen(true)}>
            <KeyRoundIcon />
            {t("shell.changePassword")}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onLogout}>
            <LogOutIcon />
            {t("shell.logout")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <AdminChangePasswordDialog open={changePasswordOpen} onOpenChange={setChangePasswordOpen} />
    </>
  )
}
