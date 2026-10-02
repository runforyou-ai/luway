/** 工作台左侧模块栏和用户菜单。 */
import { memo, type ReactNode } from "react"
import { BotIcon, ContactRoundIcon, InboxIcon, SearchIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import type { Identity } from "@/api"
import { PagePaneLink } from "@/components/page-split"
import { useGlobalSearch } from "@/contexts/global-search-context"
import { responsibleKnowledgeGapsPath } from "@/features/agents/agent-navigation"
import { agentsModulePaths } from "@/features/agents/agents-module-layout"
import { useResponsibleKnowledgeGapCount } from "@/features/agents/use-responsible-knowledge-gaps"
import { ChatRailSections } from "@/features/inbox/chat-rail"
import { WorkspaceRailToggle } from "@/components/workspace-rail"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { resolveAppPlatform } from "@/platform/app-platform"
import { requestNotificationPermissionFromMessageMenu } from "@/platform/notifications"

import { WorkspaceUserMenu } from "./workspace-user-menu"
import { WorkspaceSettingsMenu } from "./workspace-settings-menu"

/** 打开全局搜索的入口，尺寸与导航项一致；窄栏下收为图标并由浮层提示。 */
function WorkspaceSearchEntry({ collapsed }: { collapsed: boolean }) {
  const { t } = useTranslation("common")
  const globalSearch = useGlobalSearch()

  const trigger = (
    <button
      type="button"
      className={cn(
        "flex h-8 shrink-0 items-center text-sm focus-visible:ring-2 focus-visible:ring-sidebar-ring",
        collapsed
          ? "w-8 justify-center rounded-md hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          : "w-full min-w-0 flex-1 gap-2 rounded-full bg-background/45 px-3 text-muted-foreground/65 hover:bg-background/70 hover:text-muted-foreground",
      )}
      title={collapsed ? undefined : t("actions.searchShortcut")}
      aria-label={collapsed ? t("actions.searchPlaceholder") : undefined}
      onClick={() => globalSearch?.open()}
    >
      <SearchIcon className="size-4 shrink-0" />
      {collapsed ? null : (
        <span className="min-w-0 flex-1 truncate text-left">
          {t("actions.searchPlaceholder")}
        </span>
      )}
    </button>
  )

  if (!collapsed) {
    return trigger
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>{trigger}</TooltipTrigger>
      <TooltipContent side="right">{t("actions.searchShortcut")}</TooltipContent>
    </Tooltip>
  )
}

/** 模块栏导航，模块之后按群聊与单聊分节列出本人参与的聊天。 */
function WorkspaceMenu({
  identity,
  collapsed,
  railToggle,
  pendingCount,
  onInboxClick,
}: {
  identity: Identity
  collapsed: boolean
  railToggle: ReactNode
  pendingCount: number
  onInboxClick: () => void
}) {
  const { t } = useTranslation(["workspace", "inbox"])
  const gapCount = useResponsibleKnowledgeGapCount()

  return (
    <nav
      // 展开时右侧留白由主内容区的内缩间隙承担，使选中块与两侧可见边界等距；窄栏下图标整列居中。
      className={cn(
        "flex min-h-0 flex-1 flex-col",
        collapsed
          ? "items-center gap-1.5 pt-1"
          : "items-stretch gap-0.5 pt-1 pr-0 pl-1.5",
      )}
      aria-label={t("navigationGroup")}
    >
      {collapsed ? (
        <WorkspaceSearchEntry collapsed />
      ) : (
        <div className={cn("mb-4 flex items-center gap-1", railToggle && "pr-3")}>
          <WorkspaceSearchEntry collapsed={false} />
          {railToggle}
        </div>
      )}
      {/* 展开时导航项右侧额外留白，选中块与主内容卡片边缘拉开距离，搜索框保持原有宽度；聊天较多时模块之下整体滚动。 */}
      <div
        className={cn(
          "flex min-h-0 flex-1 flex-col overflow-y-auto",
          collapsed ? "items-center gap-1.5" : "items-stretch gap-0.5 pr-3",
        )}
      >
        <PagePaneLink
          to="/inbox"
          icon={InboxIcon}
          collapsed={collapsed}
          count={pendingCount}
          countTone="neutral"
          countLabel={t("inbox:pendingCount", { count: pendingCount })}
          onClick={onInboxClick}
        >
          {t("inbox")}
        </PagePaneLink>
        {/* 有本人负责的待补知识时直达待处理清单。 */}
        <PagePaneLink
          to={gapCount > 0 ? responsibleKnowledgeGapsPath : "/ai-employees"}
          activePath={agentsModulePaths}
          icon={BotIcon}
          collapsed={collapsed}
          count={gapCount}
          countTone="neutral"
          countLabel={t("responsibleGapCount", { count: gapCount })}
        >
          {t("agents")}
        </PagePaneLink>
        <PagePaneLink
          to="/contacts/employees"
          activePath="/contacts"
          icon={ContactRoundIcon}
          collapsed={collapsed}
        >
          {t("contacts")}
        </PagePaneLink>
        <div className={cn("flex flex-col", collapsed ? "items-center gap-1.5" : "mt-3 gap-3")}>
          <ChatRailSections identity={identity} collapsed={collapsed} />
        </div>
      </div>
    </nav>
  )
}


/** 渲染模块栏和用户菜单。 */
export const WorkspaceNavigation = memo(function WorkspaceNavigation({
  identity,
  inSettings,
  appHref,
  collapsed,
  inlineRailToggle,
  collapsedRailToggle,
  pendingCount,
  onToggleRail,
  onLogout,
  loggingOut,
}: {
  identity: Identity
  inSettings: boolean
  appHref: string
  collapsed: boolean
  inlineRailToggle: boolean
  collapsedRailToggle: boolean
  pendingCount: number
  onToggleRail: () => void
  onLogout: () => void
  loggingOut: boolean
}) {
  const { t } = useTranslation(["workspace", "account"])
  const showAppVersion = inSettings && resolveAppPlatform() === "desktop"
  // 展开态的收起开关按工作台布局放在一级栏顶部行的右侧或标题栏操作行。
  const railToggle =
    !collapsed && inlineRailToggle ? (
      <WorkspaceRailToggle collapsed={false} onToggle={onToggleRail} />
    ) : null

  /** 点击消息菜单时申请本设备通知权限。 */
  function requestMessageNotificationPermission() {
    if (!identity.user.messageNotificationsEnabled) {
      return
    }

    void requestNotificationPermissionFromMessageMenu({
      organizationId: identity.user.organizationId,
      userId: identity.user.id,
    })
      .catch((error) => {
        console.warn("从消息菜单申请通知权限失败", error)
      })
  }

  return (
    <aside className="app-workspace-rail flex h-full shrink-0 flex-col text-sidebar-foreground">
      {collapsed && collapsedRailToggle ? (
        <div className="flex shrink-0 justify-center pb-2">
          {/* 窄栏宽度有限，提示从右侧弹出。 */}
          <WorkspaceRailToggle collapsed tooltipSide="right" onToggle={onToggleRail} />
        </div>
      ) : null}
      {inSettings ? (
        <WorkspaceSettingsMenu
          appHref={appHref}
          collapsed={collapsed}
          railToggle={railToggle}
        />
      ) : (
        <WorkspaceMenu
          identity={identity}
          collapsed={collapsed}
          railToggle={railToggle}
          pendingCount={pendingCount}
          onInboxClick={requestMessageNotificationPermission}
        />
      )}
      {inSettings ? null : (
        <WorkspaceUserMenu
          identity={identity}
          collapsed={collapsed}
          loggingOut={loggingOut}
          onLogout={onLogout}
        />
      )}
      {showAppVersion && !collapsed ? (
        <span className="pt-1 pr-0 pb-2.5 pl-4 text-xs text-muted-foreground/70">
          {t("appVersion", { version: __APP_VERSION__ })}
        </span>
      ) : null}
    </aside>
  )
})
