/** 工作台左下角的用户菜单：工作状态、设置入口、在客户端中使用、帮助文档、桌面端检查更新、切换或创建工作区与退出登录。 */
import { useRef, useState } from "react"
import {
  CheckIcon,
  CircleArrowUpIcon,
  CircleHelpIcon,
  LayoutGridIcon,
  LoaderCircleIcon,
  LogOutIcon,
  MonitorSmartphoneIcon,
  PlusIcon,
  SettingsIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import type { Identity } from "@/api"
import { useUnsavedChangesContext } from "@/contexts/unsaved-changes-context"
import { useWorkspaceScope } from "@/contexts/workspace-scope-context"
import { useWorkStatusChange } from "@/hooks/use-work-status-change"
import { useWorkspaceAttention } from "@/hooks/use-workspace-attention"
import { ClientLinkDialog } from "@/features/server-connection/client-link-dialog"
import { useClientUpdate } from "@/features/server-connection/client-update"
import { resolveAppPlatform } from "@/platform/app-platform"
import { openProductDocs } from "@/platform/product-docs"
import { enterWorkspace, navigateToHashPath, withReturnTo } from "@/lib/workspace-route"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { CountBadge } from "@/components/count-badge"
import { WorkStatusDot, WorkStatusPicker } from "@/components/work-status"
import { UserAvatar } from "@/components/user-avatar"
import { cn } from "@/lib/utils"

/** 展示当前成员头像与工作区，展开后切换工作状态、进入设置、打开帮助文档、检查更新、切换工作区或确认后退出登录；桌面端发现新版本时提示更新。 */
export function WorkspaceUserMenu({
  identity,
  collapsed,
  loggingOut,
  onLogout,
}: {
  identity: Identity
  collapsed: boolean
  loggingOut: boolean
  onLogout: () => void
}) {
  const { t, i18n } = useTranslation(["workspace", "account", "connection", "common"])
  const workspaceScope = useWorkspaceScope()
  const workspaceAttention = useWorkspaceAttention(workspaceScope.current.id)
  const navigate = useNavigate()
  const unsavedChanges = useUnsavedChangesContext()
  const [userMenuOpen, setUserMenuOpen] = useState(false)
  const [clientLinkOpen, setClientLinkOpen] = useState(false)
  const [confirmingLogout, setConfirmingLogout] = useState(false)
  const workStatus = useWorkStatusChange(identity.user.workStatus)
  const userMenuTriggerRef = useRef<HTMLButtonElement>(null)
  const clientUpdate = useClientUpdate(resolveAppPlatform() === "desktop")
  const skipUserMenuFocusRestoreRef = useRef(false)

  /** 从用户菜单进入页面，并清除头像触发器的选中效果。 */
  function navigateFromUserMenu(path: string) {
    skipUserMenuFocusRestoreRef.current = true
    navigate(path)
  }

  return (
    <div
      className={cn("shrink-0 pt-1 pb-2.5", collapsed ? "px-0" : "pr-0 pl-1.5")}
    >
      <DropdownMenu open={userMenuOpen} onOpenChange={setUserMenuOpen}>
        <DropdownMenuTrigger asChild>
          <button
            ref={userMenuTriggerRef}
            type="button"
            className={cn(
              "flex w-full items-center rounded-md text-left outline-none hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring",
              collapsed
                ? "justify-center py-1"
                : "gap-2.5 px-2.5 py-1.5",
            )}
            // 头像上的未读红点并入按钮名称，收起侧栏时同样可读。
            aria-label={
              workspaceAttention.others > 0
                ? t("openUserMenuWithUnread", { name: identity.user.displayName, count: workspaceAttention.others })
                : t("openUserMenu", { name: identity.user.displayName })
            }
          >
            <span className="relative size-8 shrink-0">
              <UserAvatar
                user={identity.user}
                className="size-full rounded-lg"
              />
              <WorkStatusDot
                status={identity.user.workStatus}
                className="absolute -right-0.5 -bottom-0.5 ring-2 ring-sidebar"
              />
              {/* 其他工作区有未读时在头像右上角提示，打开菜单切换。 */}
              {workspaceAttention.others > 0 ? (
                <span
                  aria-hidden="true"
                  className="absolute -top-0.5 -right-0.5 size-2.5 rounded-full bg-destructive ring-2 ring-sidebar"
                />
              ) : null}
            </span>
            {collapsed ? null : (
              <span className="grid min-w-0 flex-1 gap-0.5 leading-tight">
                <span className="truncate text-sm font-medium">
                  {identity.user.displayName}
                </span>
                <span className="truncate text-xs text-muted-foreground">
                  {workspaceScope.current.name}
                </span>
              </span>
            )}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          side="top"
          align="start"
          className="w-56"
          onCloseAutoFocus={(event) => {
            if (!skipUserMenuFocusRestoreRef.current) {
              return
            }

            event.preventDefault()
            skipUserMenuFocusRestoreRef.current = false
            userMenuTriggerRef.current?.blur()
          }}
        >
          <DropdownMenuLabel className="p-2 font-normal">
            <div className="flex items-center gap-2.5">
              <div className="relative size-9 shrink-0">
                <UserAvatar
                  user={identity.user}
                  className="size-9 rounded-lg"
                />
                <WorkStatusDot
                  status={identity.user.workStatus}
                  className="absolute -right-0.5 -bottom-0.5 ring-2 ring-popover"
                />
              </div>
              <div className="grid min-w-0 flex-1 translate-y-0.5 gap-0.5 leading-tight">
                <span className="truncate text-base font-medium">
                  {identity.user.displayName}
                </span>
                <WorkStatusPicker
                  status={identity.user.workStatus}
                  handlesServiceRequests={identity.user.handlesServiceRequests}
                  onChange={(next) => void workStatus.change(next)}
                />
              </div>
            </div>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => navigateFromUserMenu("/settings/profile")}
          >
            <SettingsIcon />
            {t("settings")}
          </DropdownMenuItem>
          {/* Web 端提供唤起桌面端和移动端的入口，客户端内不展示。 */}
          {resolveAppPlatform() === "web" ? (
            <DropdownMenuItem onSelect={() => setClientLinkOpen(true)}>
              <MonitorSmartphoneIcon />
              {t("connection:clientLink.title")}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onSelect={() => void openProductDocs("home", i18n.language)}>
            <CircleHelpIcon />
            {t("common:productDocs")}
          </DropdownMenuItem>
          {/* 桌面端从所连接服务器检查并安装更新，有新版本时标出。 */}
          {resolveAppPlatform() === "desktop" ? (
            <DropdownMenuItem onSelect={() => void clientUpdate.check()}>
              <CircleArrowUpIcon />
              <span className="flex-1">{t("connection:update.check")}</span>
              {clientUpdate.update?.version ? (
                <span className="rounded-sm bg-primary/10 px-1.5 text-xs font-medium text-primary">
                  {t("connection:update.available")}
                </span>
              ) : (
                <span className="text-xs text-muted-foreground">{clientUpdate.update?.currentVersion}</span>
              )}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <LayoutGridIcon />
              <span className="flex-1">{t("account:switchWorkspace")}</span>
              {workspaceAttention.others > 0 ? <CountBadge count={workspaceAttention.others} /> : null}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-56">
              {workspaceScope.workspaces.map((workspace) => (
                <DropdownMenuItem
                  key={workspace.id}
                  onSelect={async () => {
                    if (workspace.id === workspaceScope.current.id) return
                    setUserMenuOpen(false)
                    if (unsavedChanges && !(await unsavedChanges.confirmDiscard())) return
                    enterWorkspace(workspace.slug)
                  }}
                >
                  <span className="min-w-0 flex-1 truncate">{workspace.name}</span>
                  {workspace.id === workspaceScope.current.id ? (
                    <CheckIcon />
                  ) : (workspaceAttention.totals.get(workspace.id) ?? 0) > 0 ? (
                    <CountBadge count={workspaceAttention.totals.get(workspace.id) ?? 0} />
                  ) : null}
                </DropdownMenuItem>
              ))}
              {workspaceScope.canCreate ? (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    onSelect={async () => {
                      setUserMenuOpen(false)
                      if (unsavedChanges && !(await unsavedChanges.confirmDiscard())) return
                      navigateToHashPath(withReturnTo("/workspaces/new"))
                    }}
                  >
                    <PlusIcon />
                    {t("account:create")}
                  </DropdownMenuItem>
                </>
              ) : null}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            destructive
            disabled={loggingOut}
            onSelect={() => {
              setUserMenuOpen(false)
              setConfirmingLogout(true)
            }}
          >
            {loggingOut ? (
              <LoaderCircleIcon className="animate-spin" />
            ) : (
              <LogOutIcon />
            )}
            {loggingOut ? t("loggingOut") : t("logout")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ConfirmationDialog
        open={confirmingLogout}
        pending={false}
        title={t("logoutTitle")}
        description={t(resolveAppPlatform() === "web" ? "logoutDescription" : "logoutDescriptionClient")}
        onOpenChange={setConfirmingLogout}
        onConfirm={async () => {
          setConfirmingLogout(false)
          // 确认退出后，有未保存内容时再确认放弃。
          if (unsavedChanges && !(await unsavedChanges.confirmDiscard())) return
          onLogout()
        }}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          userMenuTriggerRef.current?.focus({ preventScroll: true })
        }}
      />
      <ClientLinkDialog open={clientLinkOpen} onOpenChange={setClientLinkOpen} />
      {clientUpdate.dialog}
    </div>
  )
}
