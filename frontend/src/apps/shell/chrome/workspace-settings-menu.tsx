/** 设置页的一级导航，进入设置后替换模块栏内容。 */
import type { ReactNode } from "react"
import {
  ArchiveIcon,
  BellIcon,
  BrainCircuitIcon,
  GlobeIcon,
  Building2Icon,
  HeadsetIcon,
  ChevronLeftIcon,
  LockKeyholeIcon,
  HardDriveIcon,
  MonitorSmartphoneIcon,
  PlugIcon,
  ShieldCheckIcon,
  SlidersHorizontalIcon,
  UserRoundIcon,
  UsersRoundIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"

import { PermissionCode, type CurrentUser } from "@/api"
import { appExtensions } from "@/app-extensions"
import { PagePaneGroup, PagePaneLink } from "@/components/page-split"
import { useModelSource } from "@/hooks/use-model-source"
import { hasPermission } from "@/lib/permissions"
import { businessSystemsPath } from "@/lib/workspace-paths"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 设置导航，进入设置后替换模块栏内容。 */
export function WorkspaceSettingsMenu({
  user,
  appHref,
  collapsed,
  railToggle,
}: {
  user: Pick<CurrentUser, "permissions">
  appHref: string
  collapsed: boolean
  railToggle: ReactNode
}) {
  const { t } = useTranslation("settings")
  const { platformOnly } = useModelSource()
  const can = (code: PermissionCode) => hasPermission(user, code)
  const extensionLinks = appExtensions().useSettingsMenuLinks()
  // 工作区分组按所属角色的权限显示入口，没有可见入口时不显示分组。
  const manageWorkspace = can(PermissionCode.PermissionWorkspaceManage)
  const showCustomerService = can(PermissionCode.PermissionCustomerServiceManage)
  const showEmployeeService = can(PermissionCode.PermissionEmployeeServiceManage)
  const showEvaluation = can(PermissionCode.PermissionAIEmployeesManage)
  const showWorkspace = showEmployeeService || showEvaluation || manageWorkspace || showCustomerService || extensionLinks.workspace.length > 0

  const backToApp = (
    <PagePaneLink
      to={appHref}
      icon={ChevronLeftIcon}
      collapsed={collapsed}
      // 左箭头字形本身内缩，展开时整行左移抵消，与下方导航项视觉左对齐。
      className={collapsed ? undefined : "-ml-1"}
    >
      {t("backToApp")}
    </PagePaneLink>
  )

  return (
    <nav
      className={cn(
        "flex min-h-0 flex-1 flex-col overflow-y-auto",
        collapsed
          ? "items-center gap-1.5 pt-1"
          : "items-stretch gap-0.5 pt-1 pr-0 pl-1.5",
      )}
      aria-label={t("navigationLabel")}
    >
      {railToggle ? (
        <div className="flex items-center gap-1 pr-3">
          <div className="min-w-0 flex-1">{backToApp}</div>
          {railToggle}
        </div>
      ) : (
        backToApp
      )}
      <PagePaneGroup title={t("groups.personal")} collapsed={collapsed}>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/profile"
          icon={UserRoundIcon}
        >
          {t("navigation.profile")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/security"
          icon={LockKeyholeIcon}
        >
          {t("navigation.security")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/preferences"
          icon={SlidersHorizontalIcon}
        >
          {t("navigation.preferences")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/notifications"
          icon={BellIcon}
        >
          {t("navigation.notifications")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/computers"
          icon={MonitorSmartphoneIcon}
        >
          {t("navigation.computers")}
        </PagePaneLink>
        {resolveAppPlatform() === "desktop" ? (
          <PagePaneLink
            collapsed={collapsed}
            to="/settings/local"
            icon={HardDriveIcon}
          >
            {t("navigation.local")}
          </PagePaneLink>
        ) : null}
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/archived-chats"
          icon={ArchiveIcon}
        >
          {t("navigation.archivedChats")}
        </PagePaneLink>
      </PagePaneGroup>
      {showWorkspace ? (
        <PagePaneGroup title={t("groups.workspace")} collapsed={collapsed}>
          {manageWorkspace ? (
            <PagePaneLink
              collapsed={collapsed}
              to="/settings/general"
              icon={Building2Icon}
            >
              {t("navigation.general")}
            </PagePaneLink>
          ) : null}
          {showEmployeeService ? (
            <PagePaneLink collapsed={collapsed} to="/settings/employee-service" icon={HeadsetIcon}>{t("navigation.employeeService")}</PagePaneLink>
          ) : null}
          {showEvaluation ? (
            <PagePaneLink collapsed={collapsed} to="/settings/ai-evaluation" icon={HeadsetIcon}>{t("navigation.aiEvaluation")}</PagePaneLink>
          ) : null}
          {showCustomerService ? (
            <PagePaneLink
              collapsed={collapsed}
              to="/settings/customer-service"
              icon={HeadsetIcon}
            >
              {t("navigation.customerService")}
            </PagePaneLink>
          ) : null}
          {manageWorkspace ? (
            <PagePaneLink
              collapsed={collapsed}
              to="/settings/members"
              icon={UsersRoundIcon}
            >
              {t("navigation.members")}
            </PagePaneLink>
          ) : null}
          {manageWorkspace ? (
            <PagePaneLink
              collapsed={collapsed}
              to="/settings/roles"
              icon={ShieldCheckIcon}
            >
              {t("navigation.roles")}
            </PagePaneLink>
          ) : null}
          {extensionLinks.workspace.map((link) => (
            <PagePaneLink key={link.to} collapsed={collapsed} to={link.to} icon={link.icon}>
              {link.label}
            </PagePaneLink>
          ))}
        </PagePaneGroup>
      ) : null}
      {/* 集成：模型服务、业务系统与互联网供 AI 使用，构建挂接的入口排在模型服务之后。 */}
      <PagePaneGroup title={t("groups.integrations")} collapsed={collapsed}>
        {manageWorkspace && !platformOnly ? (
          <PagePaneLink
            collapsed={collapsed}
            to="/settings/model-services"
            icon={BrainCircuitIcon}
          >
            {t("navigation.modelServices")}
          </PagePaneLink>
        ) : null}
        {manageWorkspace ? (
          <PagePaneLink
            collapsed={collapsed}
            to="/settings/media-understanding"
            icon={BrainCircuitIcon}
          >
            {t("navigation.mediaUnderstanding")}
          </PagePaneLink>
        ) : null}
        {extensionLinks.integrations.map((link) => (
          <PagePaneLink key={link.to} collapsed={collapsed} to={link.to} icon={link.icon}>
            {link.label}
          </PagePaneLink>
        ))}
        {manageWorkspace ? (
          <PagePaneLink
            collapsed={collapsed}
            to={businessSystemsPath}
            icon={PlugIcon}
          >
            {t("navigation.businessSystems")}
          </PagePaneLink>
        ) : null}
        {manageWorkspace ? (
          <PagePaneLink
            collapsed={collapsed}
            to="/settings/web"
            icon={GlobeIcon}
          >
            {t("navigation.web")}
          </PagePaneLink>
        ) : null}
      </PagePaneGroup>
    </nav>
  )
}
