/** AI 员工模块的分栏外壳：二级栏为 AI 表现、团队表现、AI 员工、渠道、知识库、业务系统、工作区电脑七个入口，按所属角色的权限显示。 */
import { BotIcon, ChartColumnIcon, HeadsetIcon, LibraryIcon, PlugIcon, RadioTowerIcon, ServerIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Outlet, useLocation, useNavigate } from "react-router"

import { PermissionCode } from "@/api"
import { PagePaneLink, PagePaneNav, PageSplit } from "@/components/page-split"
import { NativeSelect } from "@/components/ui/native-select"
import { useWorkspace } from "@/contexts/workspace-context"
import { hasPermission } from "@/lib/permissions"

/** 模块入口：路径前缀、图标、导航文案和显示入口所需的权限。 */
const agentsModuleEntries = [
  // AI 表现对所有成员提供本人负责的待补知识，AI 员工列表对所有成员提供本人的个人 AI 员工。
  { path: "/ai-performance", icon: ChartColumnIcon, label: "navigation.performance", permission: undefined },
  { path: "/team-performance", icon: HeadsetIcon, label: "navigation.teamPerformance", permission: PermissionCode.PermissionReportsView },
  { path: "/ai-employees", icon: BotIcon, label: "navigation.agents", permission: undefined },
  { path: "/channels", icon: RadioTowerIcon, label: "navigation.channels", permission: PermissionCode.PermissionCustomerServiceManage },
  { path: "/knowledge-bases", icon: LibraryIcon, label: "navigation.knowledgeBases", permission: PermissionCode.PermissionAIEmployeesManage },
  { path: "/business-systems", icon: PlugIcon, label: "navigation.businessSystems", permission: PermissionCode.PermissionWorkspaceManage },
  { path: "/computers", icon: ServerIcon, label: "navigation.computers", permission: PermissionCode.PermissionWorkspaceManage },
] as const

/** AI 员工模块下各入口的路径前缀，一级导航按这些前缀保持选中。 */
export const agentsModulePaths = agentsModuleEntries.map((entry) => entry.path)

/** 渲染模块二级栏和当前入口的页面；窄视口下以下拉选择切换入口。 */
export function AgentsModuleLayout() {
  const { t } = useTranslation("agents")
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { identity } = useWorkspace()
  const entries = agentsModuleEntries.filter((entry) => hasPermission(identity.user, entry.permission))
  const currentPath =
    agentsModuleEntries.find(
      (entry) => pathname === entry.path || pathname.startsWith(`${entry.path}/`),
    )?.path ?? "/ai-employees"

  return (
    <PageSplit
      paneVariant="nav"
      pane={
        <PagePaneNav label={t("navigation.label")} title={t("title")}>
          {entries.map((entry) => (
            <PagePaneLink key={entry.path} to={entry.path} icon={entry.icon}>
              {t(entry.label)}
            </PagePaneLink>
          ))}
        </PagePaneNav>
      }
    >
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <div className="app-page-gutter shrink-0 pt-4 md:hidden">
          <NativeSelect
            className="h-8 w-full"
            aria-label={t("navigation.label")}
            value={currentPath}
            onChange={(event) => navigate(event.target.value)}
          >
            {entries.map((entry) => (
              <option key={entry.path} value={entry.path}>
                {t(entry.label)}
              </option>
            ))}
          </NativeSelect>
        </div>
        <Outlet />
      </div>
    </PageSplit>
  )
}
