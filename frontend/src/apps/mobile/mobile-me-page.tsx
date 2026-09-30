/** 移动端个人中心、工作状态切换和个人设置子页。 */
import { useRef, useState } from "react"
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, useNavigate } from "react-router"
import { toast } from "sonner"

import { logout } from "@/api"
import {
  MobilePageHeader,
  MobileScrollArea,
} from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { useResponsibleKnowledgeGapCount } from "@/features/agents/use-responsible-knowledge-gaps"
import { deactivateNotificationPolicy } from "@/features/notifications/new-message-notifications"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { useUnsavedChangesContext } from "@/contexts/unsaved-changes-context"
import { CountBadge } from "@/components/count-badge"
import { Button } from "@/components/ui/button"
import { UserAvatar } from "@/components/user-avatar"
import { WorkStatusDot, WorkStatusPicker } from "@/components/work-status"
import { ChangePasswordForm } from "@/features/settings/change-password-form"
import { useWorkspaceAttention } from "@/hooks/use-workspace-attention"
import { NotificationSettingsForm } from "@/features/settings/notification-settings-form"
import { ProfileSettingsForm } from "@/features/settings/profile-settings-form"
import { UserPreferencesForm } from "@/features/settings/user-preferences-form"
import { useWorkStatusChange } from "@/hooks/use-work-status-change"
import { withReturnTo } from "@/lib/workspace-route"

const rowClassName =
  "flex min-h-14 w-full items-center gap-3 px-4 text-left text-sm outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring disabled:opacity-50"

/** 展示个人资料、工作状态、待补知识入口、当前工作区与切换入口、设置入口、退出操作和应用版本号。 */
export function MobileMePage() {
  const { t } = useTranslation(["mobile", "workspace", "common", "account", "agents"])
  const navigate = useNavigate()
  const { identity } = useMobileWorkspace()
  const otherWorkspacesUnread = useWorkspaceAttention(identity.organization.id).others
  const [loggingOut, setLoggingOut] = useState(false)
  const [confirmingLogout, setConfirmingLogout] = useState(false)
  const logoutButton = useRef<HTMLButtonElement>(null)
  const workStatus = useWorkStatusChange(identity.user.workStatus)
  const unsavedChanges = useUnsavedChangesContext()
  const gapCount = useResponsibleKnowledgeGapCount()

  /** 退出登录并回到登录页。 */
  async function handleLogout() {
    setLoggingOut(true)
    deactivateNotificationPolicy()
    // 先离开工作区，登出清空查询缓存时外壳已经卸载。
    navigate("/login", { replace: true })
    try {
      await logout()
    } catch (error) {
      console.warn("退出登录失败", error)
      toast.error(t("workspace:logoutError"))
    } finally {
      setLoggingOut(false)
    }
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("me.title")} />
      <MobileScrollArea storageKey="me" className="pt-2 pb-6">
        {/* 与桌面端用户菜单一致：名字下方直接切换工作状态，资料编辑走下方入口。 */}
        <div className="flex items-center gap-3 px-4 py-4">
          <span className="relative shrink-0">
            <UserAvatar
              user={identity.user}
              className="size-16"
            />
            <WorkStatusDot
              status={identity.user.workStatus}
              className="absolute -right-0.5 -bottom-0.5 size-3.5 ring-2 ring-background"
            />
          </span>
          <div className="grid min-w-0 flex-1 gap-1.5">
            <span className="truncate text-base font-semibold">
              {identity.user.displayName}
            </span>
            <WorkStatusPicker
              status={identity.user.workStatus}
              handlesServiceRequests={identity.user.handlesServiceRequests}
              disabled={workStatus.changing}
              itemClassName="min-h-11"
              onChange={(next) => void workStatus.change(next)}
            />
          </div>
        </div>
        {/* 本人负责的 AI 员工有待补知识时显示处理入口。 */}
        {gapCount > 0 ? (
          <div className="mb-6 border-y">
            <Link to="/me/knowledge-gaps" state={{ mobileBack: true }} className={rowClassName}>
              <span className="flex-1">{t("agents:performance.tabs.knowledgeGaps")}</span>
              <CountBadge
                count={gapCount}
                tone="neutral"
                label={t("workspace:responsibleGapCount", { count: gapCount })}
                className="h-5 min-w-5 px-1.5 text-xs"
              />
              <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
            </Link>
          </div>
        ) : null}
        <div className="mb-6 border-y">
          {/* 切换工作区经账号级的工作区列表，返回时回到这里。 */}
          <Link to={withReturnTo("/workspaces")} className={rowClassName}>
            <span className="grid flex-1 gap-0.5">
              <span>{t("me.workspace")}</span>
              {/* 其他工作区的未读单独成行，与当前工作区名称区分。 */}
              {otherWorkspacesUnread > 0 ? (
                <span className="text-xs text-destructive">
                  {t("account:otherWorkspacesUnread", { count: otherWorkspacesUnread })}
                </span>
              ) : null}
            </span>
            <span className="min-w-0 max-w-[50%] truncate text-muted-foreground">{identity.organization.name}</span>
            <ChevronRightIcon className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
          </Link>
        </div>
        <div className="divide-y border-y">
          {([["profile", "profile"], ["security", "security"], ["preferences", "preferences"], ["notifications", "notifications"], ["devices", "devices"], ["archivedChats", "archived-chats"]] as const).map(([section, path]) => (
            <Link
              key={section}
              to={`/me/${path}`}
              state={{ mobileBack: true }}
              className={rowClassName}
            >
              <span className="flex-1">{t(`me.${section}`)}</span>
              <ChevronRightIcon
                className="size-5 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
            </Link>
          ))}
        </div>
        <div className="mt-9 px-4">
          <Button
            ref={logoutButton}
            className="min-h-11 w-full"
            variant="destructive"
            disabled={loggingOut}
            onClick={() => setConfirmingLogout(true)}
          >
            {loggingOut ? t("workspace:loggingOut") : t("workspace:logout")}
          </Button>
          <ConfirmationDialog
            open={confirmingLogout}
            pending={false}
            title={t("workspace:logoutTitle")}
            description={t("workspace:logoutDescriptionClient")}
            onOpenChange={setConfirmingLogout}
            onConfirm={async () => {
              setConfirmingLogout(false)
              // 确认退出后，有未保存内容时再确认放弃。
              if (unsavedChanges && !(await unsavedChanges.confirmDiscard())) return
              void handleLogout()
            }}
            onCloseAutoFocus={(event) => {
              event.preventDefault()
              logoutButton.current?.focus({ preventScroll: true })
            }}
          />
          <p className="mt-4 text-center text-xs text-muted-foreground/70">
            {t("workspace:appVersion", { version: __APP_VERSION__ })}
          </p>
        </div>
      </MobileScrollArea>
    </section>
  )
}

/** 展示个人资料、登录与安全或偏好设置表单，并返回个人中心。 */
export function MobileMeSettingsPage({
  section,
}: {
  section: "profile" | "security" | "preferences" | "notifications"
}) {
  const { t } = useTranslation("mobile")
  const { identity } = useMobileWorkspace()
  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t(`me.${section}`)} backTo="/me" />
      <div className="app-form min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        {section === "profile" ? (
          <ProfileSettingsForm user={identity.user} />
        ) : section === "security" ? (
          <ChangePasswordForm />
        ) : section === "notifications" ? (
          <NotificationSettingsForm user={identity.user} />
        ) : (
          <UserPreferencesForm user={identity.user} />
        )}
      </div>
    </section>
  )
}
