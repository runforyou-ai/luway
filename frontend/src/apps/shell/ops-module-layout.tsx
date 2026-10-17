/** Shell 运营导航分组外壳：二级栏为 AI 表现、团队表现、AI 员工、渠道、知识库、工作区电脑六个入口，按所属角色的权限显示。 */
import { BotIcon, ChartColumnIcon, HeadsetIcon, LibraryIcon, RadioTowerIcon, ServerIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Outlet, useLocation, useNavigate } from "react-router"

import { PermissionCode } from "@/api"
import { PagePaneLink, PagePaneNav, PageSplit } from "@/components/page-split"
import { NativeSelect } from "@/components/ui/native-select"
import { useWorkspace } from "@/contexts/workspace-context"
import { hasPermission } from "@/lib/permissions"

/** 运营分组入口：路径前缀、图标、导航文案和显示入口所需的权限。 */
const opsModuleEntries = [
  // AI 表现对所有成员提供本人负责的待补知识，AI 员工列表对所有成员提供本人的个人 AI 员工。
  { path: "/ai-performance", icon: ChartColumnIcon, label: "navigation.performance", permission: undefined },
  { path: "/team-performance", icon: HeadsetIcon, label: "navigation.teamPerformance", permission: PermissionCode.PermissionReportsView },
  { path: "/ai-employees", icon: BotIcon, label: "navigation.agents", permission: undefined },
  { path: "/channels", icon: RadioTowerIcon, label: "navigation.channels", permission: [PermissionCode.PermissionCustomerServiceManage, PermissionCode.PermissionEmployeeServiceManage] },
  { path: "/knowledge-bases", icon: LibraryIcon, label: "navigation.knowledgeBases", permission: PermissionCode.PermissionAIEmployeesManage },
  { path: "/computers", icon: ServerIcon, label: "navigation.computers", permission: PermissionCode.PermissionWorkspaceManage },
] as const

/** 运营分组下各入口的路径前缀，一级导航按这些前缀保持选中。 */
export const opsModulePaths = opsModuleEntries.map((entry) => entry.path)

/** 渲染运营分组二级栏和当前入口的页面；窄视口下以下拉选择切换入口。 */
export function OpsModuleLayout() {
  const { t } = useTranslation("agents")
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const { identity } = useWorkspace()
  const entries = opsModuleEntries.filter((entry) => hasPermission(identity.user, entry.permission))
  const currentPath =
    opsModuleEntries.find(
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
