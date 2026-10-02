/** 部署管理的一级导航。 */
import {
  ChevronLeftIcon,
  GaugeIcon,
  LayoutGridIcon,
  UserPlusIcon,
  UsersRoundIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"

import { PagePaneLink } from "@/components/page-split"
import { WorkspaceRailToggle } from "@/components/workspace-rail"
import { cn } from "@/lib/utils"

/** 渲染返回工作区入口和部署管理页面导航；窄栏下只显示图标，未给出 onToggleRail 时不显示收起开关。 */
export function AdminNavigation({
  collapsed,
  onToggleRail,
}: {
  collapsed: boolean
  onToggleRail?: () => void
}) {
  const { t } = useTranslation("admin")

  // 根地址进入最近使用的工作区。
  const backToApp = (
    <PagePaneLink to="/" icon={ChevronLeftIcon} collapsed={collapsed} className={collapsed ? undefined : "-ml-1"}>
      {t("backToApp")}
    </PagePaneLink>
  )

  return (
    <aside className="app-workspace-rail flex h-full shrink-0 flex-col text-sidebar-foreground">
      {collapsed && onToggleRail ? (
        <div className="flex shrink-0 justify-center pb-2">
          <WorkspaceRailToggle collapsed tooltipSide="right" onToggle={onToggleRail} />
        </div>
      ) : null}
      <nav
        className={cn(
          "flex min-h-0 flex-1 flex-col overflow-y-auto",
          collapsed ? "items-center gap-1.5 pt-1" : "items-stretch gap-0.5 pt-1 pr-3 pl-1.5",
        )}
        aria-label={t("navigationLabel")}
      >
        {collapsed ? (
          backToApp
        ) : (
          <div className="mb-3 flex items-center gap-1">
            <div className="min-w-0 flex-1">{backToApp}</div>
            {onToggleRail ? <WorkspaceRailToggle collapsed={false} onToggle={onToggleRail} /> : null}
          </div>
        )}
        <PagePaneLink to="/admin/overview" icon={GaugeIcon} collapsed={collapsed}>
          {t("navigation.overview")}
        </PagePaneLink>
        <PagePaneLink to="/admin/accounts" icon={UsersRoundIcon} collapsed={collapsed}>
          {t("navigation.accounts")}
        </PagePaneLink>
        <PagePaneLink to="/admin/workspaces" icon={LayoutGridIcon} collapsed={collapsed}>
          {t("navigation.workspaces")}
        </PagePaneLink>
        <PagePaneLink to="/admin/registration" icon={UserPlusIcon} collapsed={collapsed}>
          {t("navigation.registration")}
        </PagePaneLink>
      </nav>
    </aside>
  )
}
