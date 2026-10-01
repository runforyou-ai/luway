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
  ShieldCheckIcon,
  SlidersHorizontalIcon,
  UserRoundIcon,
  UsersRoundIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"

import { PagePaneGroup, PagePaneLink } from "@/components/page-split"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 设置导航，进入设置后替换模块栏内容。 */
export function WorkspaceSettingsMenu({
  appHref,
  collapsed,
  railToggle,
}: {
  appHref: string
  collapsed: boolean
  railToggle: ReactNode
}) {
  const { t } = useTranslation("settings")

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
          to="/settings/devices"
          icon={MonitorSmartphoneIcon}
        >
          {t("navigation.devices")}
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
      <PagePaneGroup title={t("groups.organization")} collapsed={collapsed}>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/general"
          icon={Building2Icon}
        >
          {t("navigation.general")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/customer-service"
          icon={HeadsetIcon}
        >
          {t("navigation.customerService")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/members"
          icon={UsersRoundIcon}
        >
          {t("navigation.members")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/roles"
          icon={ShieldCheckIcon}
        >
          {t("navigation.roles")}
        </PagePaneLink>
      </PagePaneGroup>
      {/* 集成：模型服务与联网搜索供 AI 使用。 */}
      <PagePaneGroup title={t("groups.integrations")} collapsed={collapsed}>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/model-services"
          icon={BrainCircuitIcon}
        >
          {t("navigation.modelServices")}
        </PagePaneLink>
        <PagePaneLink
          collapsed={collapsed}
          to="/settings/web-search"
          icon={GlobeIcon}
        >
          {t("navigation.webSearch")}
        </PagePaneLink>
      </PagePaneGroup>
    </nav>
  )
}
