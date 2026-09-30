/** AI 员工模块的分栏外壳：二级栏固定为 AI 表现、团队表现、AI 员工、渠道、知识库、工具六个入口。 */
import { BotIcon, ChartColumnIcon, HeadsetIcon, LibraryIcon, PlugIcon, RadioTowerIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Outlet, useLocation, useNavigate } from "react-router"

import { PagePaneLink, PagePaneNav, PageSplit } from "@/components/page-split"
import { NativeSelect } from "@/components/ui/native-select"

/** 模块入口：路径前缀、图标和导航文案。 */
const agentsModuleEntries = [
  { path: "/ai-performance", icon: ChartColumnIcon, label: "navigation.performance" },
  { path: "/team-performance", icon: HeadsetIcon, label: "navigation.teamPerformance" },
  { path: "/ai-employees", icon: BotIcon, label: "navigation.agents" },
  { path: "/channels", icon: RadioTowerIcon, label: "navigation.channels" },
  { path: "/knowledge-bases", icon: LibraryIcon, label: "navigation.knowledgeBases" },
  { path: "/tools", icon: PlugIcon, label: "navigation.tools" },
] as const

/** AI 员工模块下各入口的路径前缀，一级导航按这些前缀保持选中。 */
export const agentsModulePaths = agentsModuleEntries.map((entry) => entry.path)

/** 渲染模块二级栏和当前入口的页面；窄视口下以下拉选择切换入口。 */
export function AgentsModuleLayout() {
  const { t } = useTranslation("agents")
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const currentPath =
    agentsModuleEntries.find(
      (entry) => pathname === entry.path || pathname.startsWith(`${entry.path}/`),
    )?.path ?? "/ai-employees"

  return (
    <PageSplit
      paneVariant="nav"
      pane={
        <PagePaneNav label={t("navigation.label")} title={t("title")}>
          {agentsModuleEntries.map((entry) => (
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
            {agentsModuleEntries.map((entry) => (
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
