/** 移动端身份入口、一级导航和详情布局。 */
import { createContext, Suspense, useContext, useEffect, type ReactNode } from "react"
import { ContactRoundIcon, InboxIcon, MessageCircleIcon, UserRoundIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Navigate, NavLink, Outlet, useLocation, useNavigate } from "react-router"

import type { Identity, PermissionCode } from "@/api"
import { useMobileMessageNotifications } from "@/apps/mobile/shared/mobile-message-notifications"
import {
  MobileNavigationProvider,
  useMobileNavigation,
} from "@/apps/mobile/shared/mobile-navigation"
import { useResponsibleKnowledgeGapCount } from "@/features/knowledge-gaps/use-responsible-knowledge-gaps"
import { LoadingIndicator } from "@/components/loading-indicator"
import { UnsavedChangesGuard } from "@/components/unsaved-changes-guard"
import { useWorkspaceAttention } from "@/hooks/use-workspace-attention"
import { loadInboxAttention } from "@/features/inbox/list/inbox-attention"
import { SessionShell } from "@/features/session/session-shell"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { hasPermission } from "@/lib/permissions"
import { cn } from "@/lib/utils"

const MobileWorkspaceContext = createContext<Identity | null>(null)

/** 在登录工作区内挂载新消息系统通知与应用角标。 */
function MobileMessageNotifications({ identity }: { identity: Identity }) {
  useMobileMessageNotifications(identity)
  return null
}

/** 在登录外壳内为所有移动端页面提供身份、导航上下文和未保存内容确认，回到前台时重建实时事件流。 */
export function MobileWorkspaceLayout() {
  return (
    <SessionShell restartOnResume>
      {(identity) => (
        <MobileWorkspaceContext value={identity}>
          <MobileMessageNotifications identity={identity} />
          <MobileNavigationProvider>
            <UnsavedChangesGuard>
              <div className="flex h-dvh min-h-0 flex-col overflow-hidden bg-sidebar pt-[env(safe-area-inset-top)]">
                <Outlet />
              </div>
            </UnsavedChangesGuard>
          </MobileNavigationProvider>
        </MobileWorkspaceContext>
      )}
    </SessionShell>
  )
}

/** 为一级页面显示固定底部导航，收件箱显示待处理会话数，消息显示聊天提醒未读数，我的显示其他工作区未读与待补知识数。 */
export function MobileTabLayout() {
  const { t } = useTranslation(["mobile", "inbox", "account", "workspace"])
  const { chatsURL, inboxURL } = useMobileNavigation()
  const location = useLocation()
  const navigate = useNavigate()
  useEffect(() => {
    // 一级页签的系统返回先切回收件箱，已在收件箱时退到后台。
    const handleBack = (event: Event) => {
      if (event.defaultPrevented) return
      event.preventDefault()
      if (location.pathname === "/inbox") {
        const detail = (event as CustomEvent<{ background?: boolean } | null>).detail
        if (detail) detail.background = true
        return
      }
      navigate(inboxURL, { replace: true })
    }
    window.addEventListener("app:back", handleBack)
    return () => window.removeEventListener("app:back", handleBack)
  }, [inboxURL, location.pathname, navigate])
  const { identity } = useMobileWorkspace()
  // 提醒总数按权威查询读取，会话变化由同步协调器失效该查询。
  const attention = useResource(
    resourceKeys.inboxAttention({
      workspaceId: identity.workspace.id,
      userId: identity.user.id,
    }),
    loadInboxAttention,
  )
  const otherWorkspacesUnread = useWorkspaceAttention(identity.workspace.id).others
  const gapCount = useResponsibleKnowledgeGapCount()
  const tabs = [
    {
      path: inboxURL,
      label: t("tabs.inbox"),
      icon: InboxIcon,
      badge: attention.data?.pending ?? 0,
      badgeLabel: t("inbox:pendingCount", { count: attention.data?.pending ?? 0 }),
      // 待处理会话数是待办数量，使用中性色。
      neutral: true,
    },
    {
      path: chatsURL,
      label: t("tabs.chats"),
      icon: MessageCircleIcon,
      badge: attention.data?.unread ?? 0,
      badgeLabel: t("inbox:chatAttentionUnread", { count: attention.data?.unread ?? 0 }),
    },
    { path: "/contacts", label: t("tabs.contacts"), icon: ContactRoundIcon },
    {
      path: "/me",
      label: t("tabs.me"),
      icon: UserRoundIcon,
      // 其他工作区的未读与本人负责的待补知识在「我的」上提示，从这里切换工作区或处理待补知识。
      badge: otherWorkspacesUnread + gapCount,
      badgeLabel: [
        otherWorkspacesUnread > 0 ? t("account:otherWorkspacesUnread", { count: otherWorkspacesUnread }) : "",
        gapCount > 0 ? t("workspace:responsibleGapCount", { count: gapCount }) : "",
      ]
        .filter(Boolean)
        .join(" · "),
    },
  ]
  return (
    <>
      <main className="min-h-0 flex-1 overflow-hidden bg-background">
        <Outlet />
      </main>
      <nav
        aria-label={t("tabs.label")}
        className="shrink-0 border-t bg-sidebar pb-[env(safe-area-inset-bottom)]"
      >
        <div className="grid grid-cols-4">
          {tabs.map(({ path, label, icon: Icon, badge, badgeLabel, neutral }) => (
            <NavLink
              key={label}
              to={path}
              replace
              className={({ isActive }) =>
                cn(
                  "flex min-h-14 flex-col items-center justify-center gap-0.5 px-3 text-xs font-medium text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
                  isActive && "text-primary",
                )
              }
            >
              <span className="relative">
                <Icon className="size-5" />
                {badge ? (
                  <span
                    className={cn(
                      "absolute -top-1.5 left-3 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] leading-4 font-semibold ring-2 ring-sidebar",
                      neutral
                        ? "bg-foreground/10 text-foreground"
                        : "bg-destructive text-destructive-foreground",
                    )}
                  >
                    <span aria-hidden="true">{badge > 99 ? "99+" : badge}</span>
                    <span className="sr-only">{badgeLabel}</span>
                  </span>
                ) : null}
              </span>
              <span>{label}</span>
            </NavLink>
          ))}
        </div>
      </nav>
    </>
  )
}

/** 为详情页面保留底部安全区并隐藏一级导航。 */
export function MobileDetailLayout() {
  return (
    <main className="min-h-0 flex-1 overflow-hidden bg-background pb-[env(safe-area-inset-bottom)]">
      <Suspense fallback={<LoadingIndicator className="h-full justify-center" />}>
        <Outlet />
      </Suspense>
    </main>
  )
}

/** 返回移动端工作区中的当前身份。 */
export function useMobileWorkspace() {
  const identity = useContext(MobileWorkspaceContext)
  if (!identity) throw new Error("移动端页面必须位于登录工作区内")
  return { identity }
}

/** 返回移动端当前成员所属角色是否拥有指定权限。 */
export function useMobilePermission(code: PermissionCode) {
  return hasPermission(useMobileWorkspace().identity.user, code)
}

/** 所属角色拥有指定权限时渲染页面，否则回到通讯录。 */
export function MobilePermissionRoute({ permission, children }: { permission: PermissionCode; children: ReactNode }) {
  const allowed = useMobilePermission(permission)
  return allowed ? children : <Navigate to="/contacts" replace />
}
